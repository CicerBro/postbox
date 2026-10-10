package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"postbox/internal/jobs"
)

var webhookEvents = []string{"subscribe", "unsubscribe", "bounce", "complaint", "campaign_started", "campaign_finished"}

type webhook struct {
	ID      int64    `json:"id"`
	BrandID int64    `json:"brand_id"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Active  bool     `json:"is_active"`
	Secret  string   `json:"secret,omitempty"`
}

func (a *API) registerWebhooks(m *http.ServeMux) {
	for _, p := range []string{"GET /api/v1/webhooks", "POST /api/v1/webhooks", "GET /api/v1/webhooks/{id}", "PATCH /api/v1/webhooks/{id}", "DELETE /api/v1/webhooks/{id}"} {
		a.route(m, p, a.webhooks)
	}
	a.documentWebhooks()
}

func validateWebhookEvents(events []string) error {
	if len(events) == 0 {
		return errors.New("at least one webhook event is required")
	}
	seen := map[string]bool{}
	for _, event := range events {
		valid := false
		for _, supported := range webhookEvents {
			if supported == event {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unsupported webhook event %q", event)
		}
		if seen[event] {
			return fmt.Errorf("duplicate webhook event %q", event)
		}
		seen[event] = true
	}
	return nil
}

func (a *API) readWebhook(r *http.Request, brand, id int64) (webhook, error) {
	var v webhook
	var raw string
	err := a.db.Reader.QueryRowContext(r.Context(), "SELECT id,brand_id,url,events,is_active FROM webhooks WHERE id=? AND brand_id=?", id, brand).Scan(&v.ID, &v.BrandID, &v.URL, &raw, &v.Active)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v.Events)
	}
	return v, err
}

func (a *API) webhooks(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	id := number(r.PathValue("id"))
	if r.PathValue("id") != "" && id <= 0 {
		fail(w, 400, errors.New("invalid webhook id"))
		return
	}
	if r.Method == http.MethodGet {
		if id > 0 {
			v, err := a.readWebhook(r, brand, id)
			result(w, v, err)
			return
		}
		after, limit := page(r)
		rows, err := a.db.Reader.QueryContext(r.Context(), "SELECT id,brand_id,url,events,is_active FROM webhooks WHERE brand_id=? AND id>? ORDER BY id LIMIT ?", brand, after, limit)
		if err != nil {
			result(w, nil, err)
			return
		}
		defer rows.Close()
		out := []webhook{}
		for rows.Next() {
			var v webhook
			var raw string
			if err = rows.Scan(&v.ID, &v.BrandID, &v.URL, &raw, &v.Active); err != nil {
				result(w, nil, err)
				return
			}
			if err = json.Unmarshal([]byte(raw), &v.Events); err != nil {
				result(w, nil, err)
				return
			}
			out = append(out, v)
		}
		result(w, out, rows.Err())
		return
	}
	if r.Method == http.MethodDelete {
		res, err := a.db.Writer.ExecContext(r.Context(), "DELETE FROM webhooks WHERE id=? AND brand_id=?", id, brand)
		result(w, map[string]bool{"deleted": true}, requireChanged(res, err))
		return
	}
	var in struct {
		URL          *string   `json:"url"`
		Events       *[]string `json:"events"`
		Active       *bool     `json:"is_active"`
		RotateSecret bool      `json:"rotate_secret"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.URL != nil {
		if err := jobs.ValidateWebhookURL(*in.URL); err != nil {
			fail(w, 400, err)
			return
		}
	}
	if in.Events != nil {
		if err := validateWebhookEvents(*in.Events); err != nil {
			fail(w, 400, err)
			return
		}
	}
	if r.Method == http.MethodPost {
		if in.URL == nil || in.Events == nil {
			fail(w, 400, errors.New("url and events are required"))
			return
		}
		if in.RotateSecret {
			fail(w, 400, errors.New("rotate_secret is only supported for updates"))
			return
		}
		v := webhook{BrandID: brand, URL: *in.URL, Events: *in.Events, Active: true, Secret: secret()}
		if in.Active != nil {
			v.Active = *in.Active
		}
		raw, _ := json.Marshal(v.Events)
		res, err := a.db.Writer.ExecContext(r.Context(), "INSERT INTO webhooks(brand_id,url,events,secret,is_active) VALUES(?,?,?,?,?)", brand, v.URL, string(raw), v.Secret, v.Active)
		if err == nil {
			v.ID, err = res.LastInsertId()
		}
		result(w, v, err)
		return
	}
	if in.URL == nil && in.Events == nil && in.Active == nil && !in.RotateSecret {
		fail(w, 400, errors.New("at least one update is required"))
		return
	}
	// Update only supplied columns to preserve concurrent configuration changes.
	query := "UPDATE webhooks SET "
	args := []any{}
	add := func(column string, value any) {
		if len(args) > 0 {
			query += ","
		}
		query += column + "=?"
		args = append(args, value)
	}
	if in.URL != nil {
		add("url", *in.URL)
	}
	if in.Events != nil {
		raw, _ := json.Marshal(*in.Events)
		add("events", string(raw))
	}
	if in.Active != nil {
		add("is_active", *in.Active)
	}
	rotated := ""
	if in.RotateSecret {
		rotated = secret()
		add("secret", rotated)
	}
	query += " WHERE id=? AND brand_id=?"
	args = append(args, id, brand)
	res, err := a.db.Writer.ExecContext(r.Context(), query, args...)
	err = requireChanged(res, err)
	if err != nil {
		result(w, nil, err)
		return
	}
	v, err := a.readWebhook(r, brand, id)
	v.Secret = rotated
	result(w, v, err)
}

func (a *API) documentWebhooks() {
	text := map[string]any{"type": "string"}
	events := map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": webhookEvents}}
	props := map[string]any{"id": map[string]any{"type": "integer"}, "brand_id": map[string]any{"type": "integer"}, "url": map[string]any{"type": "string", "format": "uri"}, "events": events, "is_active": map[string]any{"type": "boolean"}}
	redacted := map[string]any{"type": "object", "properties": props}
	for _, path := range []string{"/api/v1/webhooks", "/api/v1/webhooks/{id}"} {
		for method, raw := range a.paths[path].(map[string]any) {
			op := raw.(map[string]any)
			op["description"] = "Brand-scoped webhook configuration. Requires read permission for GET and write permission for mutations. Only public HTTP/HTTPS destinations are accepted; delivery checks DNS and pins connections to validated public addresses. Signing secrets are omitted from reads and ordinary updates; generated secrets are returned only at creation or explicit rotation."
			response := any(redacted)
			if method == "get" && path == "/api/v1/webhooks" {
				response = map[string]any{"type": "array", "items": redacted}
			}
			if method == "post" || method == "patch" {
				requestProps := map[string]any{"url": props["url"], "events": events, "is_active": props["is_active"]}
				required := []string{}
				if method == "post" {
					required = []string{"url", "events"}
				} else {
					requestProps["rotate_secret"] = map[string]any{"type": "boolean", "description": "Explicitly generate and return a new signing secret; previous secret stops being used."}
				}
				op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": requestProps, "required": required, "additionalProperties": false}}}}
				returned := map[string]any{}
				for k, v := range props {
					returned[k] = v
				}
				returned["secret"] = text
				response = map[string]any{"type": "object", "properties": returned}
			}
			if method == "delete" {
				response = map[string]any{"type": "object", "properties": map[string]any{"deleted": map[string]any{"type": "boolean"}}}
			}
			op["responses"].(map[string]any)["200"] = map[string]any{"description": "Successful operation", "content": map[string]any{"application/json": map[string]any{"schema": response}}}
		}
	}
}
