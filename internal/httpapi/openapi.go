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
	documentSubscriberRead(method, path, op)
	if path == "/api/v1/auth/login" || path == "/api/v1/openapi.json" || strings.HasPrefix(path, "/subscribe/") || strings.HasPrefix(path, "/confirm/") {
		op["security"] = []any{}
	}
	if method != "GET" && method != "DELETE" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": requestSchema(path)}}}
		parameters = append(parameters, map[string]any{"name": "X-CSRF-Token", "in": "header", "required": false, "description": "Required for all mutations authenticated by the session cookie. Returned by login or session endpoint.", "schema": map[string]any{"type": "string"}})
		op["parameters"] = parameters
	}
	if method == "GET" && path == "/api/v1/overview" {
		op["parameters"] = append(parameters, map[string]any{"name": "days", "in": "query", "description": "Campaign start comparison window in days. Defaults to 30; preceding window has the same duration.", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "default": 30}})
		op["description"] = "KPIs use campaign_stats attributed to started_at, falling back to finished_at then created_at for imported history. Rates use sent as denominator and are null when sent is zero. Changes are percentage points, except sent_percent (relative percentage, null with zero previous sent). All-time stats remain available. Growth contains 60 UTC calendar days including today. Hourly contains 48 UTC calendar buckets for yesterday and today, with zero-filled missing buckets including future hours today."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Brand overview counters and comparisons", "content": map[string]any{"application/json": map[string]any{"schema": overviewResponseSchema()}}}
	}

	if method == "GET" && path == "/api/v1/campaigns/{id}/checklist" {
		op["description"] = "Read-only pre-send checks preserve the estimate fields and return checks.ready with structured errors and warnings. Unknown merge tags are errors; missing recipient custom-field values without fallback are warnings. Recipients, inactive, suppressed, and skipped counts use the selected include/exclude audience. SES remaining daily quota and the campaign owner's optional remaining user quota are checked without reserving quota. HTML and MIME size estimates use sample recipient values; HTML clipping is warned at 102 KiB. At most 100 public HTTP/HTTPS destinations receive HEAD requests, with concurrency four and a three-second timeout each. DNS-pinned dialing rejects local, private, and metadata addresses; redirects are not followed and produce warnings. Personalized destinations use sample values and require real-recipient verification. This endpoint does not persist preparation, recipient snapshots, or reservations. Readiness describes this preview; runtime eligibility and quota are checked again when sending."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Estimate and structured pre-send checks, including validation failures", "content": map[string]any{"application/json": map[string]any{"schema": checklistResponseSchema()}}}
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
	if strings.Contains(path, "/suppressions") {
		scopes := []string{"brand", "global"}
		defaultScope := "brand"
		if method == "GET" {
			scopes = append(scopes, "effective")
			defaultScope = "effective"
		}
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "scope", "in": "query", "schema": map[string]any{"type": "string", "enum": scopes, "default": defaultScope}})
		op["description"] = "Brand scope returns or modifies only the selected brand's suppressions. Global scope requires an administrator session. GET defaults to effective: selected-brand rules plus global rules. For brand users and API keys, global rules are returned only when their email or domain matches a subscriber already in that brand. Administrator sessions see all global rules. Sending always applies all global rules, including to future subscribers."
		if method == "PATCH" {
			op["description"] = op["description"].(string) + " PATCH preserves omitted kind, value, and reason fields; the rule stays in its existing scope and retains its creation time. Email rules require a plain address with a full domain; domain rules require only a domain. Editing applies the new rule to pending recipients without reactivating subscriber or delivery status."
			op["responses"].(map[string]any)["409"] = map[string]any{"description": "Another suppression already has this normalized kind and value in the selected scope"}
		}

	}
	documentListSettings(method, path, op)
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
		props["kind"] = map[string]any{"type": "string", "enum": []string{"email", "domain"}}
		props["value"] = map[string]any{"type": "string", "description": "A plain email address with a full domain for email rules, or a domain without @ for domain rules."}
		if strings.HasSuffix(path, "/{id}") {
			props["reason"] = map[string]any{"type": "string", "enum": []string{"hard_bounce", "soft_bounce_limit", "complaint", "manual", "ses_sync"}}
		} else {
			required = []string{"kind", "value"}
		}
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

func overviewResponseSchema() map[string]any {
	integer := map[string]any{"type": "integer", "format": "int64"}
	percent := map[string]any{"type": "number", "nullable": true}
	kpi := map[string]any{}
	for _, name := range []string{"sent", "delivered", "opened", "clicked", "bounced", "complaints"} {
		kpi[name] = integer
	}
	for _, name := range []string{"delivered_percent", "opened_percent", "clicked_percent", "bounced_percent", "complaints_percent"} {
		kpi[name] = percent
	}
	change := map[string]any{}
	for _, name := range []string{"sent_percent", "delivered_points", "opened_points", "clicked_points", "bounced_points", "complaints_points"} {
		change[name] = percent
	}
	object := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	array := func(props map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": object(props)}
	}
	return object(map[string]any{
		"stats":   array(map[string]any{"sent": integer, "delivered": integer, "opened": integer, "clicked": integer, "bounced": integer, "complaints": integer}),
		"period":  object(map[string]any{"days": integer, "basis": map[string]any{"type": "string", "enum": []string{"campaign_start"}}, "timezone": map[string]any{"type": "string", "enum": []string{"UTC"}}, "current_start": integer, "previous_start": integer, "end": integer}),
		"current": object(kpi), "previous": object(kpi), "change": object(change),
		"growth":            array(map[string]any{"day": integer, "active": integer, "subscribed": integer, "unsubscribed": integer}),
		"hourly":            array(map[string]any{"hour": integer, "sent": integer}),
		"hourly_comparison": array(map[string]any{"hour": integer, "today": integer, "yesterday": integer}),
	})
}

func checklistResponseSchema() map[string]any {
	integer := map[string]any{"type": "integer", "format": "int64"}
	number := map[string]any{"type": "number"}
	boolean := map[string]any{"type": "boolean"}
	text := map[string]any{"type": "string"}
	issue := map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": text, "message": text, "tag": text}}
	link := map[string]any{"type": "object", "required": []string{"url", "status", "personalized"}, "properties": map[string]any{
		"url": text, "status": map[string]any{"type": "string", "enum": []string{"ok", "broken", "blocked", "warning"}}, "http_status": integer, "message": text, "personalized": boolean,
	}}
	checks := map[string]any{"type": "object", "required": []string{"ready", "errors", "warnings", "links", "html_bytes", "html_clipping", "suppressed", "inactive", "skipped", "remaining_daily_quota", "sample_recipient"}, "properties": map[string]any{
		"ready":  map[string]any{"type": "boolean", "description": "False when any blocking error is present. Warnings require review but do not block readiness."},
		"errors": map[string]any{"type": "array", "items": issue}, "warnings": map[string]any{"type": "array", "items": issue}, "links": map[string]any{"type": "array", "maxItems": 100, "items": link},
		"html_bytes": map[string]any{"type": "integer", "format": "int64", "description": "Sample rendered HTML bytes before MIME encoding, excluding MIME headers and text alternative."}, "html_clipping": boolean,
		"suppressed": integer, "inactive": integer, "skipped": integer, "remaining_daily_quota": number,
		"user_remaining_daily_quota": map[string]any{"type": "integer", "format": "int64", "description": "Remaining daily recipient budget for a limited non-admin campaign owner; omitted for unlimited owners."},
		"sample_recipient":           map[string]any{"type": "boolean", "description": "True when rendering and personalized link resolution use synthetic sample recipient values."},
	}}
	quota := map[string]any{"type": "object", "required": []string{"max_send_rate", "max_24_hour_send", "sent_last_24_hours"}, "properties": map[string]any{"max_send_rate": number, "max_24_hour_send": number, "sent_last_24_hours": number}}
	return map[string]any{"type": "object", "required": []string{"recipients", "message_bytes", "upload_bytes", "duration_seconds", "quota", "fits_daily_quota", "checks"}, "properties": map[string]any{
		"recipients": integer, "message_bytes": integer, "upload_bytes": integer, "duration_seconds": number, "quota": quota, "fits_daily_quota": boolean, "checks": checks,
	}}
}
