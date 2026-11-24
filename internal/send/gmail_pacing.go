package send

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	GmailSendsPerMinuteKey              = "gmail_sends_per_minute"
	MaxGmailSendsPerMinute              = sendsPerMinuteAtMillisecondInterval
	sendsPerMinuteAtMillisecondInterval = int(time.Minute / time.Millisecond)
	gmailLastAttemptKey                 = "gmail_last_campaign_attempt_ms"
)

var errGmailPaced = errors.New("Gmail send interval has not elapsed")
var errGmailRateChanged = errors.New("Gmail campaign send rate changed")

func gmailRecipientSQL(emailColumn string) string {
	return "(lower(" + emailColumn + ") LIKE '%@gmail.com' OR lower(" + emailColumn + ") LIKE '%@googlemail.com')"
}

func isGmailRecipient(email string) bool {
	email = strings.ToLower(email)
	return strings.HasSuffix(email, "@gmail.com") || strings.HasSuffix(email, "@googlemail.com")
}

func sendInterval(rate int) time.Duration {
	if rate == 0 {
		return 0
	}
	// Round up so the timestamps stored by SQLite never permit a higher rate.
	return time.Duration(1+(sendsPerMinuteAtMillisecondInterval-1)/rate) * time.Millisecond
}

func (e *Engine) GmailSendsPerMinute(ctx context.Context) (int, error) {
	value, err := e.db.Setting(ctx, GmailSendsPerMinuteKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rate, err := strconv.Atoi(value)
	if err != nil || rate < 0 || rate > MaxGmailSendsPerMinute {
		return 0, errors.New("invalid stored Gmail campaign send rate")
	}
	return rate, nil
}

func (e *Engine) SetGmailSendsPerMinute(ctx context.Context, rate int) error {
	if rate < 0 || rate > MaxGmailSendsPerMinute {
		return errors.New("invalid Gmail campaign send rate")
	}
	res, err := e.db.Writer.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE settings.value<>excluded.value`, GmailSendsPerMinuteKey, strconv.Itoa(rate))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	// Wake the scheduler so it can rebuild recipient queues with the saved
	// rate. In-flight attempts finish and campaign statuses stay in place.
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, stop := range e.running {
		stop(errGmailRateChanged)
	}
	return nil
}

func (e *Engine) waitForGmailAttempt(ctx context.Context, interval time.Duration) error {
	var last int64
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT coalesce((SELECT cast(value AS INTEGER) FROM settings WHERE key=?), 0)`, gmailLastAttemptKey).Scan(&last); err != nil {
		return err
	}
	remaining := time.Until(time.UnixMilli(last).Add(interval))
	if remaining <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func reserveGmailAttempt(ctx context.Context, conn *sql.Conn, now int64) error {
	// Read the current cap in the same transaction as the dispatch ledger.
	// This also records attempts made while the cap is off.
	res, err := conn.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
		WHERE cast(settings.value AS INTEGER)<=? - coalesce((
			SELECT 1 + (? - 1) / cast(value AS INTEGER) FROM settings WHERE key=?
		), 0)`, gmailLastAttemptKey, now, now, MaxGmailSendsPerMinute, GmailSendsPerMinuteKey)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return errGmailPaced
	}
	return err
}
