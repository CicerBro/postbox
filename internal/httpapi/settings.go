package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"postbox/internal/contacts"
)

func (a *API) settings(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	prefix := fmt.Sprintf("brand.%d.", brand)
	names := []string{"goodbye_html", "goodbye_email", "thankyou_html", "thankyou_email", "confirmation_email"}
	if r.Method == "GET" {
		out := map[string]any{}
		for _, name := range names {
			v, e := a.db.Setting(r.Context(), prefix+name)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				result(w, nil, e)
				return
			}
			if name == "goodbye_email" || name == "thankyou_email" || name == "confirmation_email" {
				var x any
				if e = json.Unmarshal([]byte(v), &x); e != nil {
					result(w, nil, e)
					return
				}
				out[name] = x
			} else {
				out[name] = v
			}
		}
		result(w, out, nil)
		return
	}
	var in map[string]json.RawMessage
	if !decode(w, r, &in) {
		return
	}
	e := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
		for key, raw := range in {
			allowed := false
			for _, name := range names {
				if key == name {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("unknown setting %s", key)
			}
			value := ""
			if key == "goodbye_email" || key == "thankyou_email" || key == "confirmation_email" {
				var mail struct {
					Subject string `json:"subject"`
					HTML    string `json:"html"`
					Text    string `json:"text"`
				}
				if e := json.Unmarshal(raw, &mail); e != nil {
					return e
				}
				if mail.Subject == "" || mail.HTML == "" {
					return errors.New("email setting requires subject and html")
				}
				value = string(raw)
			} else {
				if e := json.Unmarshal(raw, &value); e != nil {
					return e
				}
			}
			if _, e := c.ExecContext(r.Context(), "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", prefix+key, value); e != nil {
				return e
			}
		}
		return nil
	})
	result(w, map[string]bool{"saved": true}, e)
}
func (a *API) suppressions(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		if r.Method == "GET" {
			scope = "effective"
		} else {
			scope = "brand"
		}
	}
	if scope != "brand" && scope != "global" && !(scope == "effective" && r.Method == "GET") {
		fail(w, http.StatusBadRequest, errors.New("scope must be brand or global; GET also supports effective"))
		return
	}
	var brand int64
	if scope == "global" {
		i := r.Context().Value(identityKey{}).(*identity)
		if !i.Admin || i.UserID == 0 || i.BrandID != 0 {
			fail(w, http.StatusForbidden, errors.New("global suppressions require an admin session"))
			return
		}
	} else {
		var ok bool
		brand, ok = a.scoped(w, r)
		if !ok {
			return
		}
	}
	if r.Method == "GET" {
		after, limit := page(r)
		query := "SELECT * FROM suppressions WHERE brand_id=? AND id>? ORDER BY id LIMIT ?"
		args := []any{brand, after, limit}
		if scope == "global" {
			query = "SELECT * FROM suppressions WHERE brand_id IS NULL AND id>? ORDER BY id LIMIT ?"
			args = []any{after, limit}
		} else if scope == "effective" {
			// Separate indexed ranges keep pagination bounded for both scopes.
			query = "SELECT * FROM suppressions WHERE brand_id=? AND id>? UNION ALL SELECT * FROM suppressions WHERE brand_id IS NULL AND id>? ORDER BY id LIMIT ?"
			args = []any{brand, after, after, limit}
		}
		v, e := queryRows(r.Context(), a.db.Reader, query, args...)
		result(w, v, e)
		return
	}
	if r.Method == "DELETE" {
		query := "DELETE FROM suppressions WHERE brand_id=? AND id=?"
		args := []any{brand, number(r.PathValue("id"))}
		if scope == "global" {
			query = "DELETE FROM suppressions WHERE brand_id IS NULL AND id=?"
			args = []any{number(r.PathValue("id"))}
		}
		res, e := a.db.Writer.ExecContext(r.Context(), query, args...)
		result(w, map[string]bool{"deleted": true}, requireChanged(res, e))
		return
	}
	var in contacts.Suppression
	if !decode(w, r, &in) {
		return
	}
	in.BrandID = &brand
	if scope == "global" {
		in.BrandID = nil
	}
	in.Reason = "manual"
	result(w, in, a.contacts().AddSuppression(r.Context(), in))
}
