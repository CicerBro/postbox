package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RegisterRecurring separates a queue's schedule from its handler and retries.
// Call ScheduleRecurring after registration to persist configured schedules.
func (r *Runner) RegisterRecurring(queue string, interval time.Duration, h Handler) error {
	if interval < time.Millisecond {
		return errors.New("recurring job interval must be at least one millisecond")
	}
	if err := r.Register(queue, h); err != nil {
		return err
	}
	r.mu.Lock()
	r.recurrences[queue] = interval
	r.mu.Unlock()
	return nil
}

// ScheduleRecurring preserves due times through restarts and configuration changes.
// Existing jobs are adopted when upgrading from handler-managed recurrence.
func (r *Runner) ScheduleRecurring(ctx context.Context) error {
	r.mu.RLock()
	schedules := make(map[string]time.Duration, len(r.recurrences))
	for queue, interval := range r.recurrences {
		schedules[queue] = interval
	}
	r.mu.RUnlock()
	return r.db.Immediate(ctx, func(c *sql.Conn) error {
		now := time.Now().UnixMilli()
		for queue, interval := range schedules {
			_, err := c.ExecContext(ctx, `INSERT INTO recurring_jobs(queue,interval_ms,next_run_at)
VALUES(?,?,COALESCE((SELECT min(run_at)+? FROM jobs WHERE queue=? AND status IN ('pending','running')),?))
ON CONFLICT(queue) DO UPDATE SET interval_ms=excluded.interval_ms`, queue, interval.Milliseconds(), interval.Milliseconds(), queue, now)
			if err != nil {
				return err
			}
			if err := materializeRecurring(ctx, c, queue, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Materialization and claiming share a transaction, so workers cannot overlap
// occurrences. Skip missed intervals instead of flooding the queue after downtime.
func materializeRecurring(ctx context.Context, c *sql.Conn, queue string, now int64) error {
	var interval, due int64
	err := c.QueryRowContext(ctx, `SELECT interval_ms,next_run_at FROM recurring_jobs
WHERE queue=? AND next_run_at<=? AND NOT EXISTS
(SELECT 1 FROM jobs WHERE queue=? AND status IN ('pending','running'))`, queue, now, queue).Scan(&interval, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES(?,'{}','pending',?,?)`, queue, due, now); err != nil {
		return err
	}
	next := due + ((now-due)/interval+1)*interval
	_, err = c.ExecContext(ctx, `UPDATE recurring_jobs SET next_run_at=? WHERE queue=?`, next, queue)
	return err
}
