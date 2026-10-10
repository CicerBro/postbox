package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"postbox/internal/render"
	"time"
)

func (a *API) brands(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if r.Method == "GET" {
		q := "SELECT b.* FROM brands b WHERE b.id>?"
		after, limit := page(r)
		args := []any{after}
		if !i.Admin {
			if i.BrandID != 0 {
				q += " AND b.id=?"
				args = append(args, i.BrandID)
			} else {
				q += " AND EXISTS(SELECT 1 FROM user_brands u WHERE u.brand_id=b.id AND u.user_id=?)"
				args = append(args, i.UserID)
			}
		}
		q += " ORDER BY b.id LIMIT ?"
		args = append(args, limit)
		v, e := queryRows(r.Context(), a.db.Reader, q, args...)
		result(w, v, e)
		return
	}
	if !i.Admin {
		fail(w, 403, errors.New("admin required"))
		return
	}
	a.saveBrand(w, r, 0)
}
func (a *API) brand(w http.ResponseWriter, r *http.Request) {
	id, ok := a.scoped(w, r)
	if !ok {
		return
	}
	if r.Method == "GET" {
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM brands WHERE id=?", id)
		if e == nil && len(v) == 0 {
			e = sql.ErrNoRows
		}
		result(w, v, e)
		return
	}
	if !r.Context().Value(identityKey{}).(*identity).Admin {
		fail(w, 403, errors.New("admin required"))
		return
	}
	if r.Method == "DELETE" {
		res, e := a.db.Writer.ExecContext(r.Context(), "DELETE FROM brands WHERE id=?", id)
		result(w, map[string]bool{"deleted": true}, requireChanged(res, e))
		return
	}
	a.saveBrand(w, r, id)
}
func (a *API) saveBrand(w http.ResponseWriter, r *http.Request, id int64) {
	var in struct {
		Name             string `json:"name"`
		FromName         string `json:"from_name"`
		FromEmail        string `json:"from_email"`
		ReplyTo          string `json:"reply_to"`
		ConfigurationSet string `json:"configuration_set"`
		TrackingDomain   string `json:"tracking_domain"`
	}
	if !decode(w, r, &in) {
		return
	}
	email, e := render.NormalizeEmail(in.FromEmail)
	if e != nil || in.Name == "" {
		fail(w, 400, errors.New("name and valid from_email required"))
		return
	}
	if id == 0 {
		res, e := a.db.Writer.ExecContext(r.Context(), "INSERT INTO brands(name,from_name,from_email,reply_to,configuration_set,tracking_domain,created_at) VALUES(?,?,?,?,?,NULLIF(?,''),?)", in.Name, in.FromName, email, in.ReplyTo, in.ConfigurationSet, in.TrackingDomain, time.Now().UnixMilli())
		if e == nil {
			id, e = res.LastInsertId()
		}
		result(w, map[string]int64{"id": id}, e)
	} else {
		res, e := a.db.Writer.ExecContext(r.Context(), "UPDATE brands SET name=?,from_name=?,from_email=?,reply_to=?,configuration_set=?,tracking_domain=NULLIF(?,'') WHERE id=?", in.Name, in.FromName, email, in.ReplyTo, in.ConfigurationSet, in.TrackingDomain, id)
		result(w, map[string]int64{"id": id}, requireChanged(res, e))
	}
}
func (a *API) keys(w http.ResponseWriter, r *http.Request) {
	id, ok := a.scoped(w, r)
	if !ok {
		return
	}
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 || !i.Admin {
		fail(w, 403, errors.New("API keys require an administrator session"))
		return
	}
	if r.Method == "GET" {
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT id,name,prefix,scopes,last_used_at,revoked_at,created_at FROM api_keys WHERE brand_id=? ORDER BY id", id)
		result(w, v, e)
		return
	}
	if r.Method == "DELETE" {
		res, e := a.db.Writer.ExecContext(r.Context(), "UPDATE api_keys SET revoked_at=? WHERE id=? AND brand_id=?", time.Now().UnixMilli(), number(r.PathValue("id")), id)
		result(w, map[string]bool{"revoked": true}, requireChanged(res, e))
		return
	}
	var in struct {
		Name   string
		Scopes []string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || len(in.Scopes) == 0 {
		fail(w, 400, errors.New("name and scopes required"))
		return
	}
	for _, s := range in.Scopes {
		if s != "read" && s != "write" {
			fail(w, 400, errors.New("scopes must be read or write"))
			return
		}
	}
	prefix := secret()[:12]
	key := prefix + "." + secret()
	scopes, _ := json.Marshal(in.Scopes)
	res, e := a.db.Writer.ExecContext(r.Context(), "INSERT INTO api_keys(brand_id,name,prefix,secret_hash,scopes,created_at) VALUES(?,?,?,?,?,?)", id, in.Name, prefix, digest(key), string(scopes), time.Now().UnixMilli())
	var keyID int64
	if e == nil {
		keyID, e = res.LastInsertId()
	}
	result(w, map[string]any{"id": keyID, "prefix": prefix, "secret": key}, e)
}
func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.scoped(w, r)
	if !ok {
		return
	}
	stats, e := queryRows(r.Context(), a.db.Reader, "SELECT COALESCE(SUM(s.sent),0) sent,COALESCE(SUM(s.delivered),0) delivered,COALESCE(SUM(s.opens_unique),0) opened,COALESCE(SUM(s.clicks_unique),0) clicked,COALESCE(SUM(s.bounces_hard),0) bounced,COALESCE(SUM(s.complaints),0) complaints FROM campaigns c LEFT JOIN campaign_stats s ON s.campaign_id=c.id WHERE c.brand_id=?", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	growth, e := queryRows(r.Context(), a.db.Reader, "SELECT d.day,SUM(d.active) active,SUM(d.subscribed) subscribed,SUM(d.unsubscribed) unsubscribed FROM lists l JOIN list_daily_stats d ON d.list_id=l.id WHERE l.brand_id=? AND d.day>=? GROUP BY d.day ORDER BY d.day", id, time.Now().AddDate(0, 0, -60).UnixMilli())
	if e != nil {
		result(w, nil, e)
		return
	}
	hours, e := queryRows(r.Context(), a.db.Reader, "SELECT hour,sent FROM send_hourly WHERE brand_id=? AND hour>=? ORDER BY hour", id, time.Now().Add(-48*time.Hour).UnixMilli())
	result(w, map[string]any{"stats": stats, "growth": growth, "hourly": hours}, e)
}
