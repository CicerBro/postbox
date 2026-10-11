package subscribers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"postbox/internal/jobs"
)

func (s *Service) Confirm(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	return s.db.Immediate(ctx, func(c *sql.Conn) error {
		var list, id int64
		var renew bool
		if e := c.QueryRowContext(ctx, "SELECT list_id,subscriber_id,resubscribe_brand FROM subscription_confirmations WHERE token_hash=? AND expires_at>?", hash[:], nowMS()).Scan(&list, &id, &renew); e != nil {
			return errors.New("confirmation token invalid or expired")
		}
		if renew {
			if _, e := c.ExecContext(ctx, `UPDATE subscribers SET marketing_unsubscribed_at=NULL,gdpr_consent_at=? WHERE id=? AND status='active' AND EXISTS(SELECT 1 FROM list_subscribers ls WHERE ls.subscriber_id=subscribers.id AND ls.list_id=? AND ls.status='unconfirmed') AND NOT EXISTS(SELECT 1 FROM suppressions p WHERE (p.brand_id IS NULL OR p.brand_id=subscribers.brand_id) AND ((p.kind='email' AND p.value=subscribers.email) OR (p.kind='domain' AND p.value=subscribers.domain)))`, nowMS(), id, list); e != nil {
				return e
			}
		}
		r, e := c.ExecContext(ctx, "UPDATE list_subscribers SET status='subscribed',updated_at=?,confirmation_started_at=NULL WHERE list_id=? AND subscriber_id=? AND status='unconfirmed' AND EXISTS (SELECT 1 FROM subscribers s WHERE s.id=? AND s.status='active' AND s.marketing_unsubscribed_at IS NULL AND NOT EXISTS (SELECT 1 FROM suppressions p WHERE (p.brand_id IS NULL OR p.brand_id=s.brand_id) AND ((p.kind='email' AND p.value=s.email) OR (p.kind='domain' AND p.value=s.domain))))", nowMS(), list, id, id)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			return errors.New("confirmation token no longer matches an eligible subscription")
		}
		if n > 0 {
			if e = membershipCounter(ctx, c, list, 1, 1, 0, nowMS()); e != nil {
				return e
			}
			var brand int64
			if e = c.QueryRowContext(ctx, "SELECT brand_id FROM lists WHERE id=?", list).Scan(&brand); e != nil {
				return e
			}
			if e = jobs.EmitTx(ctx, c, brand, "subscribe", map[string]any{"subscriber_id": id, "list_id": list}); e != nil {
				return e
			}
			if e = EnqueueThankyouTx(ctx, c, brand, list, id); e != nil {
				return e
			}
		}
		_, e = c.ExecContext(ctx, "DELETE FROM subscription_confirmations WHERE subscriber_id=? AND list_id=?", id, list)
		return e
	})
}
