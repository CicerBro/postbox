package tracking

import (
	"context"
	"database/sql"
	"encoding/json"
	"postbox/internal/contacts"
	"strings"
)

// skipAfterUnsubscribe reevaluates included audiences for a per-list removal.
func skipAfterUnsubscribe(ctx context.Context, c *sql.Conn, subscriber, list int64) error {
	rows, e := c.QueryContext(ctx, `SELECT cr.campaign_id FROM campaign_recipients cr JOIN campaigns ca ON ca.id=cr.campaign_id WHERE cr.subscriber_id=? AND cr.status='pending' AND ca.status IN('preparing','sending','paused')`, subscriber)
	if e != nil {
		return e
	}
	var campaigns []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		campaigns = append(campaigns, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, campaign := range campaigns {
		if list > 0 {
			eligible, e := stillInAudience(ctx, c, campaign, subscriber)
			if e != nil {
				return e
			}
			if eligible {
				continue
			}
		}
		r, e := c.ExecContext(ctx, `UPDATE campaign_recipients SET status='skipped',error='unsubscribed' WHERE campaign_id=? AND subscriber_id=? AND status='pending'`, campaign, subscriber)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if _, e = c.ExecContext(ctx, `UPDATE campaign_stats SET skipped=skipped+? WHERE campaign_id=?`, n, campaign); e != nil {
			return e
		}
	}
	return nil
}
func stillInAudience(ctx context.Context, c *sql.Conn, campaign, subscriber int64) (bool, error) {
	rows, e := c.QueryContext(ctx, `SELECT t.target_type,t.target_id,t.mode,coalesce(s.rules,'') FROM campaign_targets t LEFT JOIN segments s ON t.target_type='segment' AND s.id=t.target_id WHERE t.campaign_id=?`, campaign)
	if e != nil {
		return false, e
	}
	type target struct {
		kind, mode, rules string
		id                int64
	}
	var targets []target
	for rows.Next() {
		var t target
		if e = rows.Scan(&t.kind, &t.id, &t.mode, &t.rules); e != nil {
			rows.Close()
			return false, e
		}
		targets = append(targets, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	var inc, exc []string
	var ia, ea []any
	for _, t := range targets {
		q := `EXISTS(SELECT 1 FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=s.id AND ls.status='subscribed')`
		a := []any{t.id}
		if t.kind == "segment" {
			var rule contacts.Rule
			if e = json.Unmarshal([]byte(t.rules), &rule); e != nil {
				return false, e
			}
			q, a, e = contacts.CompileSegment(rule)
			if e != nil {
				return false, e
			}
		}
		if t.mode == "include" {
			inc = append(inc, q)
			ia = append(ia, a...)
		} else {
			exc = append(exc, q)
			ea = append(ea, a...)
		}
	}
	if len(inc) == 0 {
		return false, nil
	}
	q := `SELECT EXISTS(SELECT 1 FROM subscribers s WHERE s.id=? AND s.status='active' AND (` + strings.Join(inc, " OR ") + `)`
	a := []any{subscriber}
	a = append(a, ia...)
	if len(exc) > 0 {
		q += ` AND NOT (` + strings.Join(exc, " OR ") + `)`
		a = append(a, ea...)
	}
	q += `)`
	var eligible bool
	e = c.QueryRowContext(ctx, q, a...).Scan(&eligible)
	return eligible, e
}
