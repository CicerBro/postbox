package consent

import (
	"context"
	"database/sql"
)

// UnsubscribeMembershipsTx maintains list counters and daily rollups before marking memberships inactive.
func UnsubscribeMembershipsTx(ctx context.Context, c *sql.Conn, subscriber int64, list int64, at int64, reason string) ([]int64, error) {
	rows, e := c.QueryContext(ctx, `SELECT list_id,status FROM list_subscribers WHERE subscriber_id=? AND status!='unsubscribed' AND (?=0 OR list_id=?)`, subscriber, list, list)
	if e != nil {
		return nil, e
	}
	var lists []int64
	subscribed := map[int64]bool{}
	for rows.Next() {
		var id int64
		var status string
		if e = rows.Scan(&id, &status); e != nil {
			rows.Close()
			return nil, e
		}
		lists = append(lists, id)
		subscribed[id] = status == "subscribed"
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, id := range lists {
		if !subscribed[id] {
			continue
		}
		var bounce, complaint int64
		if reason == "bounce" {
			bounce = 1
		}
		if reason == "complaint" {
			complaint = 1
		}
		if e = UpdateCountersTx(ctx, c, id, -1, 0, 1, bounce, complaint, at); e != nil {
			return nil, e
		}
	}
	_, e = c.ExecContext(ctx, `UPDATE list_subscribers SET status='unsubscribed',updated_at=?,confirmation_started_at=NULL WHERE subscriber_id=? AND (?=0 OR list_id=?)`, at, subscriber, list, list)
	if e != nil {
		return nil, e
	}
	_, e = c.ExecContext(ctx, `DELETE FROM subscription_confirmations WHERE subscriber_id=? AND (?=0 OR list_id=?)`, subscriber, list, list)
	return lists, e
}

// UpdateCountersTx records a membership transition in lifetime and daily counters.
func UpdateCountersTx(ctx context.Context, c *sql.Conn, listID, delta, subscribed, unsubscribed, bounced, complained, at int64) error {
	if _, err := c.ExecContext(ctx, `INSERT INTO list_counters(list_id,active,subscribed,unsubscribed,bounced,complained) VALUES(?,max(0,?),?,?,?,?) ON CONFLICT(list_id) DO UPDATE SET active=max(0,active+?),subscribed=subscribed+excluded.subscribed,unsubscribed=unsubscribed+excluded.unsubscribed,bounced=bounced+excluded.bounced,complained=complained+excluded.complained`, listID, delta, subscribed, unsubscribed, bounced, complained, delta); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO list_active_history(list_id,at,active,subscribed,unsubscribed)
SELECT list_id,?,active,?,? FROM list_counters WHERE list_id=?
ON CONFLICT(list_id,at) DO UPDATE SET active=excluded.active,subscribed=subscribed+excluded.subscribed,unsubscribed=unsubscribed+excluded.unsubscribed`, at, subscribed, unsubscribed, listID); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, `INSERT INTO list_daily_stats(list_id,day,active,subscribed,unsubscribed,bounced,complained) SELECT ?,?,active,?,?,?,? FROM list_counters WHERE list_id=? ON CONFLICT(list_id,day) DO UPDATE SET active=excluded.active,subscribed=subscribed+excluded.subscribed,unsubscribed=unsubscribed+excluded.unsubscribed,bounced=bounced+excluded.bounced,complained=complained+excluded.complained`, listID, at/86400000*86400000, subscribed, unsubscribed, bounced, complained, listID)
	return err
}

// RecordBrandOptOutTx persists a brand marketing opt-out and revokes pending renewals.
// Callers validate identity and apply membership/audience transitions in this transaction.
func RecordBrandOptOutTx(ctx context.Context, c *sql.Conn, brandID, subscriber, at int64) error {
	if _, err := c.ExecContext(ctx, `UPDATE subscribers SET marketing_unsubscribed_at=coalesce(marketing_unsubscribed_at,?),updated_at=? WHERE id=? AND brand_id=?`, at, at, subscriber, brandID); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, `DELETE FROM subscription_confirmations WHERE subscriber_id=? AND EXISTS(SELECT 1 FROM subscribers WHERE id=? AND brand_id=?)`, subscriber, subscriber, brandID)
	return err
}
