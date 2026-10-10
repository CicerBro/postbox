package tracking

import (
	"context"
	"database/sql"
	"time"
)

func (s *Service) writer(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	batch := make([]record, 0, s.cfg.BatchSize)
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		for {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e := s.writeBatch(c, batch)
			cancel()
			if e == nil {
				batch = batch[:0]
				s.err = nil
				return true
			}
			s.err = e
			select {
			case <-ctx.Done():
				return false
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	for {
		select {
		case ev := <-s.queue:
			batch = append(batch, ev)
			if len(batch) >= s.cfg.BatchSize {
				if !flush() {
					return
				}
			}
		case <-ticker.C:
			if !flush() {
				return
			}
		case <-ctx.Done():
			for {
				select {
				case ev := <-s.queue:
					batch = append(batch, ev)
				default:
					flush()
					return
				}
			}
		}
	}
}
func (s *Service) writeBatch(ctx context.Context, records []record) error {
	return s.db.Immediate(ctx, func(c *sql.Conn) error {
		for _, ev := range records {
			var first, firstHuman sql.NullInt64
			kind := "opens"
			stamp := "first_opened_at"
			humanStamp := "first_human_opened_at"
			if ev.link > 0 {
				kind = "clicks"
				stamp = "first_clicked_at"
				humanStamp = "first_human_clicked_at"
			}
			e := c.QueryRowContext(ctx, `SELECT `+stamp+`,`+humanStamp+` FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=?`, ev.campaign, ev.subscriber).Scan(&first, &firstHuman)
			if e == sql.ErrNoRows {
				continue
			}
			if e != nil {
				return e
			}
			unique := 0
			if !first.Valid {
				unique = 1
			}
			human := 0
			humanUnique := 0
			if !ev.machine {
				human = 1
				if !firstHuman.Valid {
					humanUnique = 1
				}
			}
			if _, e = c.ExecContext(ctx, `UPDATE campaign_recipients SET `+stamp+`=coalesce(`+stamp+`,?),`+humanStamp+`=CASE WHEN ? THEN `+humanStamp+` ELSE coalesce(`+humanStamp+`,?) END WHERE campaign_id=? AND subscriber_id=?`, ev.at, ev.machine, ev.at, ev.campaign, ev.subscriber); e != nil {
				return e
			}
			if _, e = c.ExecContext(ctx, `INSERT OR IGNORE INTO campaign_stats(campaign_id) VALUES(?)`, ev.campaign); e != nil {
				return e
			}
			if _, e = c.ExecContext(ctx, `UPDATE campaign_stats SET `+kind+`_total=`+kind+`_total+1,`+kind+`_unique=`+kind+`_unique+?,`+kind+`_human_total=`+kind+`_human_total+?,`+kind+`_human_unique=`+kind+`_human_unique+? WHERE campaign_id=?`, unique, human, humanUnique, ev.campaign); e != nil {
				return e
			}
			if ev.raw {
				if ev.link == 0 {
					_, e = c.ExecContext(ctx, `INSERT INTO opens(campaign_id,subscriber_id,at,country,is_machine) VALUES(?,?,?,?,?)`, ev.campaign, ev.subscriber, ev.at, ev.country, ev.machine)
				} else {
					_, e = c.ExecContext(ctx, `INSERT INTO clicks(campaign_id,subscriber_id,link_id,at,country,is_machine) VALUES(?,?,?,?,?,?)`, ev.campaign, ev.subscriber, ev.link, ev.at, ev.country, ev.machine)
				}
				if e != nil {
					return e
				}
			}
			if ev.raw {
				activity := "open"
				if ev.link > 0 {
					activity = "click"
				}
				if _, e = c.ExecContext(ctx, `INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) SELECT brand_id,?,?,?,json_object('campaign_id',?,'link_id',?,'is_machine',?) FROM campaigns WHERE id=?`, ev.subscriber, activity, ev.at, ev.campaign, ev.link, ev.machine, ev.campaign); e != nil {
					return e
				}
			}

			if ev.link > 0 {
				var hf sql.NullInt64
				e = c.QueryRowContext(ctx, `SELECT first_human_clicked_at FROM recipient_link_stats WHERE campaign_id=? AND subscriber_id=? AND link_id=?`, ev.campaign, ev.subscriber, ev.link).Scan(&hf)
				linkUnique := 0
				if e == sql.ErrNoRows {
					linkUnique = 1
				} else if e != nil {
					return e
				}
				linkHumanUnique := 0
				if !ev.machine && !hf.Valid {
					linkHumanUnique = 1
				}
				var ht any
				if !ev.machine {
					ht = ev.at
				}
				if _, e = c.ExecContext(ctx, `INSERT INTO recipient_link_stats(campaign_id,subscriber_id,link_id,first_clicked_at,first_human_clicked_at) VALUES(?,?,?,?,?) ON CONFLICT(campaign_id,subscriber_id,link_id) DO UPDATE SET first_human_clicked_at=coalesce(first_human_clicked_at,excluded.first_human_clicked_at)`, ev.campaign, ev.subscriber, ev.link, ev.at, ht); e != nil {
					return e
				}
				if _, e = c.ExecContext(ctx, `INSERT INTO link_stats(link_id,campaign_id,clicks_total,clicks_unique,clicks_human_total,clicks_human_unique) VALUES(?,?,1,?,?,?) ON CONFLICT(link_id) DO UPDATE SET clicks_total=clicks_total+1,clicks_unique=clicks_unique+excluded.clicks_unique,clicks_human_total=clicks_human_total+excluded.clicks_human_total,clicks_human_unique=clicks_human_unique+excluded.clicks_human_unique`, ev.link, ev.campaign, linkUnique, human, linkHumanUnique); e != nil {
					return e
				}
			}
			op, cl := 0, 0
			if ev.link == 0 {
				op = 1
			} else {
				cl = 1
			}
			if _, e = c.ExecContext(ctx, `INSERT INTO engagement_hourly(campaign_id,hour,opens,clicks) VALUES(?,?,?,?) ON CONFLICT(campaign_id,hour) DO UPDATE SET opens=opens+excluded.opens,clicks=clicks+excluded.clicks`, ev.campaign, ev.at/3600000*3600000, op, cl); e != nil {
				return e
			}
			if ev.country != "" {
				if _, e = c.ExecContext(ctx, `INSERT INTO campaign_countries(campaign_id,country,opens,clicks) VALUES(?,?,?,?) ON CONFLICT(campaign_id,country) DO UPDATE SET opens=opens+excluded.opens,clicks=clicks+excluded.clicks`, ev.campaign, ev.country, op, cl); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
