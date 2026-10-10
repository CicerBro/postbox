package events

import (
	"context"
	"database/sql"
)

// UnsubscribeMembershipsTx maintains list counters and daily rollups before marking memberships inactive.
func UnsubscribeMembershipsTx(ctx context.Context, c *sql.Conn, subscriber int64, list int64, at int64, reason string) error {
	rows, e := c.QueryContext(ctx, `SELECT list_id FROM list_subscribers WHERE subscriber_id=? AND status='subscribed' AND (?=0 OR list_id=?)`, subscriber, list, list)
	if e != nil {
		return e
	}
	var lists []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		lists = append(lists, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range lists {
		bounce, complaint := 0, 0
		if reason == "bounce" {
			bounce = 1
		}
		if reason == "complaint" {
			complaint = 1
		}
		if _, e = c.ExecContext(ctx, `INSERT INTO list_counters(list_id,active,unsubscribed,bounced,complained) VALUES(?,0,1,?,?) ON CONFLICT(list_id) DO UPDATE SET active=max(0,active-1),unsubscribed=unsubscribed+1,bounced=bounced+excluded.bounced,complained=complained+excluded.complained`, id, bounce, complaint); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `INSERT INTO list_daily_stats(list_id,day,active,unsubscribed,bounced,complained) SELECT ?,?,active,1,?,? FROM list_counters WHERE list_id=? ON CONFLICT(list_id,day) DO UPDATE SET active=excluded.active,unsubscribed=unsubscribed+1,bounced=bounced+excluded.bounced,complained=complained+excluded.complained`, id, at/86400000*86400000, bounce, complaint, id); e != nil {
			return e
		}
	}
	_, e = c.ExecContext(ctx, `UPDATE list_subscribers SET status='unsubscribed',updated_at=? WHERE subscriber_id=? AND (?=0 OR list_id=?)`, at, subscriber, list, list)
	return e
}
