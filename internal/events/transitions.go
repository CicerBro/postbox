package events

import (
	"context"
	"database/sql"
	"errors"
	"postbox/internal/jobs"
	"strconv"
	"strings"
)

type recipientMatch struct{ campaign, subscriber, brand int64 }

func resolveEventBrandTx(ctx context.Context, c *sql.Conn, brand int64, ev sesEvent) (int64, error) {
	if brand > 0 {
		return brand, nil
	}
	if tag := firstTag(ev.Mail.Tags, "brand_id", "brand"); tag != "" {
		id, err := strconv.ParseInt(tag, 10, 64)
		if err != nil || id <= 0 {
			return 0, errors.New("invalid SES event brand tag")
		}
		return id, nil
	}
	var id sql.NullInt64
	var count int
	if err := c.QueryRowContext(ctx, `SELECT min(ca.brand_id),count(DISTINCT ca.brand_id) FROM campaign_recipients cr JOIN campaigns ca ON ca.id=cr.campaign_id WHERE cr.message_id=?`, ev.Mail.MessageID).Scan(&id, &count); err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, errors.New("SES event has no unambiguous brand")
	}
	return id.Int64, nil
}

// The explicit index keeps SQLite from scanning the campaign's primary-key
// prefix for every receipt while holding the single writer transaction.
const snapshotMatchSQL = `SELECT cr.campaign_id,cr.subscriber_id,ca.brand_id FROM campaign_recipients cr INDEXED BY campaign_recipients_snapshot_email JOIN campaigns ca ON ca.id=cr.campaign_id JOIN subscribers s ON s.id=cr.subscriber_id WHERE cr.campaign_id=? AND cr.snapshot_email=? AND ca.brand_id=? AND s.brand_id=ca.brand_id AND (cr.message_id IS NULL OR cr.message_id=?)`

func matchRecipientTx(ctx context.Context, c *sql.Conn, brand int64, ev sesEvent, email string) (recipientMatch, error) {
	var m recipientMatch
	err := c.QueryRowContext(ctx, `SELECT cr.campaign_id,cr.subscriber_id,ca.brand_id FROM campaign_recipients cr JOIN campaigns ca ON ca.id=cr.campaign_id JOIN subscribers s ON s.id=cr.subscriber_id WHERE cr.message_id=? AND coalesce(cr.snapshot_email,s.email)=? AND ca.brand_id=? AND s.brand_id=ca.brand_id`, ev.Mail.MessageID, email, brand).Scan(&m.campaign, &m.subscriber, &m.brand)
	if errors.Is(err, sql.ErrNoRows) {
		campaignTag := firstTag(ev.Mail.Tags, "campaign_id", "campaign")
		brandTag := firstTag(ev.Mail.Tags, "brand_id", "brand")
		err = c.QueryRowContext(ctx, snapshotMatchSQL, campaignTag, email, brandTag, ev.Mail.MessageID).Scan(&m.campaign, &m.subscriber, &m.brand)
		if errors.Is(err, sql.ErrNoRows) {
			err = c.QueryRowContext(ctx, `SELECT cr.campaign_id,cr.subscriber_id,ca.brand_id FROM subscribers s JOIN campaign_recipients cr ON cr.subscriber_id=s.id JOIN campaigns ca ON ca.id=cr.campaign_id WHERE s.brand_id=? AND ca.brand_id=s.brand_id AND s.email=? AND cr.campaign_id=? AND cr.snapshot_email IS NULL AND (cr.message_id IS NULL OR cr.message_id=?)`, brandTag, email, campaignTag, ev.Mail.MessageID).Scan(&m.campaign, &m.subscriber, &m.brand)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return recipientMatch{}, nil
	}
	return m, err
}

// An SES receipt proves acceptance even if a worker lost the SendEmail response.
// Updating the recipient first also fences a later result writer, which only
// persists recipients still in the claimed state.
func reconcileAcceptanceTx(ctx context.Context, c *sql.Conn, m recipientMatch, messageID string, at int64) error {
	var state string
	if err := c.QueryRowContext(ctx, `SELECT status FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=?`, m.campaign, m.subscriber).Scan(&state); err != nil {
		return err
	}
	var attempt int
	acceptedAt := at
	err := c.QueryRowContext(ctx, `SELECT attempt,started_at FROM send_attempts WHERE campaign_id=? AND subscriber_id=? AND (message_id=? OR (message_id IS NULL AND outcome IN ('started','unknown'))) ORDER BY attempt DESC LIMIT 1`, m.campaign, m.subscriber, messageID).Scan(&attempt, &acceptedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = c.ExecContext(ctx, `UPDATE campaign_recipients SET status='sent',message_id=?,error=NULL,sent_at=coalesce(sent_at,?) WHERE campaign_id=? AND subscriber_id=?`, messageID, acceptedAt, m.campaign, m.subscriber); err != nil {
		return err
	}
	if attempt > 0 {
		if _, err = c.ExecContext(ctx, `UPDATE send_attempts SET outcome='sent',finished_at=coalesce(finished_at,?),message_id=?,error=NULL WHERE campaign_id=? AND subscriber_id=? AND attempt=?`, at, messageID, m.campaign, m.subscriber, attempt); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE campaign_dispatch_ledger INDEXED BY campaign_dispatch_owner SET outcome='sent',updated_at=? WHERE campaign_id=? AND owner_subscriber_id=? AND owner_attempt=? AND outcome IN ('started','unknown') AND generation=(SELECT dispatch_generation FROM send_attempts WHERE campaign_id=? AND subscriber_id=? AND attempt=?)`, at, m.campaign, m.subscriber, attempt, m.campaign, m.subscriber, attempt); err != nil {
			return err
		}
	}
	if state == "sent" {
		return nil
	}
	if _, err = c.ExecContext(ctx, `UPDATE campaign_stats SET sent=sent+1,unknown=max(0,unknown-?),failed=max(0,failed-?),skipped=max(0,skipped-?) WHERE campaign_id=?`, boolInt(state == "unknown"), boolInt(state == "failed"), boolInt(state == "skipped"), m.campaign); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, `INSERT INTO send_hourly(brand_id,hour,sent) VALUES(?,?,1) ON CONFLICT(brand_id,hour) DO UPDATE SET sent=sent+1`, m.brand, acceptedAt/3600000*3600000)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func recordRecipientEventTx(ctx context.Context, c *sql.Conn, m recipientMatch, ev sesEvent, typ string, at int64) error {
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO campaign_stats(campaign_id) VALUES(?)`, m.campaign); err != nil {
		return err
	}
	if typ == "Delivery" || typ == "Bounce" || typ == "Complaint" {
		if err := reconcileAcceptanceTx(ctx, c, m, ev.Mail.MessageID, at); err != nil {
			return err
		}
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,?,?,json_object('campaign_id',?,'ses_message_id',?,'subtype',?))`, m.brand, m.subscriber, strings.ToLower(typ), at, m.campaign, ev.Mail.MessageID, ev.Bounce.BounceType); err != nil {
		return err
	}
	var res sql.Result
	var err error
	column := ""
	switch typ {
	case "Delivery":
		column = "delivered"
		res, err = c.ExecContext(ctx, `UPDATE campaign_recipients SET delivered_at=? WHERE campaign_id=? AND subscriber_id=? AND delivered_at IS NULL`, at, m.campaign, m.subscriber)
	case "Complaint":
		column = "complaints"
		res, err = c.ExecContext(ctx, `UPDATE campaign_recipients SET complained_at=? WHERE campaign_id=? AND subscriber_id=? AND complained_at IS NULL`, at, m.campaign, m.subscriber)
	case "Bounce":
		column = "bounces_soft"
		if ev.Bounce.BounceType == "Permanent" {
			column = "bounces_hard"
		}
		res, err = c.ExecContext(ctx, `UPDATE campaign_recipients SET bounced_at=coalesce(bounced_at,?) WHERE campaign_id=? AND subscriber_id=?`, at, m.campaign, m.subscriber)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if _, err = c.ExecContext(ctx, `UPDATE campaign_stats SET `+column+`=`+column+`+1 WHERE campaign_id=?`, m.campaign); err != nil {
		return err
	}
	if typ == "Delivery" {
		_, err = c.ExecContext(ctx, `INSERT INTO engagement_hourly(campaign_id,hour,deliveries) VALUES(?,?,1) ON CONFLICT(campaign_id,hour) DO UPDATE SET deliveries=deliveries+1`, m.campaign, at/3600000*3600000)
	}
	return err
}

func (s *Service) applySuppressionTx(ctx context.Context, c *sql.Conn, brand int64, m recipientMatch, email string, ev sesEvent, typ string, at int64) error {
	reason, status := "", "bounced"
	switch {
	case typ == "Complaint":
		reason, status = "complaint", "complained"
	case typ == "Bounce" && ev.Bounce.BounceType == "Permanent":
		reason = "hard_bounce"
	case typ == "Bounce" && ev.Bounce.BounceType == "Transient":
		if _, err := c.ExecContext(ctx, `UPDATE subscribers SET soft_bounce_count=soft_bounce_count+1,updated_at=? WHERE (email=? OR id=?) AND brand_id=?`, at, email, m.subscriber, brand); err != nil {
			return err
		}
		var count int
		if err := c.QueryRowContext(ctx, `SELECT coalesce(max(soft_bounce_count),0) FROM subscribers WHERE (email=? OR id=?) AND brand_id=?`, email, m.subscriber, brand).Scan(&count); err != nil {
			return err
		}
		if count >= s.cfg.SoftBounceLimit {
			reason = "soft_bounce_limit"
		}
	}
	if reason == "" {
		if typ == "Bounce" && m.subscriber != 0 {
			return jobs.EmitTx(ctx, c, m.brand, "bounce", map[string]any{"subscriber_id": m.subscriber, "email": email, "campaign_id": m.campaign, "reason": "transient"})
		}
		return nil
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(?,'email',?,?,?)`, brand, email, reason, at); err != nil {
		return err
	}
	rows, err := c.QueryContext(ctx, `UPDATE subscribers SET status=CASE WHEN status='complained' THEN status ELSE ? END,updated_at=? WHERE (email=? OR id=?) AND brand_id=? RETURNING id,brand_id`, status, at, email, m.subscriber, brand)
	if err != nil {
		return err
	}
	type identity struct{ id, brand int64 }
	var ids []identity
	for rows.Next() {
		var i identity
		if err = rows.Scan(&i.id, &i.brand); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	event := "bounce"
	if typ == "Complaint" {
		event = "complaint"
	}
	for _, i := range ids {
		if err = skipSubscriber(ctx, c, i.id); err != nil {
			return err
		}
		if err = UnsubscribeMembershipsTx(ctx, c, i.id, 0, at, event); err != nil {
			return err
		}
		if err = jobs.EmitTx(ctx, c, i.brand, event, map[string]any{"subscriber_id": i.id, "email": email, "campaign_id": m.campaign, "reason": reason}); err != nil {
			return err
		}
	}
	return nil
}
