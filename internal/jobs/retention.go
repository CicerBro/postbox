package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"postbox/internal/store"
)

const FailedJobRetentionKey = "failed_job_retention_days"
const DefaultFailedJobRetentionDays = 30
const MaxFailedJobRetentionDays = 3650

// FailedJobRetentionDays reads the application-wide policy on each maintenance run.
func FailedJobRetentionDays(ctx context.Context, db *store.DB) (int, error) {
	value, err := db.Setting(ctx, FailedJobRetentionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultFailedJobRetentionDays, nil
	}
	if err != nil {
		return 0, err
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > MaxFailedJobRetentionDays {
		return 0, errors.New("failed job retention days must be between 1 and 3650")
	}
	return days, nil
}

// PurgeFailed removes only expired terminal failures, committing bounded batches.
func (r *Runner) PurgeFailed(ctx context.Context, before time.Time, batch int) error {
	if batch < 1 || batch > 10000 {
		return errors.New("retention batch size must be between 1 and 10000")
	}
	for {
		var removed int64
		err := r.db.Immediate(ctx, func(c *sql.Conn) error {
			res, err := c.ExecContext(ctx, `DELETE FROM jobs WHERE id IN (SELECT id FROM jobs WHERE status='failed' AND failed_at<? ORDER BY failed_at,id LIMIT ?)`, before.UnixMilli(), batch)
			if err != nil {
				return err
			}
			removed, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if removed < int64(batch) {
			return nil
		}
	}
}
