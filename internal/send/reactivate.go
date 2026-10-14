package send

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrCannotReactivate = errors.New("only cancelled campaigns with no sent, uncertain, or active sends can return to draft")

// Keep the eligibility read and the state transition on the same predicate.
// The ledger survives subscriber deletion, so recipient counts alone are insufficient.
const reactivatableCampaignSQL = `campaigns.status='cancelled'
	AND NOT EXISTS(SELECT 1 FROM campaign_stats WHERE campaign_id=campaigns.id AND (sent>0 OR unknown>0 OR delivered>0))
	AND NOT EXISTS(SELECT 1 FROM campaign_recipients WHERE campaign_id=campaigns.id AND (status IN ('sent','unknown','claimed') OR sent_at IS NOT NULL))
	AND NOT EXISTS(SELECT 1 FROM send_attempts WHERE campaign_id=campaigns.id AND outcome IN ('started','sent','unknown'))
	AND NOT EXISTS(SELECT 1 FROM campaign_dispatch_ledger WHERE campaign_id=campaigns.id AND outcome IN ('started','sent','unknown'))`

func (e *Engine) CanReactivate(ctx context.Context, id int64) (bool, error) {
	e.mu.Lock()
	running := e.running[id] != nil
	e.mu.Unlock()
	if running {
		return false, nil
	}
	var allowed bool
	err := e.db.Reader.QueryRowContext(ctx, `SELECT `+reactivatableCampaignSQL+` FROM campaigns WHERE id=?`, id).Scan(&allowed)
	return allowed, err
}

func (e *Engine) Reactivate(ctx context.Context, id int64) error {
	// A cancelled run must finish preparation and flush its results before reset.
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running[id] != nil {
		return ErrCannotReactivate
	}
	return e.db.Immediate(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `UPDATE campaigns SET status='draft',scheduled_at=NULL,started_at=NULL,finished_at=NULL,updated_at=? WHERE id=? AND `+reactivatableCampaignSQL, time.Now().UnixMilli(), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrCannotReactivate
		}
		for _, table := range []string{"campaign_preparations", "campaign_recipients", "campaign_stats", "links", "send_attempts", "campaign_dispatch_ledger", "campaign_lifecycle_events"} {
			if _, err := conn.ExecContext(ctx, "DELETE FROM "+table+" WHERE campaign_id=?", id); err != nil {
				return err
			}
		}
		return nil
	})
}
