package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"postbox/internal/subscribers"
)

var listSettingNames = []string{"confirmation_email", "confirmation_page_html", "thankyou_email", "thankyou_html", "goodbye_email", "goodbye_html"}

func (a *API) listSettings(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	id := number(r.PathValue("id"))
	var found int
	if e := a.db.Reader.QueryRowContext(r.Context(), "SELECT 1 FROM lists WHERE id=? AND brand_id=?", id, brand).Scan(&found); e != nil {
		result(w, nil, e)
		return
	}
	prefix := fmt.Sprintf("list.%d.", id)
	if r.Method == "PATCH" {
		var in map[string]json.RawMessage
		if !decode(w, r, &in) {
			return
		}
		values := map[string]*string{}
		for key, raw := range in {
			allowed := false
			for _, name := range listSettingNames {
				if name == key {
					allowed = true
				}
			}
			if !allowed {
				fail(w, 400, fmt.Errorf("unknown list setting %s", key))
				return
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				values[key] = nil
				continue
			}
			var value string
			if key == "confirmation_email" || key == "thankyou_email" || key == "goodbye_email" {
				var mail struct {
					Subject string `json:"subject"`
					HTML    string `json:"html"`
					Text    string `json:"text"`
				}
				d := json.NewDecoder(bytes.NewReader(raw))
				d.DisallowUnknownFields()
				if e := d.Decode(&mail); e != nil {
					fail(w, 400, e)
					return
				}
				if mail.Subject == "" || mail.HTML == "" {
					fail(w, 400, errors.New("email setting requires subject and html"))
					return
				}
				value = string(raw)
			} else {
				if e := json.Unmarshal(raw, &value); e != nil {
					fail(w, 400, e)
					return
				}
				if value == "" {
					values[key] = nil
					continue
				}
			}
			values[key] = &value
		}
		e := a.db.Immediate(r.Context(), func(c *sql.Conn) error {
			if e := c.QueryRowContext(r.Context(), "SELECT 1 FROM lists WHERE id=? AND brand_id=?", id, brand).Scan(&found); e != nil {
				return e
			}
			for key, value := range values {
				if value == nil {
					if _, e := c.ExecContext(r.Context(), "DELETE FROM settings WHERE key=?", prefix+key); e != nil {
						return e
					}
				} else {
					if _, e := c.ExecContext(r.Context(), "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", prefix+key, *value); e != nil {
						return e
					}
				}
				// Replacing or clearing confirmation email also clears old split overrides.
				if key == "confirmation_email" {
					for _, suffix := range []string{"confirmation_subject", "confirmation_html", "confirmation_text"} {
						if _, e := c.ExecContext(r.Context(), "DELETE FROM settings WHERE key=?", prefix+suffix); e != nil {
							return e
						}
					}
				}
			}
			return nil
		})
		if e != nil {
			result(w, nil, e)
			return
		}
	}
	overrides, effective := map[string]any{}, map[string]any{}
	for _, name := range listSettingNames {
		decodeValue := func(raw string) (any, error) {
			if name == "confirmation_email" || name == "thankyou_email" || name == "goodbye_email" {
				var v any
				e := json.Unmarshal([]byte(raw), &v)
				return v, e
			}
			return raw, nil
		}
		raw, e := a.db.Setting(r.Context(), prefix+name)
		if e == nil {
			v, e := decodeValue(raw)
			if e != nil {
				result(w, nil, e)
				return
			}
			overrides[name] = v
		} else if !errors.Is(e, sql.ErrNoRows) {
			result(w, nil, e)
			return
		}
		raw, e = subscribers.NotificationSetting(r.Context(), a.db.Reader, brand, id, name)
		if name == "confirmation_page_html" && errors.Is(e, sql.ErrNoRows) {
			raw, e = subscribers.NotificationSetting(r.Context(), a.db.Reader, brand, id, "thankyou_html")
		}
		if e == nil {
			v, e := decodeValue(raw)
			if e != nil {
				result(w, nil, e)
				return
			}
			effective[name] = v
		} else if !errors.Is(e, sql.ErrNoRows) {
			result(w, nil, e)
			return
		}
	}
	// Older installations stored confirmation email fields individually.
	// Surface those overrides so the editor reflects the message actually sent.
	if _, modern := overrides["confirmation_email"]; !modern {
		message := map[string]any{"subject": "Confirm your subscription", "html": "<p>Please <a href=\"{{ confirmation_url }}\">confirm your subscription</a>.</p>", "text": "Confirm your subscription: {{ confirmation_url }}"}
		if inherited, ok := effective["confirmation_email"].(map[string]any); ok {
			for key, value := range inherited {
				message[key] = value
			}
		}
		legacy := false
		for suffix, field := range map[string]string{"confirmation_subject": "subject", "confirmation_html": "html", "confirmation_text": "text"} {
			value, e := a.db.Setting(r.Context(), prefix+suffix)
			if e == nil {
				message[field] = value
				legacy = true
			} else if !errors.Is(e, sql.ErrNoRows) {
				result(w, nil, e)
				return
			}
		}
		if legacy {
			overrides["confirmation_email"] = message
			effective["confirmation_email"] = message
		}
	}
	result(w, map[string]any{"overrides": overrides, "effective": effective}, nil)
}
