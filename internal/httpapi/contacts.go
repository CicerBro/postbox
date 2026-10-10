package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"postbox/internal/contacts"
	"strconv"
	"strings"
)

func (a *API) registerContacts(m *http.ServeMux) {
	a.route(m, "GET /api/v1/lists/{id}/settings", a.listSettings)
	a.route(m, "PATCH /api/v1/lists/{id}/settings", a.listSettings)
	for _, p := range []string{"GET /api/v1/lists", "POST /api/v1/lists", "GET /api/v1/lists/{id}", "PATCH /api/v1/lists/{id}", "DELETE /api/v1/lists/{id}"} {
		a.route(m, p, a.lists)
	}
	for _, p := range []string{"GET /api/v1/subscribers", "POST /api/v1/subscribers", "POST /api/v1/subscribers/bulk", "GET /api/v1/subscribers/by-email", "GET /api/v1/subscribers/{id}", "PATCH /api/v1/subscribers/{id}", "DELETE /api/v1/subscribers/{id}", "POST /api/v1/subscribers/{id}/unsubscribe", "GET /api/v1/subscribers/{id}/memberships", "PUT /api/v1/subscribers/{id}/memberships"} {
		a.route(m, p, a.subscribers)
	}
	for _, p := range []string{"GET /api/v1/custom-fields", "POST /api/v1/custom-fields", "PATCH /api/v1/custom-fields/{id}"} {
		a.route(m, p, a.fields)
	}
	for _, p := range []string{"GET /api/v1/segments", "POST /api/v1/segments", "PATCH /api/v1/segments/{id}", "DELETE /api/v1/segments/{id}", "POST /api/v1/segments/preview"} {
		a.route(m, p, a.segments)
	}
}
func (a *API) lists(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	s := a.contacts()
	id := number(r.PathValue("id"))
	switch r.Method {
	case "GET":
		const listColumns = "SELECT l.*,coalesce(c.active,0) AS active,coalesce(c.subscribed,0) AS subscribed,coalesce(c.unsubscribed,0) AS unsubscribed,coalesce(c.bounced,0) AS bounced,coalesce(c.complained,0) AS complained FROM lists l LEFT JOIN list_counters c ON c.list_id=l.id "
		if id != 0 {
			v, e := queryRows(r.Context(), a.db.Reader, listColumns+"WHERE l.brand_id=? AND l.id=?", brand, id)
			if e == nil && len(v) == 0 {
				http.NotFound(w, r)
				return
			}
			result(w, v, e)
			return
		}
		after, limit := page(r)
		v, e := queryRows(r.Context(), a.db.Reader, listColumns+"WHERE l.brand_id=? AND l.id>? ORDER BY l.id LIMIT ?", brand, after, limit)
		result(w, v, e)
	case "DELETE":
		result(w, map[string]bool{"deleted": true}, s.DeleteList(r.Context(), brand, id))
	default:
		var in contacts.List
		if !decode(w, r, &in) {
			return
		}
		in.BrandID = brand
		in.ID = id
		if id == 0 {
			v, e := s.CreateList(r.Context(), in)
			result(w, v, e)
		} else {
			result(w, in, s.UpdateList(r.Context(), in))
		}
	}
}
func (a *API) subscribers(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	s := a.contacts()
	id := number(r.PathValue("id"))
	if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/memberships") {
		var in []struct {
			ListID int64  `json:"list_id"`
			Status string `json:"status"`
		}
		if !decode(w, r, &in) {
			return
		}
		var email string
		e := a.db.Reader.QueryRowContext(r.Context(), "SELECT email FROM subscribers WHERE brand_id=? AND id=?", brand, id).Scan(&email)
		if e != nil {
			result(w, nil, e)
			return
		}
		v, e := s.GetByEmail(r.Context(), brand, email)
		if e != nil {
			result(w, nil, e)
			return
		}
		for _, membership := range in {
			var n int
			if e = a.db.Reader.QueryRowContext(r.Context(), "SELECT 1 FROM lists WHERE brand_id=? AND id=?", brand, membership.ListID).Scan(&n); e != nil {
				result(w, nil, e)
				return
			}
			if membership.Status == "unsubscribed" {
				e = s.Unsubscribe(r.Context(), brand, id, membership.ListID)
			} else if membership.Status == "subscribed" {
				_, e = s.Upsert(r.Context(), contacts.UpsertInput{BrandID: brand, Email: v.Email, Name: v.Name, Fields: v.Fields, ListIDs: []int64{membership.ListID}, Resubscribe: true})
			} else {
				e = errors.New("membership status must be subscribed or unsubscribed")
			}
			if e != nil {
				result(w, nil, e)
				return
			}
		}
		result(w, map[string]bool{"updated": true}, nil)
		return
	}
	if r.URL.Path == "/api/v1/subscribers/by-email" {
		v, e := s.GetByEmail(r.Context(), brand, r.URL.Query().Get("email"))
		result(w, v, e)
		return
	}
	if r.URL.Path == "/api/v1/subscribers/bulk" {
		var inputs []contacts.UpsertInput
		if !decode(w, r, &inputs) {
			return
		}
		if len(inputs) > 1000 {
			fail(w, 400, errors.New("bulk requests limited to 1000 subscribers"))
			return
		}
		out := []contacts.Subscriber{}
		for _, in := range inputs {
			in.BrandID = brand
			v, e := s.Upsert(r.Context(), in)
			if e != nil {
				result(w, nil, e)
				return
			}
			out = append(out, v)
		}
		result(w, out, nil)
		return
	}
	if r.Method == "GET" {
		if id == 0 {
			after, limit := page(r)
			filter := contacts.SearchFilter{EmailPrefix: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), MembershipStatus: r.URL.Query().Get("membership_status")}
			if raw := r.URL.Query().Get("list_id"); raw != "" {
				var e error
				filter.ListID, e = strconv.ParseInt(raw, 10, 64)
				if e != nil || filter.ListID <= 0 {
					fail(w, 400, errors.New("list_id must be a positive integer"))
					return
				}
				var exists int
				if e = a.db.Reader.QueryRowContext(r.Context(), "SELECT 1 FROM lists WHERE brand_id=? AND id=?", brand, filter.ListID).Scan(&exists); e != nil {
					result(w, nil, e)
					return
				}
			}
			v, e := s.SearchFiltered(r.Context(), brand, after, limit, filter)
			result(w, v, e)
		} else {
			v, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM subscribers WHERE brand_id=? AND id=?", brand, id)
			if e == nil && len(v) == 0 {
				e = sql.ErrNoRows
			}
			if e == nil && r.PathValue("id") != "" && strings.HasSuffix(r.URL.Path, "/memberships") {
				v, e = queryRows(r.Context(), a.db.Reader, "SELECT ls.* FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE s.brand_id=? AND s.id=?", brand, id)
			}
			result(w, v, e)
		}
		return
	}
	if r.Method == "DELETE" {
		result(w, map[string]bool{"erased": true}, s.Erase(r.Context(), brand, id))
		return
	}
	if id != 0 && r.Method == "POST" {
		var in struct {
			ListID int64 `json:"list_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		result(w, map[string]bool{"unsubscribed": true}, s.Unsubscribe(r.Context(), brand, id, in.ListID))
		return
	}
	var in contacts.UpsertInput
	if !decode(w, r, &in) {
		return
	}
	in.BrandID = brand
	if id != 0 {
		var email string
		e := a.db.Reader.QueryRowContext(r.Context(), "SELECT email FROM subscribers WHERE brand_id=? AND id=?", brand, id).Scan(&email)
		if e != nil {
			result(w, nil, e)
			return
		}
		if in.Email != "" && in.Email != email {
			fail(w, 400, errors.New("subscriber email cannot be changed"))
			return
		}
		in.Email = email
	}
	v, e := s.Upsert(r.Context(), in)
	result(w, v, e)
}
func (a *API) fields(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	s := a.contacts()
	if r.Method == "GET" {
		v, e := s.CustomFields(r.Context(), brand)
		result(w, v, e)
		return
	}
	var in contacts.CustomField
	if !decode(w, r, &in) {
		return
	}
	in.BrandID = brand
	in.ID = number(r.PathValue("id"))
	v, e := s.SaveCustomField(r.Context(), in)
	result(w, v, e)
}
func (a *API) segments(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	s := a.contacts()
	if r.URL.Path == "/api/v1/segments/preview" {
		var rule contacts.Rule
		if !decode(w, r, &rule) {
			return
		}
		n, e := s.SegmentCount(r.Context(), brand, rule)
		result(w, map[string]int64{"count": n}, e)
		return
	}
	if r.Method == "GET" {
		after, limit := page(r)
		v, e := queryRows(r.Context(), a.db.Reader, "SELECT * FROM segments WHERE brand_id=? AND id>? ORDER BY id LIMIT ?", brand, after, limit)
		result(w, v, e)
		return
	}
	if r.Method == "DELETE" {
		res, e := a.db.Writer.ExecContext(r.Context(), "DELETE FROM segments WHERE brand_id=? AND id=?", brand, number(r.PathValue("id")))
		result(w, map[string]bool{"deleted": true}, requireChanged(res, e))
		return
	}
	var in contacts.Segment
	if !decode(w, r, &in) {
		return
	}
	in.BrandID = brand
	in.ID = number(r.PathValue("id"))
	v, e := s.SaveSegment(r.Context(), in)
	result(w, v, e)
}

func (a *API) contacts() *contacts.Service {
	s := contacts.New(a.db)
	s.SetBaseURL(a.cfg.BaseURL)
	return s
}
