package send

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"postbox/internal/jobs"
	"postbox/internal/store"
)

func TestDispatchLedgerPreventsRepeatSendAfterHistoryExpires(t *testing.T) {
	for _, outcome := range []string{"sent", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			db, e, _ := fixture(t, 1)
			ctx := context.Background()
			if err := e.ensureDispatchLedger(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, err := ledgerAttempt(t, e, 1, 1, "reader1@example.org"); !ok || err != nil {
				t.Fatalf("initial dispatch: accepted=%v error=%v", ok, err)
			}
			finishLedger(t, e, 1, 1, outcome)
			if _, err := db.Writer.Exec(`UPDATE send_attempts SET outcome=?,finished_at=1,message_id='expired' WHERE campaign_id=1`, outcome); err != nil {
				t.Fatal(err)
			}
			if err := jobs.New(db, jobs.Config{}).PurgeSendHistory(ctx, time.UnixMilli(500), 1); err != nil {
				t.Fatal(err)
			}
			var attempts int
			if err := db.Reader.QueryRow(`SELECT count(*) FROM send_attempts`).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatalf("expired attempts=%d error=%v", attempts, err)
			}
			if err := e.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, err := ledgerAttempt(t, e, 1, 2, "reader1@example.org"); ok || !errors.Is(err, errUnsafeAttempt) {
				t.Fatalf("expired history allowed repeat send: accepted=%v error=%v", ok, err)
			}
		})
	}
}

func ledgerAttempt(t *testing.T, e *Engine, sid int64, attempt int, email string) (bool, error) {
	t.Helper()
	ok := false
	err := e.db.Immediate(context.Background(), func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(context.Background(), `INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(1,?,?,1,'started')`, sid, attempt); err != nil {
			return err
		}
		var err error
		ok, err = e.beginDispatchTx(context.Background(), conn, 1, sid, attempt, email)
		if err != nil {
			return err
		}
		if !ok {
			return errUnsafeAttempt
		}
		return nil
	})
	return ok, err
}
func finishLedger(t *testing.T, e *Engine, sid int64, attempt int, outcome string) {
	t.Helper()
	if err := e.db.Immediate(context.Background(), func(conn *sql.Conn) error {
		return e.finishDispatchTx(context.Background(), conn, 1, sid, attempt, outcome)
	}); err != nil {
		t.Fatal(err)
	}
}
func TestDispatchLedgerSurvivesErasure(t *testing.T) {
	db, e, _ := fixture(t, 1)
	ctx := context.Background()
	if err := e.ensureDispatchLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := ledgerAttempt(t, e, 1, 1, " Reader1@Example.org "); err != nil || !ok {
		t.Fatalf("acquire %v %v", ok, err)
	}
	if _, err := db.Writer.Exec(`DELETE FROM subscribers WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(2,1,'reader1@example.org',1,1)`); err != nil {
		t.Fatal(err)
	}
	if ok, err := ledgerAttempt(t, e, 2, 1, "reader1@example.org"); ok || !errors.Is(err, errUnsafeAttempt) {
		t.Fatalf("erased identity dispatched: %v %v", ok, err)
	}
	var count int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_dispatch_ledger`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger %d %v", count, err)
	}
}
func TestDispatchLedgerRejectionAndUnknown(t *testing.T) {
	_, e, _ := fixture(t, 1)
	if err := e.ensureDispatchLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, err := ledgerAttempt(t, e, 1, 1, "reader1@example.org"); !ok || err != nil {
		t.Fatal(err)
	}
	finishLedger(t, e, 1, 1, "rejected")
	if ok, err := ledgerAttempt(t, e, 1, 2, "reader1@example.org"); !ok || err != nil {
		t.Fatalf("rejection prevented retry %v %v", ok, err)
	}
	// A late finish for generation one cannot unlock the second request.
	finishLedger(t, e, 1, 1, "rejected")
	if ok, err := ledgerAttempt(t, e, 1, 3, "reader1@example.org"); ok || !errors.Is(err, errUnsafeAttempt) {
		t.Fatalf("stale finisher unlocked %v %v", ok, err)
	}
	finishLedger(t, e, 1, 2, "unknown")
	finishLedger(t, e, 1, 2, "rejected")
	if ok, err := ledgerAttempt(t, e, 1, 3, "reader1@example.org"); ok || !errors.Is(err, errUnsafeAttempt) {
		t.Fatalf("unknown unlocked %v %v", ok, err)
	}
}
func TestDispatchLedgerConcurrentAcquire(t *testing.T) {
	_, e, _ := fixture(t, 8)
	if err := e.ensureDispatchLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for sid := int64(1); sid <= 8; sid++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			ok, err := ledgerAttempt(t, e, id, 1, "shared@example.org")
			if ok {
				accepted.Add(1)
			}
			if err != nil && !errors.Is(err, errUnsafeAttempt) {
				t.Error(err)
			}
		}(sid)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("acquired %d times", accepted.Load())
	}
}
func TestDispatchLedgerSecretStableAndLegacyBackfill(t *testing.T) {
	db, e, _ := fixture(t, 1)
	ctx := context.Background()
	c, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`UPDATE campaign_recipients SET status='unknown' WHERE subscriber_id=1`); err != nil {
		t.Fatal(err)
	}
	if err = e.ensureDispatchLedger(ctx); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err = db.Reader.QueryRow(`SELECT value FROM settings WHERE key='dispatch_ledger_secret'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = e.ensureDispatchLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Reader.QueryRow(`SELECT value FROM settings WHERE key='dispatch_ledger_secret'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || len(before) != 64 {
		t.Fatal("unstable database secret")
	}
	if ok, err := ledgerAttempt(t, e, 1, 1, "reader1@example.org"); ok || !errors.Is(err, errUnsafeAttempt) {
		t.Fatalf("legacy unknown dispatched %v %v", ok, err)
	}
}
func TestDispatchLedgerFailsClosedWithoutBootstrap(t *testing.T) {
	_, e, _ := fixture(t, 1)
	if ok, err := ledgerAttempt(t, e, 1, 1, "reader1@example.org"); ok || err == nil {
		t.Fatalf("uninitialized ledger dispatched %v %v", ok, err)
	}
}

func TestDispatchLedgerSecretSurvivesDatabaseReopen(t *testing.T) {
	db, e, _ := fixture(t, 1)
	if err := e.ensureDispatchLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := db.Reader.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.Reader.QueryRow(`SELECT value FROM settings WHERE key='dispatch_ledger_secret'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	other := New(reopened, e.sender, Config{TokenSecret: []byte("different-tracking-secret")})
	if err := other.ensureDispatchLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := reopened.Reader.QueryRow(`SELECT value FROM settings WHERE key='dispatch_ledger_secret'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("database reopen changed dispatch secret")
	}
}
