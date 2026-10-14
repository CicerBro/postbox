package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"postbox/internal/csvexport"
	"postbox/internal/send"
)

func (a *API) report(w http.ResponseWriter, r *http.Request) {
	brand, id, ok := a.campaignScope(w, r)
	if !ok {
		return
	}
	if has(r.URL.Path, "recipients") {
		after, limit := page(r)
		status := r.URL.Query().Get("status")
		q := "SELECT subscriber_id AS id,snapshot_email AS email,status,error,message_id,sent_at FROM campaign_recipients WHERE campaign_id=? AND subscriber_id>?"
		args := []any{id, after}
		if status != "" {
			q += " AND status=?"
			args = append(args, status)
		}
		q += " ORDER BY subscriber_id LIMIT ?"
		args = append(args, limit)
		v, e := queryRows(r.Context(), a.db.Reader, q, args...)
		result(w, v, e)
		return
	}
	if has(r.URL.Path, "checklist") {
		v, e := a.engine.Checklist(r.Context(), id, send.PreflightOptions{})
		result(w, v, e)
		return
	}
	if has(r.URL.Path, "preview") {
		subject, html, text, e := a.engine.Preview(r.Context(), id, number(r.URL.Query().Get("subscriber_id")))
		result(w, map[string]string{"subject": subject, "html": html, "text": text}, e)
		return
	}
	if has(r.URL.Path, "export") {
		a.exportRecipients(w, r, id)
		return
	}
	if has(r.URL.Path, "events") {
		a.streamCampaignStats(w, r, brand, id)
		return
	}

	v, err := a.campaignReportData(r.Context(), brand, id)
	result(w, v, err)
}

func (a *API) campaignReportData(ctx context.Context, brand, id int64) (map[string]any, error) {
	canReactivate, e := a.engine.CanReactivate(ctx, id)
	if e != nil {
		return nil, e
	}
	stats, e := queryRows(ctx, a.db.Reader, "SELECT * FROM campaign_stats WHERE campaign_id=?", id)
	if e != nil {
		return nil, e
	}
	links, e := queryRows(ctx, a.db.Reader, "SELECT l.id,l.url,COALESCE(s.clicks_total,0) clicks_total,COALESCE(s.clicks_unique,0) clicks_unique,COALESCE(s.clicks_human_total,0) clicks_human_total,COALESCE(s.clicks_human_unique,0) clicks_human_unique FROM links l LEFT JOIN link_stats s ON s.link_id=l.id WHERE l.campaign_id=? ORDER BY l.id", id)
	if e != nil {
		return nil, e
	}
	countries, e := queryRows(ctx, a.db.Reader, "SELECT country,opens,clicks FROM campaign_countries WHERE campaign_id=? ORDER BY country", id)
	if e != nil {
		return nil, e
	}
	hourly, e := queryRows(ctx, a.db.Reader, "SELECT hour,opens,clicks,deliveries FROM engagement_hourly WHERE campaign_id=? ORDER BY hour", id)
	if e != nil {
		return nil, e
	}
	recent := map[string]any{}
	for _, event := range campaignRecentEvents {
		rows, err := queryRows(ctx, a.db.Reader, campaignRecentSQL(event.column), id, brand)
		if err != nil {
			return nil, err
		}
		recent[event.name] = rows
	}
	var firstSent, lastSent sql.NullInt64
	var fromName, fromEmail string
	var snapshotted, historical, deliveryEstimated bool
	e = a.db.Reader.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT sent_at FROM campaign_recipients WHERE campaign_id=c.id AND sent_at IS NOT NULL ORDER BY sent_at,subscriber_id LIMIT 1), CASE WHEN m.target_id IS NOT NULL THEN c.started_at END),
		(SELECT sent_at FROM campaign_recipients WHERE campaign_id=c.id AND sent_at IS NOT NULL ORDER BY sent_at DESC,subscriber_id DESC LIMIT 1),
		COALESCE(p.from_name,b.from_name),COALESCE(p.from_email,b.from_email),p.campaign_id IS NOT NULL,m.target_id IS NOT NULL,
        m.target_id IS NOT NULL AND json_extract(r.payload,'$.delivered') IS NULL
		FROM campaigns c JOIN brands b ON b.id=c.brand_id LEFT JOIN campaign_preparations p ON p.campaign_id=c.id LEFT JOIN migration_identities m ON m.entity='campaigns' AND m.target_id=c.id
        LEFT JOIN migration_records r ON r.source=m.source AND r.entity=m.entity AND r.source_id=m.source_id
        WHERE c.id=?`, id).Scan(&firstSent, &lastSent, &fromName, &fromEmail, &snapshotted, &historical, &deliveryEstimated)
	if e != nil {
		return nil, e
	}
	var firstAt, lastAt any
	if firstSent.Valid {
		firstAt = firstSent.Int64
	}
	if lastSent.Valid {
		lastAt = lastSent.Int64
	}
	targets, e := queryRows(ctx, a.db.Reader, `SELECT t.target_type,t.target_id,t.mode,
		CASE t.target_type WHEN 'list' THEN l.name ELSE s.name END AS name
		FROM campaign_targets t
		LEFT JOIN lists l ON t.target_type='list' AND l.id=t.target_id AND l.brand_id=?
		LEFT JOIN segments s ON t.target_type='segment' AND s.id=t.target_id AND s.brand_id=?
		WHERE t.campaign_id=? ORDER BY t.mode,t.target_type,t.target_id`, brand, brand, id)
	return map[string]any{"can_reactivate": canReactivate, "stats": stats, "links": links, "countries": countries, "hourly": hourly, "recent": recent, "targets": targets,
		"delivery": map[string]any{"first_sent_at": firstAt, "last_sent_at": lastAt, "from_name": fromName, "from_email": fromEmail, "sender_snapshotted": snapshotted, "historical": historical, "delivered_estimated": deliveryEstimated}}, e
}

func (a *API) exportRecipients(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=recipients.csv")
	cw := csvexport.NewWriter(w)
	_ = cw.Write([]string{"subscriber_id", "email", "status", "message_id", "error", "sent_at"})
	after := int64(0)
	for {
		rows, e := queryRows(r.Context(), a.db.Reader, "SELECT subscriber_id,snapshot_email,status,COALESCE(message_id,'') message_id,COALESCE(error,'') error,COALESCE(sent_at,0) sent_at FROM campaign_recipients WHERE campaign_id=? AND subscriber_id>? ORDER BY subscriber_id LIMIT 500", id, after)
		if e != nil {
			return
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row["subscriber_id"].(int64)
			_ = cw.Write([]string{fmt.Sprint(after), fmt.Sprint(row["snapshot_email"]), fmt.Sprint(row["status"]), fmt.Sprint(row["message_id"]), fmt.Sprint(row["error"]), fmt.Sprint(row["sent_at"])})
		}
		cw.Flush()
		if cw.Error() != nil {
			return
		}
	}
	cw.Flush()
}

var campaignRecentEvents = []struct{ name, column string }{
	{"opened", "first_opened_at"},
	{"unsubscribed", "unsubscribed_at"},
	{"bounced", "bounced_at"},
	{"complained", "complained_at"},
}

// Columns are chosen only from campaignRecentEvents, never request input.
// Recipient timestamps survive raw event retention and count each recipient once.
func campaignRecentSQL(column string) string {
	return `SELECT r.subscriber_id AS id,COALESCE(r.snapshot_email,s.email) AS email,
		COALESCE(r.snapshot_name,s.name) AS name,r.` + column + ` AS at
		FROM campaign_recipients r JOIN subscribers s ON s.id=r.subscriber_id
		WHERE r.campaign_id=? AND s.brand_id=? AND r.` + column + ` IS NOT NULL
		ORDER BY r.` + column + ` DESC,r.subscriber_id DESC LIMIT 10`
}
