package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const sendHistoryRetention = 180 * 24 * time.Hour

const purgeRecipientMessageIDsSQL = `UPDATE campaign_recipients SET message_id=NULL
WHERE (campaign_id,subscriber_id) IN (
	SELECT campaign_id,subscriber_id FROM campaign_recipients
	WHERE message_id IS NOT NULL AND sent_at IS NOT NULL AND sent_at<? AND status<>'claimed'
	ORDER BY sent_at,campaign_id,subscriber_id LIMIT ?
)`

const purgeSendAttemptsSQL = `DELETE FROM send_attempts WHERE id IN (
	SELECT id FROM send_attempts
	WHERE finished_at IS NOT NULL AND finished_at<? AND outcome<>'started'
	ORDER BY finished_at,id LIMIT ?
)`

// PurgeSendHistory expires provider IDs and completed attempt details in small
// transactions. Recipient history, aggregate counters and the dispatch ledger
// remain intact, including the ledger's protection against repeat sends.
// Records without a known send/completion time and in-flight work are retained.
func (r *Runner) PurgeSendHistory(ctx context.Context, before time.Time, batch int) error {
	if batch <= 0 || batch > 10000 {
		return errors.New("retention batch size must be between 1 and 10000")
	}
	for _, q := range []string{purgeRecipientMessageIDsSQL, purgeSendAttemptsSQL} {
		for {
			var n int64
			err := r.db.Immediate(ctx, func(c *sql.Conn) error {
				res, err := c.ExecContext(ctx, q, before.UnixMilli(), batch)
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
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}
