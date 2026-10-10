package subscribers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"postbox/internal/render"
)

func (s *Service) CustomFields(ctx context.Context, brandID int64) ([]CustomField, error) {
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT id,brand_id,key,label,type,is_filterable FROM custom_fields WHERE brand_id=? ORDER BY id", brandID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CustomField{}
	for rows.Next() {
		var v CustomField
		if e = rows.Scan(&v.ID, &v.BrandID, &v.Key, &v.Label, &v.Type, &v.Filterable); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveCustomField(ctx context.Context, v CustomField) (CustomField, error) {
	if !fieldKey.MatchString(v.Key) {
		return v, errors.New("key must be an ASCII identifier of at most 64 characters")
	}
	switch v.Type {
	case "text", "number", "date", "boolean", "select":
	default:
		return v, errors.New("invalid custom field type")
	}
	e := s.db.Immediate(ctx, func(c *sql.Conn) error {
		if v.ID > 0 {
			var oldKey string
			if e := c.QueryRowContext(ctx, "SELECT key FROM custom_fields WHERE id=? AND brand_id=?", v.ID, v.BrandID).Scan(&oldKey); e != nil {
				return e
			}
			if oldKey != v.Key {
				return errors.New("custom field keys are immutable; create a new field to change its key")
			}
			if _, e := c.ExecContext(ctx, "UPDATE custom_fields SET label=?,type=?,is_filterable=? WHERE id=? AND brand_id=?", v.Label, v.Type, v.Filterable, v.ID, v.BrandID); e != nil {
				return e
			}
		} else {
			if e := c.QueryRowContext(ctx, "INSERT INTO custom_fields(brand_id,key,label,type,is_filterable) VALUES(?,?,?,?,?) ON CONFLICT(brand_id,key) DO UPDATE SET label=excluded.label,type=excluded.type,is_filterable=excluded.is_filterable RETURNING id", v.BrandID, v.Key, v.Label, v.Type, v.Filterable).Scan(&v.ID); e != nil {
				return e
			}
		}
		index := fmt.Sprintf("subscribers_custom_%d", v.ID)
		if _, e := c.ExecContext(ctx, "DROP INDEX IF EXISTS "+index); e != nil {
			return e
		}
		if v.Filterable {
			_, e := c.ExecContext(ctx, "CREATE INDEX "+index+" ON subscribers(brand_id, fields ->> '$."+v.Key+"')")
			return e
		}
		return nil
	})
	return v, e
}
func (s *Service) Timeline(ctx context.Context, brandID, id, afterID int64, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT a.id,a.kind,a.at,a.detail FROM subscriber_activity a WHERE a.subscriber_id=? AND a.id>? AND a.brand_id=? ORDER BY a.id LIMIT ?", id, afterID, brandID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if e = rows.Scan(&a.ID, &a.Kind, &a.At, &a.Detail); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Erase deletes raw event history in bounded transactions, then removes identity and memberships.
func (s *Service) Erase(ctx context.Context, brandID, id int64) error {
	var found int
	if e := s.db.Reader.QueryRowContext(ctx, "SELECT 1 FROM subscribers WHERE brand_id=? AND id=?", brandID, id).Scan(&found); e != nil {
		return e
	}
	if e := s.Unsubscribe(ctx, brandID, id, 0); e != nil {
		return e
	}
	for _, table := range []string{"opens", "clicks", "subscriber_activity"} {
		for {
			r, e := s.db.Writer.ExecContext(ctx, "DELETE FROM "+table+" WHERE id IN (SELECT id FROM "+table+" WHERE subscriber_id=? ORDER BY id LIMIT 1000)", id)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n < 1000 {
				break
			}
		}
	}
	var email string
	if e := s.db.Reader.QueryRowContext(ctx, "SELECT email FROM subscribers WHERE brand_id=? AND id=?", brandID, id).Scan(&email); e != nil {
		return e
	}
	for {
		r, e := s.db.Writer.ExecContext(ctx, "DELETE FROM ses_events WHERE id IN (SELECT id FROM ses_events WHERE ses_message_id IN (SELECT cr.message_id FROM campaign_recipients cr JOIN campaigns c ON c.id=cr.campaign_id WHERE cr.subscriber_id=? AND c.brand_id=? AND cr.message_id IS NOT NULL) ORDER BY id LIMIT 1000)", id, brandID)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n < 1000 {
			break
		}
	}
	if _, e := s.db.Writer.ExecContext(ctx, "DELETE FROM jobs WHERE queue='transactional_email' AND json_extract(payload,'$.brand_id')=? AND json_extract(payload,'$.to')=?", brandID, email); e != nil {
		return e
	}
	_, e := s.db.Writer.ExecContext(ctx, "DELETE FROM subscribers WHERE id=? AND brand_id=?", id, brandID)
	return e
}
func (s *Service) MassUnsubscribe(ctx context.Context, brandID, listID int64, emails []string) (int, error) {
	n := 0
	for start := 0; start < len(emails); start += 500 {
		end := min(start+500, len(emails))
		e := s.db.Immediate(ctx, func(c *sql.Conn) error {
			for _, email := range emails[start:end] {
				v, e := s.findOnConn(ctx, c, brandID, email)
				if e == sql.ErrNoRows {
					continue
				}
				if e != nil {
					return e
				}
				if e = unsubscribe(ctx, c, brandID, v.ID, listID); e != nil {
					return e
				}
				n++
			}
			return nil
		})
		if e != nil {
			return n, e
		}
	}
	return n, nil
}
func (s *Service) findOnConn(ctx context.Context, c *sql.Conn, brandID int64, email string) (Subscriber, error) {
	var e error
	email, e = render.NormalizeEmail(email)
	if e != nil {
		return Subscriber{}, e
	}
	return scanSubscriber(c.QueryRowContext(ctx, "SELECT "+subscriberColumns+" FROM subscribers WHERE brand_id=? AND email=?", brandID, email))
}
func (s *Service) MassErase(ctx context.Context, brandID int64, emails []string) (int, error) {
	n := 0
	for _, email := range emails {
		v, e := s.GetByEmail(ctx, brandID, email)
		if e == sql.ErrNoRows {
			continue
		}
		if e != nil {
			return n, e
		}
		if e = s.Erase(ctx, brandID, v.ID); e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}
func (s *Service) Housekeeping(ctx context.Context, brandID int64, olderThan int64) (int64, error) {
	var total int64
	for {
		var ids []int64
		rows, e := s.db.Reader.QueryContext(ctx, "SELECT ls.subscriber_id FROM list_subscribers ls JOIN lists l ON l.id=ls.list_id WHERE l.brand_id=? AND ls.status='unconfirmed' AND ls.created_at<? ORDER BY ls.subscriber_id LIMIT 500", brandID, olderThan)
		if e != nil {
			return total, e
		}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return total, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return total, e
		}
		if len(ids) == 0 {
			return total, nil
		}
		e = s.db.Immediate(ctx, func(c *sql.Conn) error {
			for _, id := range ids {
				r, e := c.ExecContext(ctx, "DELETE FROM list_subscribers WHERE subscriber_id=? AND status='unconfirmed' AND created_at<? AND list_id IN(SELECT id FROM lists WHERE brand_id=?)", id, olderThan, brandID)
				if e != nil {
					return e
				}
				n, _ := r.RowsAffected()
				total += n
				if _, e = c.ExecContext(ctx, "DELETE FROM subscription_confirmations WHERE subscriber_id=? AND list_id NOT IN (SELECT list_id FROM list_subscribers WHERE subscriber_id=?)", id, id); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return total, e
		}
	}
}
func (s *Service) Segments(ctx context.Context, brandID int64) ([]Segment, error) {
	rows, e := s.db.Reader.QueryContext(ctx, "SELECT id,brand_id,name,rules FROM segments WHERE brand_id=? ORDER BY id", brandID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var v Segment
		var raw string
		if e = rows.Scan(&v.ID, &v.BrandID, &v.Name, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &v.Rules); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func membershipCounter(ctx context.Context, c *sql.Conn, listID, delta, subscribed, unsubscribed, at int64) error {
	if _, e := c.ExecContext(ctx, "INSERT INTO list_counters(list_id,active,subscribed,unsubscribed) VALUES(?,max(0,?),?,?) ON CONFLICT(list_id) DO UPDATE SET active=max(0,active+?),subscribed=subscribed+excluded.subscribed,unsubscribed=unsubscribed+excluded.unsubscribed", listID, delta, subscribed, unsubscribed, delta); e != nil {
		return e
	}
	_, e := c.ExecContext(ctx, "INSERT INTO list_daily_stats(list_id,day,active,subscribed,unsubscribed) SELECT ?,?,active,?,? FROM list_counters WHERE list_id=? ON CONFLICT(list_id,day) DO UPDATE SET active=excluded.active,subscribed=subscribed+excluded.subscribed,unsubscribed=unsubscribed+excluded.unsubscribed", listID, at/86400000*86400000, subscribed, unsubscribed, listID)
	return e
}

func skipPendingTx(ctx context.Context, c *sql.Conn, predicate string, args ...any) error {
	rows, e := c.QueryContext(ctx, "UPDATE campaign_recipients SET status='skipped' WHERE status='pending' AND "+predicate+" RETURNING campaign_id", args...)
	if e != nil {
		return e
	}
	counts := map[int64]int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		counts[id]++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for id, n := range counts {
		if _, e = c.ExecContext(ctx, "UPDATE campaign_stats SET skipped=skipped+? WHERE campaign_id=?", n, id); e != nil {
			return e
		}
	}
	return nil
}
