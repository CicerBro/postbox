package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MaintenanceConfig struct {
	RawRetention                  time.Duration
	UnconfirmedRetention          time.Duration
	Interval                      time.Duration
	BatchSize                     int
	BackupDirectory               string
	BackupInterval, BackupTimeout time.Duration
}

func (r *Runner) RegisterMaintenance(cfg MaintenanceConfig) error {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.BatchSize > 10000 {
		return errors.New("maintenance batch size must not exceed 10000")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.BackupInterval <= 0 {
		cfg.BackupInterval = 24 * time.Hour
	}
	if cfg.BackupTimeout <= 0 {
		cfg.BackupTimeout = 30 * time.Second
	}
	if cfg.BackupTimeout > time.Minute {
		return errors.New("backup timeout must not exceed one minute")
	}
	if err := r.RegisterRecurring("housekeeping", cfg.Interval, func(ctx context.Context, j Job) error {
		days, err := FailedJobRetentionDays(ctx, r.db)
		if err != nil {
			return err
		}
		if err := r.PurgeFailed(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour), cfg.BatchSize); err != nil {
			return err
		}
		if cfg.RawRetention > 0 {
			if err := r.PurgeRaw(ctx, time.Now().Add(-cfg.RawRetention), cfg.BatchSize); err != nil {
				return err
			}
		}
		if err := r.PurgeSendHistory(ctx, time.Now().Add(-sendHistoryRetention), cfg.BatchSize); err != nil {
			return err
		}
		if cfg.UnconfirmedRetention > 0 {
			if err := r.PurgeUnconfirmed(ctx, time.Now().Add(-cfg.UnconfirmedRetention), cfg.BatchSize); err != nil {
				return err
			}
		}
		if err := r.Rollup(ctx, time.Now()); err != nil {
			return err
		}
		if _, err := r.db.Writer.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if cfg.BackupDirectory != "" {
		if err := r.RegisterRecurring("backup", cfg.BackupInterval, func(ctx context.Context, j Job) error {
			ctx, cancel := context.WithTimeout(ctx, cfg.BackupTimeout)
			defer cancel()
			if err := os.MkdirAll(cfg.BackupDirectory, 0700); err != nil {
				return err
			}
			path := filepath.Join(cfg.BackupDirectory, fmt.Sprintf("postbox-%d-%d.sqlite", time.Now().UnixMilli(), j.ID))
			if err := r.Backup(ctx, path); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleMaintenance persists registered maintenance schedules without moving due times.
func (r *Runner) ScheduleMaintenance(ctx context.Context) error {
	return r.ScheduleRecurring(ctx)
}

// PurgeRaw commits each small batch independently and never modifies aggregate counters.
func (r *Runner) PurgeRaw(ctx context.Context, before time.Time, batch int) error {
	if batch <= 0 || batch > 10000 {
		return errors.New("retention batch size must be between 1 and 10000")
	}
	for _, table := range []string{"opens", "clicks", "ses_events"} {
		for {
			var n int64
			err := r.db.Immediate(ctx, func(c *sql.Conn) error {
				res, err := c.ExecContext(ctx, "DELETE FROM "+table+" WHERE id IN (SELECT id FROM "+table+" WHERE at<? ORDER BY at,id LIMIT ?)", before.UnixMilli(), batch)
				if err != nil {
					return err
				}
				n, err = res.RowsAffected()
				return err
			})
			if err != nil {
				return err
			}
			if n < int64(batch) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
	return nil
}
func (r *Runner) PurgeUnconfirmed(ctx context.Context, before time.Time, batch int) error {
	if batch <= 0 || batch > 10000 {
		return errors.New("housekeeping batch size must be between 1 and 10000")
	}
	for {
		var n int64
		err := r.db.Immediate(ctx, func(c *sql.Conn) error {
			const expired = `SELECT list_id,subscriber_id FROM list_subscribers WHERE status='unconfirmed' AND COALESCE(confirmation_started_at,updated_at)<? ORDER BY COALESCE(confirmation_started_at,updated_at),list_id,subscriber_id LIMIT ?`
			if _, err := c.ExecContext(ctx, `DELETE FROM subscription_confirmations WHERE (list_id,subscriber_id) IN (`+expired+`)`, before.UnixMilli(), batch); err != nil {
				return err
			}
			res, err := c.ExecContext(ctx, `DELETE FROM list_subscribers WHERE (list_id,subscriber_id) IN (`+expired+`)`, before.UnixMilli(), batch)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if n < int64(batch) {
			return nil
		}
	}
}

// Rollup snapshots persisted list counters. It performs no raw-event or subscriber counts.
func (r *Runner) Rollup(ctx context.Context, at time.Time) error {
	day := time.Date(at.UTC().Year(), at.UTC().Month(), at.UTC().Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	return r.db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, `INSERT INTO list_daily_stats(list_id,day,active,subscribed,unsubscribed,bounced,complained)
SELECT x.list_id,?,x.active,
max(0,x.subscribed-coalesce(p.subscribed,0)),max(0,x.unsubscribed-coalesce(p.unsubscribed,0)),
max(0,x.bounced-coalesce(p.bounced,0)),max(0,x.complained-coalesce(p.complained,0))
FROM list_counters x LEFT JOIN list_counter_snapshots p ON p.list_id=x.list_id AND p.day=(SELECT max(day) FROM list_counter_snapshots WHERE list_id=x.list_id AND day<?)
WHERE 1 ON CONFLICT(list_id,day) DO UPDATE SET active=excluded.active,subscribed=excluded.subscribed,unsubscribed=excluded.unsubscribed,bounced=excluded.bounced,complained=excluded.complained`, day, day)
		if err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, `INSERT INTO list_counter_snapshots(list_id,day,subscribed,unsubscribed,bounced,complained)
SELECT list_id,?,subscribed,unsubscribed,bounced,complained FROM list_counters WHERE 1
ON CONFLICT(list_id,day) DO UPDATE SET subscribed=excluded.subscribed,unsubscribed=excluded.unsubscribed,bounced=excluded.bounced,complained=excluded.complained`, day)
		return err
	})
}

// Backup uses the writer because the reader pool is query-only. VACUUM INTO runs
// outside a write transaction and respects the caller's deadline.
func (r *Runner) Backup(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("backup path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err = os.Stat(abs); err == nil {
		return errors.New("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err = r.db.Writer.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(abs, "'", "''")+"'")
	if err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("online backup: %w", err)
	}
	if err = os.Chmod(abs, 0600); err != nil {
		return err
	}
	return nil
}

// DecodePayload helps handlers validate typed import/export job arguments.
func DecodePayload(j Job, target any) error { return json.Unmarshal(j.Payload, target) }
