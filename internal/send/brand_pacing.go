package send

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const MaxBrandSendsPerMinute = sendsPerMinuteAtMillisecondInterval
const brandLastAttemptKey = "last_send_attempt_ms"

var errBrandPaced = errors.New("brand send interval has not elapsed")

func (e *Engine) brandSendsPerMinute(ctx context.Context, brandID int64) (int, error) {
	var rate int
	err := e.db.Reader.QueryRowContext(ctx, `SELECT sends_per_minute FROM brands WHERE id=?`, brandID).Scan(&rate)
	return rate, err
}

// acquireBrandPacing lets one capped sender per brand wait for its next
// attempt and the SES token, leaving shared SES capacity for other brands.
func (e *Engine) acquireBrandPacing(ctx context.Context, brandID int64) (func(), error) {
	if brandID == 0 {
		return func() {}, ctx.Err()
	}
	rate, err := e.brandSendsPerMinute(ctx, brandID)
	if err != nil || rate == 0 {
		return func() {}, err
	}
	stored, ok := e.brandPacing.Load(brandID)
	if !ok {
		stored, _ = e.brandPacing.LoadOrStore(brandID, make(chan struct{}, 1))
	}
	gate := stored.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case gate <- struct{}{}:
	}
	release := sync.OnceFunc(func() { <-gate })
	if err := e.waitForBrandAttempt(ctx, brandID); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (e *Engine) waitForBrandAttempt(ctx context.Context, brandID int64) error {
	for {
		var rate int
		var last int64
		err := e.db.Reader.QueryRowContext(ctx, `SELECT b.sends_per_minute, coalesce(cast(s.value AS INTEGER), 0)
			FROM brands b LEFT JOIN brand_settings s ON s.brand_id=b.id AND s.name=? WHERE b.id=?`, brandLastAttemptKey, brandID).Scan(&rate, &last)
		if err != nil {
			return err
		}
		remaining := time.Until(time.UnixMilli(last).Add(sendInterval(rate)))
		if rate == 0 || remaining <= 0 {
			return ctx.Err()
		}
		// Re-read the cap while waiting so a change takes effect on active sends.
		timer := time.NewTimer(min(remaining, time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func reserveBrandAttempt(ctx context.Context, conn *sql.Conn, brandID, now int64) error {
	// Keep the last attempt even when the cap is off so enabling it spaces the
	// next attempt from the most recent send.
	res, err := conn.ExecContext(ctx, `INSERT INTO brand_settings(brand_id,name,value) VALUES(?,?,?)
		ON CONFLICT(brand_id,name) DO UPDATE
		SET value=max(cast(brand_settings.value AS INTEGER), cast(excluded.value AS INTEGER))
		WHERE EXISTS (
			SELECT 1 FROM brands WHERE id=? AND (
				sends_per_minute=0 OR
				cast(brand_settings.value AS INTEGER)<=? - (1 + (? - 1) / sends_per_minute)
			)
		)`, brandID, brandLastAttemptKey, now, brandID, now, MaxBrandSendsPerMinute)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return errBrandPaced
	}
	return err
}

// waitForSend reserves a brand attempt for sends without a campaign ledger.
func (d *brandDelivery) waitForSend(ctx context.Context, brandID int64) error {
	if brandID == 0 {
		return d.wait(ctx)
	}
	for {
		err := d.reserveSendSlot(ctx, brandID)
		if !errors.Is(err, errBrandPaced) {
			return err
		}
	}
}

func (d *brandDelivery) reserveSendSlot(ctx context.Context, brandID int64) error {
	release, err := d.engine.acquireBrandPacing(ctx, brandID)
	if err != nil {
		return err
	}
	defer release()
	if err := d.wait(ctx); err != nil {
		return err
	}
	return d.engine.db.Immediate(ctx, func(conn *sql.Conn) error {
		return reserveBrandAttempt(ctx, conn, brandID, time.Now().UnixMilli())
	})
}
