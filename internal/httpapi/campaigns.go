package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"net/http"
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
	m.HandleFunc("GET /preview/campaign/{token}", a.savedCampaignPreview)
	m.HandleFunc("GET /preview/unsubscribe/{token}", a.unsubscribePreview)
	m.HandleFunc("POST /preview/unsubscribe/{token}", a.unsubscribePreview)
	a.route(m, "POST /api/v1/email-preview", a.editorPreview)
	a.route(m, "GET /api/v1/campaigns/options", a.campaignOptions)
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
		v, e := a.campaignRows(r, brand, id)
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
