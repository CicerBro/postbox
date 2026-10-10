package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"postbox/internal/subscribers"
	"time"
)

func (a *API) registerOperations(m *http.ServeMux) {
	a.route(m, "GET /api/v1/settings", a.settings)
	a.route(m, "PATCH /api/v1/settings", a.settings)
	for _, p := range []string{"GET /api/v1/suppressions", "POST /api/v1/suppressions", "DELETE /api/v1/suppressions/{id}"} {
		a.route(m, p, a.suppressions)
	}
	for _, p := range []string{"POST /api/v1/subscribers/import", "POST /api/v1/subscribers/import/preview", "GET /api/v1/subscribers/export", "POST /api/v1/subscribers/mass-unsubscribe", "POST /api/v1/subscribers/mass-erase", "GET /api/v1/subscribers/{id}/activity", "POST /api/v1/housekeeping/unconfirmed", "GET /api/v1/jobs"} {
		a.route(m, p, a.operations)
	}
	for _, p := range []string{"GET /api/v1/templates", "POST /api/v1/templates", "PATCH /api/v1/templates/{id}", "DELETE /api/v1/templates/{id}"} {
		a.route(m, p, a.templates)
	}
}
func (a *API) operations(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/jobs" {
		i := r.Context().Value(identityKey{}).(*identity)
		if !i.Admin {
			fail(w, 403, errors.New("admin required"))
			return
		}
		after, limit := page(r)
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT id,queue,attempts,last_error,created_at FROM jobs WHERE status='failed' AND id>? ORDER BY id LIMIT ?", after, limit)
		result(w, v, e)
		return
	}
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	s := a.subscriberService()
	switch {
	case has(r.URL.Path, "export"):
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=subscribers.csv")
		_ = s.ExportCSV(r.Context(), w, brand, number(r.URL.Query().Get("list_id")))
	case has(r.URL.Path, "import"), has(r.URL.Path, "preview"):
		var opt subscribers.ImportOptions
		raw := r.URL.Query().Get("options")
		if raw != "" {
			if e := json.Unmarshal([]byte(raw), &opt); e != nil {
				result(w, nil, e)
				return
			}
		}
		opt.BrandID = brand
		opt.DryRun = has(r.URL.Path, "preview")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
		v, e := s.ImportCSV(r.Context(), r.Body, opt)
		result(w, v, e)
	case has(r.URL.Path, "activity"):
		after, limit := page(r)
		v, e := s.Timeline(r.Context(), brand, number(r.PathValue("id")), after, limit)
		result(w, v, e)
	case has(r.URL.Path, "unconfirmed"):
		var in struct {
			OlderThan int64 `json:"older_than"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.OlderThan <= 0 || in.OlderThan > time.Now().UnixMilli() {
			fail(w, 400, errors.New("older_than must be a past millisecond timestamp"))
			return
		}
		v, e := s.Housekeeping(r.Context(), brand, in.OlderThan)
		result(w, map[string]int64{"removed": v}, e)
	default:
		var in struct {
			Emails []string `json:"emails"`
			ListID int64    `json:"list_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if len(in.Emails) > 10000 {
			fail(w, 400, errors.New("maximum 10000 emails per request"))
			return
		}
		var n int
		var e error
		if has(r.URL.Path, "mass-erase") {
			n, e = s.MassErase(r.Context(), brand, in.Emails)
		} else {
			n, e = s.MassUnsubscribe(r.Context(), brand, in.ListID, in.Emails)
		}
		result(w, map[string]int{"affected": n}, e)
	}
}
func (a *API) templates(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	id := number(r.PathValue("id"))
	if r.Method == "GET" {
		after, limit := page(r)
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM templates WHERE brand_id=? AND id>? ORDER BY id LIMIT ?", brand, after, limit)
		result(w, v, e)
		return
	}
	if r.Method == "DELETE" {
		res, e := a.db.Writer.ExecContext(r.Context(), "DELETE FROM templates WHERE id=? AND brand_id=?", id, brand)
		result(w, map[string]bool{"deleted": true}, requireChanged(res, e))
		return
	}
	var in struct {
		Name string `json:"name"`
		HTML string `json:"html"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || in.HTML == "" {
		fail(w, 400, errors.New("name and html required"))
		return
	}
	if id == 0 {
		res, e := a.db.Writer.ExecContext(r.Context(), "INSERT INTO templates(brand_id,name,html,created_at,updated_at) VALUES(?,?,?,?,?)", brand, in.Name, in.HTML, time.Now().UnixMilli(), time.Now().UnixMilli())
		if e == nil {
			id, e = res.LastInsertId()
		}
		result(w, map[string]int64{"id": id}, e)
	} else {
		res, e := a.db.Writer.ExecContext(r.Context(), "UPDATE templates SET name=?,html=?,updated_at=? WHERE id=? AND brand_id=?", in.Name, in.HTML, time.Now().UnixMilli(), id, brand)
		result(w, map[string]int64{"id": id}, requireChanged(res, e))
	}
}
