package httpapi

import (
	"fmt"
	"net/http"
	"strings"
)

func (a *API) documentRoute(pattern string) {
	method, path, _ := strings.Cut(pattern, " ")
	methods, ok := a.paths[path].(map[string]any)
	if !ok {
		methods = map[string]any{}
		a.paths[path] = methods
	}
	parameters := []map[string]any{}
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") {
			name := strings.Trim(part, "{}")
			typ := "integer"
			if name == "uuid" || name == "token" {
				typ = "string"
			}
			parameters = append(parameters, map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]any{"type": typ}})
		}
	}
	if !strings.Contains(path, "auth/") && !strings.Contains(path, "openapi") && !strings.HasPrefix(path, "/subscribe/") && !strings.HasPrefix(path, "/confirm/") {
		parameters = append(parameters, map[string]any{"name": "brand_id", "in": "query", "required": false, "description": "Brand scope. Required for user sessions unless identifying a brand path. API keys default to their own brand.", "schema": map[string]any{"type": "integer", "minimum": 1}})
	}
	if method == "GET" {
		for _, name := range []string{"after", "limit"} {
			parameters = append(parameters, map[string]any{"name": name, "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0}, "description": "Keyset pagination; limit defaults to 100 and is capped at 500."})
		}
	}
	op := map[string]any{"operationId": strings.ToLower(method) + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(path), "summary": method + " " + path, "parameters": parameters, "security": []map[string]any{{"bearerAuth": []string{}}, {"cookieAuth": []string{}}}, "responses": map[string]any{"200": map[string]any{"description": "Successful operation", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{}}}}, "400": map[string]any{"description": "Invalid request; JSON error contains actionable message"}, "401": map[string]any{"description": "Authentication required"}, "403": map[string]any{"description": "Brand, scope, or CSRF access denied"}, "404": map[string]any{"description": "Resource not found"}, "429": map[string]any{"description": "Rate limit exceeded"}}}
	if path == "/api/v1/auth/login" || path == "/api/v1/openapi.json" || strings.HasPrefix(path, "/subscribe/") || strings.HasPrefix(path, "/confirm/") {
		op["security"] = []any{}
	}
	if method != "GET" && method != "DELETE" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": requestSchema(path)}}}
		parameters = append(parameters, map[string]any{"name": "X-CSRF-Token", "in": "header", "required": false, "description": "Required for all mutations authenticated by the session cookie. Returned by login or session endpoint.", "schema": map[string]any{"type": "string"}})
		op["parameters"] = parameters
	}
	if strings.HasSuffix(path, "/events") {
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "SSE stream; stats event emitted once per second until disconnect", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}
	}
	if strings.HasSuffix(path, "/export") {
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Streamed keyset CSV export", "content": map[string]any{"text/csv": map[string]any{"schema": map[string]any{"type": "string"}}}}
	}
	if strings.Contains(path, "/import") {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"text/csv": map[string]any{"schema": map[string]any{"type": "string"}}}}
		op["description"] = "Streams CSV in bounded transactions. Query options is JSON ImportOptions (mapping, list_ids, batch_size); preview performs no writes."
	}
	if strings.HasPrefix(path, "/subscribe/") && method == "POST" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/x-www-form-urlencoded": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"email", "consent"}, "properties": map[string]any{"email": map[string]any{"type": "string", "format": "email"}, "name": map[string]any{"type": "string"}, "website": map[string]any{"type": "string", "description": "Honeypot, leave empty"}, "consent": map[string]any{"type": "string"}, "cf-turnstile-response": map[string]any{"type": "string"}}}}}}
	}
	methods[strings.ToLower(method)] = op
}
func requestSchema(path string) map[string]any {
	props := map[string]any{}
	required := []string{}
	add := func(name, typ string) { props[name] = map[string]any{"type": typ} }
	switch {
	case strings.HasSuffix(path, "/memberships"):
		return map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"list_id", "status"}, "properties": map[string]any{"list_id": map[string]any{"type": "integer"}, "status": map[string]any{"type": "string", "enum": []string{"subscribed", "unsubscribed"}}}}}
	case strings.HasSuffix(path, "/settings"):
		add("goodbye_html", "string")
		add("thankyou_html", "string")
		add("goodbye_email", "object")
		add("confirmation_email", "object")
	case strings.Contains(path, "suppressions"):
		add("kind", "string")
		add("value", "string")
		required = []string{"kind", "value"}
	case strings.HasSuffix(path, "/login"):
		for _, s := range []string{"email", "password", "totp"} {
			add(s, "string")
		}
		required = []string{"email", "password"}
	case strings.Contains(path, "subscribers"):
		for _, s := range []string{"email", "name", "status"} {
			add(s, "string")
		}
		add("fields", "object")
		add("consent", "boolean")
		add("resubscribe", "boolean")
		props["list_ids"] = map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
		if strings.HasSuffix(path, "bulk") {
			return map[string]any{"type": "array", "maxItems": 1000, "items": map[string]any{"type": "object", "properties": props}}
		}
		if strings.Contains(path, "mass-") {
			return map[string]any{"type": "object", "properties": map[string]any{"emails": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "list_id": map[string]any{"type": "integer"}}, "required": []string{"emails"}}
		}
		add("list_id", "integer")
	case strings.HasSuffix(path, "targets"):
		return map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"target_type", "target_id", "mode"}, "properties": map[string]any{"target_type": map[string]any{"type": "string", "enum": []string{"list", "segment"}}, "target_id": map[string]any{"type": "integer"}, "mode": map[string]any{"type": "string", "enum": []string{"include", "exclude"}}}}}
	case strings.HasSuffix(path, "test-send"):
		props["emails"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case strings.HasSuffix(path, "schedule"):
		add("scheduled_at", "integer")
		add("timezone", "string")
		required = []string{"scheduled_at"}
	case strings.Contains(path, "campaigns"):
		add("subject", "string")
		add("html", "string")
		add("text", "string")
		add("inline_css", "boolean")
	case strings.Contains(path, "api-keys"):
		add("name", "string")
		props["scopes"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"read", "write"}}}
		required = []string{"name", "scopes"}
	case strings.Contains(path, "brands"):
		for _, s := range []string{"name", "from_name", "from_email", "reply_to", "configuration_set", "tracking_domain"} {
			add(s, "string")
		}
		required = []string{"name", "from_email"}
	case strings.Contains(path, "lists"):
		add("name", "string")
		props["opt_in"] = map[string]any{"type": "string", "enum": []string{"single", "double"}}
	case strings.Contains(path, "templates"):
		add("name", "string")
		add("html", "string")
	case strings.Contains(path, "custom-fields"):
		for _, s := range []string{"key", "label", "type"} {
			add(s, "string")
		}
		add("is_filterable", "boolean")
	case strings.Contains(path, "segments"):
		add("name", "string")
		add("rules", "object")
	case strings.HasSuffix(path, "sending"):
		add("enabled", "boolean")
	case strings.Contains(path, "unconfirmed"):
		add("older_than", "integer")
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "description": fmt.Sprintf("Request for %s", path)}
}
func (a *API) openapi(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, map[string]any{"openapi": "3.0.3", "info": map[string]string{"title": "Postbox API", "version": "1.0.0", "description": "JSON API with brand-scoped read/write bearer keys and authenticated CSRF-protected user sessions. Keyset list responses are arrays; use the last row id as after. Timestamps are Unix milliseconds. Secrets are returned only when created."}, "paths": a.paths, "components": map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"}, "cookieAuth": map[string]string{"type": "apiKey", "in": "cookie", "name": "postbox_session"}}}})
}
