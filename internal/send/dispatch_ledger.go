package send

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The secret belongs to the database, rather than a rotating tracking configuration.
// Losing or replacing it would lose the ability to recognize previous dispatches.
func dispatchSecretTx(ctx context.Context, conn *sql.Conn) ([]byte, error) {
	var value string
	err := conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='dispatch_ledger_secret'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM campaign_dispatch_ledger`).Scan(&count); err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, errors.New("dispatch ledger secret is missing")
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		value = hex.EncodeToString(key)
		if _, err = conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('dispatch_ledger_secret',?)`, value); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid dispatch ledger secret")
	}
	return key, nil
}
func dispatchHash(key []byte, email string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	return mac.Sum(nil)
}

// Bootstrap scans immutable snapshots in bounded transactions before dispatch is
// permitted. A persisted cursor makes interrupted upgrades resumable. Legacy
// attempts are conservatively held because old classifications were less strict.
func (e *Engine) ensureDispatchLedger(ctx context.Context) error {
	for {
		done := false
		err := e.db.Immediate(ctx, func(conn *sql.Conn) error {
			secret, err := dispatchSecretTx(ctx, conn)
			if err != nil {
				return err
			}
			var cursor string
			if err = conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='dispatch_ledger_bootstrap'`).Scan(&cursor); err != nil {
				return err
			}
			if cursor == "done" {
				done = true
				return nil
			}
			var cid, sid int64
			if _, err = fmt.Sscanf(cursor, "%d:%d", &cid, &sid); err != nil {
				return errors.New("invalid dispatch ledger bootstrap cursor")
			}
			rows, err := conn.QueryContext(ctx, `SELECT r.campaign_id,r.subscriber_id,coalesce(r.snapshot_email,s.email),r.attempts FROM campaign_recipients r JOIN subscribers s ON s.id=r.subscriber_id WHERE (r.campaign_id,r.subscriber_id)>(?,?) ORDER BY r.campaign_id,r.subscriber_id LIMIT 500`, cid, sid)
			if err != nil {
				return err
			}
			type old struct {
				cid, sid int64
				email    string
				attempts int
			}
			var batch []old
			for rows.Next() {
				var r old
				if err = rows.Scan(&r.cid, &r.sid, &r.email, &r.attempts); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, r)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, r := range batch {
				var unsafe int
				if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=? AND (attempts>0 OR status IN ('claimed','sent','unknown'))) OR EXISTS(SELECT 1 FROM send_attempts WHERE campaign_id=? AND subscriber_id=?)`, r.cid, r.sid, r.cid, r.sid).Scan(&unsafe); err != nil {
					return err
				}
				if unsafe != 0 {
					if _, err = conn.ExecContext(ctx, `INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(?,?,'unknown',1,?,?,?) ON CONFLICT(campaign_id,email_hash) DO NOTHING`, r.cid, dispatchHash(secret, r.email), r.sid, r.attempts, time.Now().UnixMilli()); err != nil {
						return err
					}
				}
			}
			if len(batch) < 500 {
				cursor = "done"
				done = true
			} else {
				last := batch[len(batch)-1]
				cursor = fmt.Sprintf("%d:%d", last.cid, last.sid)
			}
			_, err = conn.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='dispatch_ledger_bootstrap'`, cursor)
			return err
		})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// Call after inserting the journal row, inside the same immediate transaction.
// The durable generation fences delayed completions after identity recreation.
func (e *Engine) beginDispatchTx(ctx context.Context, conn *sql.Conn, cid, sid int64, attempt int, email string) (bool, error) {
	var ready string
	if err := conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='dispatch_ledger_bootstrap'`).Scan(&ready); err != nil {
		return false, err
	}
	if ready != "done" {
		return false, errors.New("dispatch ledger bootstrap is incomplete")
	}
	secret, err := dispatchSecretTx(ctx, conn)
	if err != nil {
		return false, err
	}
	digest := dispatchHash(secret, email)
	var generation int64
	err = conn.QueryRowContext(ctx, `INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(?,?,'started',1,?,?,?) ON CONFLICT(campaign_id,email_hash) DO UPDATE SET outcome='started',generation=generation+1,owner_subscriber_id=excluded.owner_subscriber_id,owner_attempt=excluded.owner_attempt,updated_at=excluded.updated_at WHERE campaign_dispatch_ledger.outcome='rejected' RETURNING generation`, cid, digest, sid, attempt, time.Now().UnixMilli()).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := conn.ExecContext(ctx, `UPDATE send_attempts SET dispatch_generation=? WHERE campaign_id=? AND subscriber_id=? AND attempt=? AND outcome='started'`, generation, cid, sid, attempt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, errors.New("dispatch journal is missing")
	}
	return true, nil
}

// Only an explicit definitive rejection unlocks another dispatch. Never derive
// rejection from a generic recipient failure or an administrative status reset.
func (e *Engine) finishDispatchTx(ctx context.Context, conn *sql.Conn, cid, sid int64, attempt int, outcome string) error {
	if outcome != "rejected" && outcome != "sent" && outcome != "unknown" {
		return errors.New("invalid dispatch outcome")
	}
	// Without an explicit owner access path SQLite can prefer the WITHOUT ROWID
	// primary key's campaign prefix, visiting every dispatch for each completion.
	// Keep completion bounded to the attempt owner while retaining the durable
	// generation check that fences stale finishers.
	_, err := conn.ExecContext(ctx, `UPDATE campaign_dispatch_ledger INDEXED BY campaign_dispatch_owner SET outcome=?,updated_at=? WHERE campaign_id=? AND owner_subscriber_id=? AND owner_attempt=? AND outcome='started' AND generation=(SELECT dispatch_generation FROM send_attempts WHERE campaign_id=? AND subscriber_id=? AND attempt=?)`, outcome, time.Now().UnixMilli(), cid, sid, attempt, cid, sid, attempt)
	return err
}
