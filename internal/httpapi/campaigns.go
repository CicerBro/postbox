package httpapi

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"postbox/internal/send"
	"strconv"
	"strings"
	"time"
)

type campaignInput struct {
	Subject     string `json:"subject"`
	HTML        string `json:"html"`
	Text        string `json:"text"`
	InlineCSS   bool   `json:"inline_css"`
	ScheduledAt *int64 `json:"scheduled_at"`
}

func (a *API) registerCampaigns(m *http.ServeMux) {
	a.route(m, "POST /api/v1/email-preview", a.editorPreview)
	a.route(m, "POST /api/v1/email-preview/recipients", a.editorRecipients)
	for _, p := range []string{"GET /api/v1/campaigns", "POST /api/v1/campaigns", "GET /api/v1/campaigns/{id}", "PATCH /api/v1/campaigns/{id}", "DELETE /api/v1/campaigns/{id}"} {
		a.route(m, p, a.campaigns)
	}
	for _, action := range []string{"start", "pause", "resume", "cancel", "schedule", "duplicate", "resend-unknown", "test-send"} {
		a.route(m, "POST /api/v1/campaigns/{id}/"+action, a.campaignAction)
	}
	for _, p := range []string{"GET /api/v1/campaigns/{id}/targets", "PUT /api/v1/campaigns/{id}/targets"} {
		a.route(m, p, a.targets)
	}
	for _, p := range []string{"GET /api/v1/campaigns/{id}/stats", "GET /api/v1/campaigns/{id}/report", "GET /api/v1/campaigns/{id}/events", "GET /api/v1/campaigns/{id}/export", "GET /api/v1/campaigns/{id}/checklist", "GET /api/v1/campaigns/{id}/preview", "GET /api/v1/campaigns/{id}/recipients"} {
		a.route(m, p, a.report)
	}
	a.route(m, "GET /api/v1/sending", a.sending)
	a.route(m, "POST /api/v1/sending", a.sending)
}
func (a *API) campaignScope(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return 0, 0, false
	}
	id := number(r.PathValue("id"))
	var n int
	err := a.db.Reader.QueryRowContext(r.Context(), "SELECT 1 FROM campaigns WHERE id=? AND brand_id=?", id, brand).Scan(&n)
	if err != nil {
		result(w, nil, err)
		return 0, 0, false
	}
	return brand, id, true
}
func (a *API) campaigns(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	id := number(r.PathValue("id"))
	if id != 0 {
		_, _, ok = a.campaignScope(w, r)
		if !ok {
			return
		}
	}
	if r.Method == "GET" {
		after, limit := page(r)
		q := "SELECT * FROM campaigns WHERE brand_id=? AND id>? ORDER BY id LIMIT ?"
		args := []any{brand, after, limit}
		// Offset pagination supports globally sorted and filtered table views.
		// Keep the existing ID cursor contract for integrations using after.
		if id == 0 && r.URL.Query().Has("offset") {
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if offset < 0 {
				offset = 0
			}
			order := "CASE WHEN status='draft' AND scheduled_at IS NULL THEN 0 ELSE 1 END, created_at DESC, id DESC"
			columns := map[string]string{"subject": "subject COLLATE NOCASE", "status": "status", "created_at": "created_at", "scheduled_at": "scheduled_at"}
			if column, ok := columns[r.URL.Query().Get("sort")]; ok {
				direction := "ASC"
				if r.URL.Query().Get("direction") == "desc" {
					direction = "DESC"
				}
				order = column + " " + direction + ", id " + direction
			}
			q = "SELECT * FROM campaigns WHERE brand_id=?"
			args = []any{brand}
			if status := r.URL.Query().Get("status"); status != "" && status != "all" {
				q += " AND status=?"
				args = append(args, status)
			}
			if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
				q += " AND instr(lower(subject), lower(?)) > 0"
				args = append(args, search)
			}
			q += " ORDER BY " + order + " LIMIT ? OFFSET ?"
			args = append(args, limit, offset)
		}
		if id != 0 {
			q = "SELECT * FROM campaigns WHERE brand_id=? AND id=?"
			args = []any{brand, id}
		}
		v, e := queryRows(r.Context(), a.db.Reader, q, args...)
		result(w, v, e)
		return
	}
	if r.Method == "DELETE" {
		res, e := a.db.Writer.ExecContext(r.Context(), "DELETE FROM campaigns WHERE brand_id=? AND id=? AND status='draft'", brand, id)
		result(w, map[string]bool{"deleted": true}, requireChanged(res, e))
		return
	}
	var in campaignInput
	if !decode(w, r, &in) {
		return
	}
	if in.Subject == "" || in.HTML == "" {
		fail(w, 400, errors.New("subject and html required"))
		return
	}
	now := time.Now().UnixMilli()
	if id == 0 {
		res, e := a.db.Writer.ExecContext(r.Context(), "INSERT INTO campaigns(brand_id,uuid,status,subject,html,text,inline_css,created_at,updated_at,created_by) VALUES(?,?,'draft',?,?,?,?,?,?,NULLIF(?,0))", brand, uuid.NewString(), in.Subject, in.HTML, in.Text, in.InlineCSS, now, now, r.Context().Value(identityKey{}).(*identity).UserID)
		if e == nil {
			id, e = res.LastInsertId()
		}
		result(w, map[string]int64{"id": id}, e)
		return
	}
	err := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		res, e := c.ExecContext(r.Context(), "UPDATE campaigns SET subject=?,html=?,text=?,inline_css=?,updated_at=? WHERE id=? AND brand_id=? AND status='draft'", in.Subject, in.HTML, in.Text, in.InlineCSS, now, id, brand)
		if e = requireChanged(res, e); e != nil {
			return e
		}
		return invalidate(r.Context(), c, id)
	})
	result(w, map[string]int64{"id": id}, err)
}
func invalidate(ctx context.Context, c *sql.Conn, id int64) error {
	for _, table := range []string{"campaign_preparations", "campaign_recipients", "campaign_stats", "links"} {
		if _, err := c.ExecContext(ctx, "DELETE FROM "+table+" WHERE campaign_id=?", id); err != nil {
			return err
		}
	}
	return nil
}
func (a *API) targets(w http.ResponseWriter, r *http.Request) {
	brand, id, ok := a.campaignScope(w, r)
	if !ok {
		return
	}
	if r.Method == "GET" {
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT target_type,target_id,mode FROM campaign_targets WHERE campaign_id=?", id)
		result(w, v, e)
		return
	}
	var in []struct {
		Type string `json:"target_type"`
		ID   int64  `json:"target_id"`
		Mode string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	e := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		var status string
		if e := c.QueryRowContext(r.Context(), "SELECT status FROM campaigns WHERE id=?", id).Scan(&status); e != nil {
			return e
		}
		if status != "draft" {
			return errors.New("only draft campaign targets can be changed")
		}
		if _, e := c.ExecContext(r.Context(), "DELETE FROM campaign_targets WHERE campaign_id=?", id); e != nil {
			return e
		}
		for _, t := range in {
			table := "lists"
			if t.Type == "segment" {
				table = "segments"
			} else if t.Type != "list" {
				return errors.New("target_type must be list or segment")
			}
			if t.Mode != "include" && t.Mode != "exclude" {
				return errors.New("mode must be include or exclude")
			}
			var n int
			if e := c.QueryRowContext(r.Context(), "SELECT 1 FROM "+table+" WHERE id=? AND brand_id=?", t.ID, brand).Scan(&n); e != nil {
				return errors.New("target belongs to another brand or does not exist")
			}
			if _, e := c.ExecContext(r.Context(), "INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(?,?,?,?)", id, t.Type, t.ID, t.Mode); e != nil {
				return e
			}
		}
		return invalidate(r.Context(), c, id)
	})
	result(w, in, e)
}
func (a *API) campaignAction(w http.ResponseWriter, r *http.Request) {
	brand, id, ok := a.campaignScope(w, r)
	if !ok {
		return
	}
	action := r.URL.Path
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID != 0 && !i.Admin && (has(action, "start") || has(action, "schedule") || has(action, "resume") || has(action, "test-send") || has(action, "resend-unknown")) {
		var owner sql.NullInt64
		if err := a.db.Reader.QueryRowContext(r.Context(), "SELECT created_by FROM campaigns WHERE id=?", id).Scan(&owner); err != nil {
			result(w, nil, err)
			return
		}
		if !owner.Valid || owner.Int64 != i.UserID {
			fail(w, 403, errors.New("duplicate this campaign before sending from your account"))
			return
		}
		if has(action, "resend-unknown") {
			fail(w, 403, errors.New("uncertain recipients cannot be resent because SES may already have accepted them"))
			return
		}
	}
	var err error
	switch {
	case has(action, "pause"):
		err = a.engine.Pause(r.Context(), id)
	case has(action, "resume"):
		err = a.engine.Resume(r.Context(), id)
	case has(action, "cancel"):
		err = a.engine.Cancel(r.Context(), id)
	case has(action, "test-send"):
		var in struct {
			Emails []string `json:"emails"`
		}
		if !decode(w, r, &in) {
			return
		}
		err = a.engine.TestSend(r.Context(), id, in.Emails)
	case has(action, "schedule"), has(action, "start"):
		at := time.Now().UnixMilli()
		if has(action, "schedule") {
			var in struct {
				ScheduledAt int64  `json:"scheduled_at"`
				Timezone    string `json:"timezone"`
			}
			if !decode(w, r, &in) {
				return
			}
			if in.Timezone != "" {
				if _, e := time.LoadLocation(in.Timezone); e != nil {
					result(w, nil, e)
					return
				}
			}
			at = in.ScheduledAt
			if at < time.Now().UnixMilli() {
				fail(w, 400, errors.New("scheduled_at must be in the future"))
				return
			}
		}
		res, e := a.db.Writer.ExecContext(r.Context(), "UPDATE campaigns SET status='scheduled',scheduled_at=?,updated_at=? WHERE brand_id=? AND id=? AND status='draft'", at, time.Now().UnixMilli(), brand, id)
		err = requireChanged(res, e)
	case has(action, "duplicate"):
		var newID int64
		err = a.db.Immediate(r.Context(), func(c *sql.Conn) error {
			res, e := c.ExecContext(r.Context(), "INSERT INTO campaigns(brand_id,uuid,status,subject,html,text,inline_css,created_at,updated_at,created_by) SELECT brand_id,?,'draft',subject,html,text,inline_css,?,?,NULLIF(?,0) FROM campaigns WHERE id=?", uuid.NewString(), time.Now().UnixMilli(), time.Now().UnixMilli(), r.Context().Value(identityKey{}).(*identity).UserID, id)
			if e != nil {
				return e
			}
			newID, e = res.LastInsertId()
			if e != nil {
				return e
			}
			_, e = c.ExecContext(r.Context(), "INSERT INTO campaign_targets SELECT ?,target_type,target_id,mode FROM campaign_targets WHERE campaign_id=?", newID, id)
			return e
		})
		result(w, map[string]int64{"id": newID}, err)
		return
	case has(action, "resend-unknown"):
		fail(w, http.StatusConflict, errors.New("uncertain recipients cannot be resent because SES may already have accepted them"))
		return
	}
	result(w, map[string]bool{"ok": err == nil}, err)
}
func has(s, end string) bool { return len(s) > len(end) && s[len(s)-len(end)-1:] == "/"+end }
func (a *API) sending(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if !i.Admin {
		fail(w, 403, errors.New("admin required"))
		return
	}
	if r.Method == "GET" {
		v, e := a.db.Setting(r.Context(), "sending_enabled")
		if errors.Is(e, sql.ErrNoRows) {
			v = "true"
			e = nil
		}
		result(w, map[string]any{"enabled": v != "false", "metrics": a.engine.Metrics()}, e)
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	result(w, in, a.engine.SetEnabled(r.Context(), in.Enabled))
}
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
		flusher, ok := w.(http.Flusher)
		if !ok {
			fail(w, 500, errors.New("streaming unavailable"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			v, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM campaign_stats WHERE campaign_id=?", id)
			if e != nil {
				return
			}
			b, _ := json.Marshal(v)
			if _, e = fmt.Fprintf(w, "event: stats\ndata: %s\n\n", b); e != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
		}
	}
	stats, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM campaign_stats WHERE campaign_id=?", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	links, e := queryRows(r.Context(), a.db.Reader, "SELECT l.id,l.url,COALESCE(s.clicks_total,0) clicks_total,COALESCE(s.clicks_unique,0) clicks_unique,COALESCE(s.clicks_human_total,0) clicks_human_total,COALESCE(s.clicks_human_unique,0) clicks_human_unique FROM links l LEFT JOIN link_stats s ON s.link_id=l.id WHERE l.campaign_id=? ORDER BY l.id", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	countries, e := queryRows(r.Context(), a.db.Reader, "SELECT country,opens,clicks FROM campaign_countries WHERE campaign_id=? ORDER BY country", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	hourly, e := queryRows(r.Context(), a.db.Reader, "SELECT hour,opens,clicks,deliveries FROM engagement_hourly WHERE campaign_id=? ORDER BY hour", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	recent := map[string]any{}
	for _, event := range campaignRecentEvents {
		rows, err := queryRows(r.Context(), a.db.Reader, campaignRecentSQL(event.column), id, brand)
		if err != nil {
			result(w, nil, err)
			return
		}
		recent[event.name] = rows
	}
	var firstSent, lastSent sql.NullInt64
	var fromName, fromEmail string
	var snapshotted bool
	e = a.db.Reader.QueryRowContext(r.Context(), `SELECT
		(SELECT sent_at FROM campaign_recipients WHERE campaign_id=c.id AND sent_at IS NOT NULL ORDER BY sent_at,subscriber_id LIMIT 1),
		(SELECT sent_at FROM campaign_recipients WHERE campaign_id=c.id AND sent_at IS NOT NULL ORDER BY sent_at DESC,subscriber_id DESC LIMIT 1),
		COALESCE(p.from_name,b.from_name),COALESCE(p.from_email,b.from_email),p.campaign_id IS NOT NULL
		FROM campaigns c JOIN brands b ON b.id=c.brand_id LEFT JOIN campaign_preparations p ON p.campaign_id=c.id WHERE c.id=?`, id).Scan(&firstSent, &lastSent, &fromName, &fromEmail, &snapshotted)
	if e != nil {
		result(w, nil, e)
		return
	}
	var firstAt, lastAt any
	if firstSent.Valid {
		firstAt = firstSent.Int64
	}
	if lastSent.Valid {
		lastAt = lastSent.Int64
	}
	targets, e := queryRows(r.Context(), a.db.Reader, `SELECT t.target_type,t.target_id,t.mode,
		CASE t.target_type WHEN 'list' THEN l.name ELSE s.name END AS name
		FROM campaign_targets t
		LEFT JOIN lists l ON t.target_type='list' AND l.id=t.target_id AND l.brand_id=?
		LEFT JOIN segments s ON t.target_type='segment' AND s.id=t.target_id AND s.brand_id=?
		WHERE t.campaign_id=? ORDER BY t.mode,t.target_type,t.target_id`, brand, brand, id)
	result(w, map[string]any{"stats": stats, "links": links, "countries": countries, "hourly": hourly, "recent": recent, "targets": targets,
		"delivery": map[string]any{"first_sent_at": firstAt, "last_sent_at": lastAt, "from_name": fromName, "from_email": fromEmail, "sender_snapshotted": snapshotted}}, e)
}
func (a *API) exportRecipients(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=recipients.csv")
	cw := csv.NewWriter(w)
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
