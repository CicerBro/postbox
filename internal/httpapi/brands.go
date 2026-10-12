package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"postbox/internal/render"
	"strconv"
	"time"
)

func (a *API) brands(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if r.Method == "GET" {
		projection, args := brandPermissionProjection(i)
		q := "SELECT b.*, " + projection + " AS permissions FROM brands b WHERE b.id>?"
		after, limit := page(r)
		args = append(args, after)
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
		if e == nil {
			e = decodeBrandPermissions(v)
		}
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
		projection, args := brandPermissionProjection(r.Context().Value(identityKey{}).(*identity))
		args = append(args, id)
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT b.*, "+projection+" AS permissions FROM brands b WHERE b.id=?", args...)
		if e == nil {
			e = decodeBrandPermissions(v)
		}
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

// Include only the current caller's grants, never another user's membership.
func brandPermissionProjection(i *identity) (string, []any) {
	if i.Admin {
		return `?`, []any{`["read","write","send"]`}
	}
	if i.BrandID != 0 {
		scopes, _ := json.Marshal(i.Scopes)
		return `?`, []any{string(scopes)}
	}
	return `(SELECT u.permissions FROM user_brands u WHERE u.brand_id=b.id AND u.user_id=?)`, []any{i.UserID}
}

func decodeBrandPermissions(rows []map[string]any) error {
	for _, row := range rows {
		permissions := []string{}
		if raw, ok := row["permissions"].(string); ok {
			if err := json.Unmarshal([]byte(raw), &permissions); err != nil {
				return err
			}
		}
		row["permissions"] = permissions
	}
	return nil
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

// overviewPeriodSQL reads only campaign counters. Late engagement remains attributed
// to the campaign's start period, rather than claiming to count event timestamps.
const overviewHistorySQL = `SELECT
 COALESCE(SUM(s.sent),0), COALESCE(SUM(s.delivered),0),
 COALESCE(SUM(s.opens_unique),0), COALESCE(SUM(s.clicks_unique),0),
 COALESCE(SUM(s.bounces_hard),0), COALESCE(SUM(s.complaints),0)
 FROM campaigns c JOIN campaign_stats s ON s.campaign_id=c.id
 WHERE c.brand_id=? AND s.sent>0`

const overviewPeriodSQL = overviewHistorySQL + `
 AND COALESCE(c.started_at,c.finished_at,c.created_at)>=?
 AND COALESCE(c.started_at,c.finished_at,c.created_at)<?`

type overviewKPI struct {
	Sent              int64    `json:"sent"`
	Delivered         int64    `json:"delivered"`
	Opened            int64    `json:"opened"`
	Clicked           int64    `json:"clicked"`
	Bounced           int64    `json:"bounced"`
	Complaints        int64    `json:"complaints"`
	DeliveredPercent  *float64 `json:"delivered_percent"`
	OpenedPercent     *float64 `json:"opened_percent"`
	ClickedPercent    *float64 `json:"clicked_percent"`
	BouncedPercent    *float64 `json:"bounced_percent"`
	ComplaintsPercent *float64 `json:"complaints_percent"`
}

func overviewPercent(n, total int64) *float64 {
	if total == 0 {
		return nil
	}
	v := 100 * float64(n) / float64(total)
	return &v
}
func overviewPoints(current, previous *float64) *float64 {
	if current == nil || previous == nil {
		return nil
	}
	v := *current - *previous
	return &v
}
func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	id, ok := a.scoped(w, r)
	if !ok {
		return
	}
	days := 30
	allHistory := false
	if raw, exists := r.URL.Query()["days"]; exists {
		var err error
		if len(raw) != 1 {
			respond(w, 400, map[string]string{"error": "days must be all or an integer between 1 and 365"})
			return
		}
		allHistory = raw[0] == "all"
		if !allHistory {
			days, err = strconv.Atoi(raw[0])
			if err != nil || days < 1 || days > 365 {
				respond(w, 400, map[string]string{"error": "days must be all or an integer between 1 and 365"})
				return
			}
		}
	}
	now := time.Now().UTC()
	end := now.UnixMilli()
	start := now.AddDate(0, 0, -days).UnixMilli()
	previousStart := now.AddDate(0, 0, -2*days).UnixMilli()
	stats, e := queryRows(r.Context(), a.db.Reader, "SELECT COALESCE(SUM(s.sent),0) sent,COALESCE(SUM(s.delivered),0) delivered,COALESCE(SUM(s.opens_unique),0) opened,COALESCE(SUM(s.clicks_unique),0) clicked,COALESCE(SUM(s.bounces_hard),0) bounced,COALESCE(SUM(s.complaints),0) complaints FROM campaigns c LEFT JOIN campaign_stats s ON s.campaign_id=c.id WHERE c.brand_id=?", id)
	if e != nil {
		result(w, nil, e)
		return
	}
	readPeriod := func(from, to int64, entireHistory bool) (overviewKPI, error) {
		var k overviewKPI
		query, args := overviewPeriodSQL, []any{id, from, to}
		if entireHistory {
			query, args = overviewHistorySQL, []any{id}
		}
		err := a.db.Reader.QueryRowContext(r.Context(), query, args...).Scan(&k.Sent, &k.Delivered, &k.Opened, &k.Clicked, &k.Bounced, &k.Complaints)
		k.DeliveredPercent = overviewPercent(k.Delivered, k.Sent)
		k.OpenedPercent = overviewPercent(k.Opened, k.Sent)
		k.ClickedPercent = overviewPercent(k.Clicked, k.Sent)
		k.BouncedPercent = overviewPercent(k.Bounced, k.Sent)
		k.ComplaintsPercent = overviewPercent(k.Complaints, k.Sent)
		return k, err
	}
	current, e := readPeriod(start, end, allHistory)
	if e != nil {
		result(w, nil, e)
		return
	}
	var previous overviewKPI
	if !allHistory {
		previous, e = readPeriod(previousStart, start, false)
		if e != nil {
			result(w, nil, e)
			return
		}
	}
	change := map[string]any{
		"sent_percent":      overviewPercent(current.Sent-previous.Sent, previous.Sent),
		"delivered_points":  overviewPoints(current.DeliveredPercent, previous.DeliveredPercent),
		"opened_points":     overviewPoints(current.OpenedPercent, previous.OpenedPercent),
		"clicked_points":    overviewPoints(current.ClickedPercent, previous.ClickedPercent),
		"bounced_points":    overviewPoints(current.BouncedPercent, previous.BouncedPercent),
		"complaints_points": overviewPoints(current.ComplaintsPercent, previous.ComplaintsPercent),
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	growthStart := today.AddDate(0, 0, -59)
	growthRows, e := queryRows(r.Context(), a.db.Reader, "SELECT d.day,SUM(d.active) active,SUM(d.subscribed) subscribed,SUM(d.unsubscribed) unsubscribed FROM lists l JOIN list_daily_stats d ON d.list_id=l.id WHERE l.brand_id=? AND d.day>=? AND d.day<? GROUP BY d.day ORDER BY d.day", id, growthStart.UnixMilli(), today.AddDate(0, 0, 1).UnixMilli())
	if e != nil {
		result(w, nil, e)
		return
	}
	byDay := map[int64]map[string]any{}
	for _, row := range growthRows {
		byDay[row["day"].(int64)] = row
	}
	growth := make([]map[string]any, 0, 60)
	for i := 0; i < 60; i++ {
		day := growthStart.AddDate(0, 0, i).UnixMilli()
		row := byDay[day]
		if row == nil {
			row = map[string]any{"day": day, "active": int64(0), "subscribed": int64(0), "unsubscribed": int64(0)}
		}
		growth = append(growth, row)
	}
	hourStart := today.AddDate(0, 0, -1)
	hourRows, e := queryRows(r.Context(), a.db.Reader, "SELECT hour,sent FROM send_hourly WHERE brand_id=? AND hour>=? AND hour<? ORDER BY hour", id, hourStart.UnixMilli(), today.AddDate(0, 0, 1).UnixMilli())
	if e != nil {
		result(w, nil, e)
		return
	}
	byHour := map[int64]int64{}
	for _, row := range hourRows {
		byHour[row["hour"].(int64)] = row["sent"].(int64)
	}
	hours := make([]map[string]any, 0, 48)
	comparison := make([]map[string]any, 0, 24)
	for i := 0; i < 48; i++ {
		hour := hourStart.Add(time.Duration(i) * time.Hour).UnixMilli()
		hours = append(hours, map[string]any{"hour": hour, "sent": byHour[hour]})
	}
	for i := 0; i < 24; i++ {
		comparison = append(comparison, map[string]any{"hour": i, "today": hours[i+24]["sent"], "yesterday": hours[i]["sent"]})
	}
	var periodDays, currentStart, comparisonStart any = days, start, previousStart
	if allHistory {
		periodDays, currentStart, comparisonStart = nil, nil, nil
	}
	result(w, map[string]any{"stats": stats, "period": map[string]any{"days": periodDays, "basis": "campaign_start", "current_start": currentStart, "previous_start": comparisonStart, "end": end, "timezone": "UTC"}, "current": current, "previous": previous, "change": change, "growth": growth, "hourly": hours, "hourly_comparison": comparison}, nil)
}
