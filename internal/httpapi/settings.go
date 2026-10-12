package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"postbox/internal/subscribers"
	"strings"
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

// Keep both effective-read queries shared with their query-plan tests.
const effectiveSuppressionQuery = "SELECT * FROM suppressions WHERE brand_id=? AND id>? UNION ALL SELECT * FROM suppressions WHERE brand_id IS NULL AND id>? ORDER BY id LIMIT ?"
const scopedEffectiveSuppressionQuery = "SELECT * FROM suppressions WHERE brand_id=? AND id>? UNION ALL SELECT p.* FROM suppressions p WHERE p.brand_id IS NULL AND p.id>? AND ((p.kind='email' AND EXISTS(SELECT 1 FROM subscribers s WHERE s.brand_id=? AND s.email=p.value)) OR (p.kind='domain' AND EXISTS(SELECT 1 FROM subscribers s WHERE s.brand_id=? AND s.domain=p.value))) ORDER BY id LIMIT ?"

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
		filters := []string{}
		filterArgs := []any{}
		if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
			filters = append(filters, "(instr(lower(value),lower(?))>0 OR instr(lower(reason),lower(?))>0)")
			filterArgs = append(filterArgs, search, search)
		}
		if kind := r.URL.Query().Get("kind"); kind != "" {
			if kind != "email" && kind != "domain" {
				fail(w, http.StatusBadRequest, errors.New("kind must be email or domain"))
				return
			}
			filters = append(filters, "kind=?")
			filterArgs = append(filterArgs, kind)
		}
		if reason := r.URL.Query().Get("reason"); reason != "" {
			switch reason {
			case "manual", "ses_sync", "hard_bounce", "soft_bounce_limit", "complaint":
			default:
				fail(w, http.StatusBadRequest, errors.New("reason must be manual, ses_sync, hard_bounce, soft_bounce_limit, or complaint"))
				return
			}
			filters = append(filters, "reason=?")
			filterArgs = append(filterArgs, reason)
		}
		query := "SELECT * FROM suppressions WHERE brand_id=? AND id>? ORDER BY id LIMIT ?"
		args := []any{brand, after, limit}
		if scope == "global" {
			query = "SELECT * FROM suppressions WHERE brand_id IS NULL AND id>? ORDER BY id LIMIT ?"
			args = []any{after, limit}
		} else if scope == "effective" {
			// Separate indexed ranges preserve keyset pagination for both scopes.
			query = effectiveSuppressionQuery
			args = []any{brand, after, after, limit}
			i := r.Context().Value(identityKey{}).(*identity)
			if !i.Admin || i.UserID == 0 || i.BrandID != 0 {
				query = scopedEffectiveSuppressionQuery
				args = []any{brand, after, after, brand, brand, limit}
			}
		}
		if len(filters) > 0 {
			// Filter every permitted scope before limiting the page, so matches
			// beyond the first page are searchable without loading all rows.
			query = "SELECT * FROM (" + strings.TrimSuffix(query, " ORDER BY id LIMIT ?") + ") WHERE " + strings.Join(filters, " AND ") + " ORDER BY id LIMIT ?"
			args = append(args[:len(args)-1], filterArgs...)
			args = append(args, limit)
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
	if r.Method == "PATCH" {
		var patch subscribers.SuppressionPatch
		if !decode(w, r, &patch) {
			return
		}
		var brandID *int64
		if scope == "brand" {
			brandID = &brand
		}
		v, err := a.subscriberService().UpdateSuppression(r.Context(), number(r.PathValue("id")), brandID, patch)
		if errors.Is(err, subscribers.ErrSuppressionConflict) {
			fail(w, http.StatusConflict, err)
			return
		}
		result(w, v, err)
		return
	}
	var in subscribers.Suppression
	if !decode(w, r, &in) {
		return
	}
	in.BrandID = &brand
	if scope == "global" {
		in.BrandID = nil
	}
	in.Reason = "manual"
	result(w, in, a.subscriberService().AddSuppression(r.Context(), in))
}
