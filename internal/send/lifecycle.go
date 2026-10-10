package send

import (
	"context"
	"database/sql"
	"time"

	"postbox/internal/jobs"
)

// emitLifecycleTx records the event marker and its deliveries with the state change.
// The durable marker survives restarts and pause/resume, even when no webhook is configured.
func (e *Engine) emitLifecycleTx(ctx context.Context, conn *sql.Conn, id int64, event string) error {
	res, err := conn.ExecContext(ctx, `INSERT INTO campaign_lifecycle_events(campaign_id,event,emitted_at) VALUES(?,?,?) ON CONFLICT(campaign_id,event) DO NOTHING`, id, event, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	var brand int64
	var status string
	if err = conn.QueryRowContext(ctx, `SELECT brand_id,status FROM campaigns WHERE id=?`, id).Scan(&brand, &status); err != nil {
		return err
	}
	hook := e.cfg.EventHook
	if hook == nil {
		hook = jobs.EmitTx
	}
	return hook(ctx, conn, brand, event, map[string]any{"campaign_id": id, "status": status})
}

func (e *Engine) finishCampaign(ctx context.Context, id int64) error {
	return e.db.Immediate(ctx, func(conn *sql.Conn) error {
		now := time.Now().UnixMilli()
		res, err := conn.ExecContext(ctx, `UPDATE campaigns SET status='sent',finished_at=?,updated_at=? WHERE id=? AND status='sending' AND NOT EXISTS(SELECT 1 FROM campaign_recipients WHERE campaign_id=? AND status IN ('pending','claimed'))`, now, now, id, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return e.emitLifecycleTx(ctx, conn, id, "campaign_finished")
	})
}

func (e *Engine) cancelCampaign(ctx context.Context, id int64) error {
	err := e.db.Immediate(ctx, func(conn *sql.Conn) error {
		now := time.Now().UnixMilli()
		res, err := conn.ExecContext(ctx, `UPDATE campaigns SET status='cancelled',finished_at=?,updated_at=? WHERE id=? AND status NOT IN ('sent','cancelled')`, now, now, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		if _, err = conn.ExecContext(ctx, `UPDATE campaign_recipients SET status='skipped' WHERE campaign_id=? AND status='pending'`, id); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, `UPDATE campaign_stats SET skipped=(SELECT count(*) FROM campaign_recipients WHERE campaign_id=? AND status='skipped') WHERE campaign_id=?`, id, id); err != nil {
			return err
		}
		return e.emitLifecycleTx(ctx, conn, id, "campaign_finished")
	})
	if err == nil {
		e.mu.Lock()
		if stop := e.running[id]; stop != nil {
			stop()
		}
		e.mu.Unlock()
	}
	return err
}
