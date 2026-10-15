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
	op["tags"] = []string{apiTag(path)}
	documentSubscriberRead(method, path, op)
	documentCampaignReport(method, path, op)
	documentCampaignReads(method, path, op)
	if method == "DELETE" && path == "/api/v1/campaigns/{id}" {
		op["description"] = "Permanently delete a draft campaign with brand write permission. An administrator session can also delete sent or cancelled campaigns. Scheduled, preparing, sending and paused campaigns must be cancelled first. Deletes campaign delivery history and reports while retaining subscribers and lists."
		op["responses"].(map[string]any)["409"] = map[string]any{"description": "Cancel this campaign before deleting it"}
	}
	if path == "/api/v1/auth/config" || path == "/api/v1/auth/login" || strings.HasPrefix(path, "/subscribe/") || strings.HasPrefix(path, "/confirm/") {
		op["security"] = []any{}
	}
	if path == "/api/v1/auth/login" {
		op["description"] = "Verify email and password. When Turnstile is configured, every attempt requires a fresh turnstile_token from the login widget, verified before credentials. When TOTP is enabled and no code is supplied, returns 202 with totp_required=true without creating a session. Resubmit the credentials with the authenticator code to complete sign-in."
		op["responses"].(map[string]any)["202"] = map[string]any{"description": "Authenticator code required; no session has been created", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"totp_required"}, "properties": map[string]any{"totp_required": map[string]any{"type": "boolean", "enum": []bool{true}}}}}}}
	}
	if path == "/api/v1/auth/config" {
		op["parameters"] = []map[string]any{}
		op["description"] = "Public sign-in configuration. Returns turnstile_required, the public turnstile_site_key and the plain-text tagline settings; never returns the secret."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Sign-in configuration", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"turnstile_required", "turnstile_site_key", "tagline"}, "properties": map[string]any{"turnstile_required": map[string]any{"type": "boolean"}, "turnstile_site_key": map[string]any{"type": "string"}, "tagline": taglineSettingsSchema()}}}}}
	}
	if path == "/api/v1/openapi.json" {
		op["security"] = []map[string]any{{"cookieAuth": []string{}}}
		op["description"] = "Download the generated API specification. Requires an active user session; API keys do not grant documentation access."
		op["parameters"] = []map[string]any{}
	}
	if method == "GET" && path == "/api/v1/deployment-settings" {
		op["security"] = []map[string]any{{"cookieAuth": []string{}}}
		op["parameters"] = []map[string]any{}
		op["description"] = "Read safe deployment configuration managed through environment variables and startup flags. Requires an administrator session. Credentials and local paths are never returned."
		properties := map[string]any{}
		for _, name := range []string{"base_url", "recovery_policy"} {
			properties[name] = map[string]any{"type": "string"}
		}
		for _, name := range []string{"secure_cookies", "turnstile_configured", "backups_configured", "geoip_configured"} {
			properties[name] = map[string]any{"type": "boolean"}
		}
		for _, name := range []string{"session_ttl_seconds", "trusted_proxy_count", "raw_event_retention_days", "max_send_workers", "soft_bounce_limit"} {
			properties[name] = map[string]any{"type": "integer"}
		}
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Safe deployment configuration", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": properties}}}}
	}
	if method == "GET" && (path == "/api/v1/brands" || path == "/api/v1/brands/{brand}") {
		op["description"] = "Returns brand records with permissions as a JSON array for the current caller. Administrator sessions receive read, write and send; brand users receive their actual brand grants; API keys receive their scopes."
	}
	if method != "GET" && method != "DELETE" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": requestSchema(path)}}}
	}
	if method == "POST" && path == "/api/v1/campaigns/{id}/duplicate" {
		delete(op, "requestBody")
		op["description"] = "Copy the saved subject, HTML, text version, CSS setting, and audience targets of any campaign into a new draft. The new draft has its own ID and does not inherit schedule or delivery history."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "New draft campaign", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "integer"}}}}}}
	}
	if method == "POST" && path == "/api/v1/campaigns/{id}/reactivate" {
		delete(op, "requestBody")
		op["description"] = "Return a cancelled campaign to draft only when no emails were sent and no uncertain or active sends exist. Clears the schedule and prepared delivery data while preserving the campaign ID, saved content, owner, and audience targets."
		op["responses"].(map[string]any)["409"] = map[string]any{"description": "Campaign is not cancelled or has sent, uncertain, or active sends"}
	}
	if method != "GET" && path != "/api/v1/auth/login" && !strings.HasPrefix(path, "/subscribe/") && !strings.HasPrefix(path, "/confirm/") {
		op["parameters"] = append(parameters, map[string]any{"name": "X-CSRF-Token", "in": "header", "required": false, "description": "Required for mutations authenticated by the session cookie. Returned by login or session endpoint; Scalar adds it automatically for the signed-in user.", "schema": map[string]any{"type": "string"}})
	}
	if method == "GET" && path == "/api/v1/overview" {
		op["parameters"] = append(parameters, map[string]any{"name": "days", "in": "query", "description": "Campaign start comparison window in days, or all for the entire history without a previous-period comparison. Defaults to 30; numeric windows compare the preceding window of the same duration.", "schema": map[string]any{"default": 30, "oneOf": []any{map[string]any{"type": "integer", "minimum": 1, "maximum": 365}, map[string]any{"type": "string", "enum": []string{"all"}}}}})
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "timezone", "in": "query", "description": "IANA timezone for chart calendar days and hours. Defaults to UTC. Campaign KPI windows remain UTC.", "schema": map[string]any{"type": "string", "default": "UTC"}})
		op["description"] = "KPIs use campaign_stats attributed to started_at, falling back to finished_at then created_at for imported history. Rates use sent as denominator and are null when sent is zero. Changes are percentage points, except sent_percent (relative percentage, null with zero previous sent). All-time stats remain available. Growth contains 60 local calendar days including today, with explicit day/end instants. Older UTC snapshots are marked approximate. Hourly covers yesterday and today in the requested timezone. Hourly comparison uses 24 local clock-hour slots with explicit interval instants: repeated DST hours are combined and skipped hours have null interval instants. Missing and future hours have zero counts."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Brand overview counters and comparisons", "content": map[string]any{"application/json": map[string]any{"schema": overviewResponseSchema()}}}
	}

	if method == "GET" && path == "/api/v1/campaigns/{id}/checklist" {
		op["description"] = "Read-only pre-send checks preserve the estimate fields and return checks.ready with structured errors and warnings. Unknown merge tags are errors; missing recipient custom-field values without fallback are warnings. Recipients, inactive, suppressed, and skipped counts use the selected include/exclude audience. SES remaining daily quota and the campaign owner's optional remaining user quota are checked without reserving quota. HTML and MIME size estimates use sample recipient values; HTML clipping is warned at 102 KiB. At most 100 public HTTP/HTTPS destinations receive HEAD requests, with concurrency four and a three-second timeout each. DNS-pinned dialing rejects local, private, and metadata addresses; redirects are not followed and produce warnings. Personalized destinations use sample values and require real-recipient verification. This endpoint does not persist preparation, recipient snapshots, or reservations. Readiness describes this preview; runtime eligibility and quota are checked again when sending."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Estimate and structured pre-send checks, including validation failures", "content": map[string]any{"application/json": map[string]any{"schema": checklistResponseSchema()}}}
	}

	if strings.HasSuffix(path, "/events") {
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "SSE stream; stats event emitted once per second until disconnect, credential expiry or revocation, or loss of brand read permission", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}
	}
	if strings.HasSuffix(path, "/export") {
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Streamed keyset spreadsheet-safe CSV export. Formula prefixes (=, +, -, @ and fullwidth forms) after leading whitespace, controls or Unicode format characters receive an apostrophe. Reimports retain this apostrophe as data; use JSON or database backups for exact machine roundtrips. No raw CSV mode is exposed.", "content": map[string]any{"text/csv": map[string]any{"schema": map[string]any{"type": "string"}}}}
	}
	if strings.Contains(path, "/import") {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"text/csv": map[string]any{"schema": map[string]any{"type": "string"}}}}
		op["description"] = "Streams CSV in bounded transactions. Query options is JSON ImportOptions (mapping, list_ids, batch_size); preview performs no writes. The fields column accepts a JSON object or null (treated as empty); mapped fields.KEY columns override object keys regardless of column order. Invalid rows are reported with row numbers. Earlier committed batches remain if a later read or database batch fails; the pending batch is discarded."
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
		if method == "GET" && path == "/api/v1/suppressions" {
			op["parameters"] = append(op["parameters"].([]map[string]any),
				map[string]any{"name": "q", "in": "query", "description": "Case-insensitive literal substring search in the email/domain value or reason. Applied before pagination.", "schema": map[string]any{"type": "string"}},
				map[string]any{"name": "kind", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"email", "domain"}}},
				map[string]any{"name": "reason", "in": "query", "description": "Exact reason filter. Manual entries may include Sendy imports.", "schema": map[string]any{"type": "string", "enum": []string{"manual", "ses_sync", "hard_bounce", "soft_bounce_limit", "complaint"}}},
			)
		}
		if method == "PATCH" {
			op["description"] = op["description"].(string) + " PATCH preserves omitted kind, value, and reason fields; the rule stays in its existing scope and retains its creation time. Email rules require a plain address with a full domain; domain rules require only a domain. Editing applies the new rule to pending recipients without reactivating subscriber or delivery status."
			op["responses"].(map[string]any)["409"] = map[string]any{"description": "Another suppression already has this normalized kind and value in the selected scope"}
		}

	}
	documentListSettings(method, path, op)
	if path == "/api/v1/email-preview" {
		op["description"] = "Render current, unsaved email HTML, subject and text using the sending renderer. subscriber_id selects a real subscriber within the scoped brand; omit it or use zero for sample data. Empty custom fields use their fallback or an empty value. A scoped campaign_id produces encrypted, one-hour preview links using the selected subscriber or sample data. Browser links render the latest saved campaign edits; unsubscribe links simulate preferences without mutations. Unsaved campaigns use sample URLs. Does not create campaigns, tracking links, or send messages."
		op["responses"].(map[string]any)["200"] = map[string]any{
			"description": "Rendered sample HTML",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "properties": map[string]any{"html": map[string]any{"type": "string"}, "subject": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
				},
			},
		}
	}
	documentAWSSettings(method, path, op)
	documentAWSIdentity(method, path, op)
	documentSubscriptionSettings(method, path, op)
	documentTaglineSettings(method, path, op)
	documentJobSettings(method, path, op)
	documentPreferences(method, path, op)
	methods[strings.ToLower(method)] = op
}
func requestSchema(path string) map[string]any {
	props := map[string]any{}
	required := []string{}
	add := func(name, typ string) { props[name] = map[string]any{"type": typ} }
	switch {
	case path == "/api/v1/email-preview/recipients":
		props["after"] = map[string]any{"type": "integer", "minimum": 0}
		props["targets"] = requestSchema("/api/v1/campaigns/{id}/targets")
		required = []string{"targets"}
	case path == "/api/v1/email-preview":
		add("html", "string")
		add("subject", "string")
		add("text", "string")
		props["subscriber_id"] = map[string]any{"type": "integer", "minimum": 0}
		props["campaign_id"] = map[string]any{"type": "integer", "minimum": 0}
		add("inline_css", "boolean")
		required = []string{"html"}
	case strings.HasSuffix(path, "/memberships"):
		return map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"list_id", "status"}, "properties": map[string]any{"list_id": map[string]any{"type": "integer"}, "status": map[string]any{"type": "string", "enum": []string{"subscribed", "unsubscribed"}}}}}
	case strings.HasSuffix(path, "/settings"):
		for _, name := range []string{"goodbye_html", "thankyou_html"} {
			props[name] = map[string]any{"type": "string", "nullable": true, "description": "Null clears the custom brand page and restores the built-in version."}
		}
		for _, name := range []string{"goodbye_email", "thankyou_email", "confirmation_email"} {
			props[name] = map[string]any{"type": "object", "nullable": true, "description": "Null clears the custom brand email. Confirmation uses the built-in email; optional welcome and goodbye emails stop unless a list overrides them.", "required": []string{"subject", "html"}, "properties": map[string]any{"subject": map[string]any{"type": "string", "minLength": 1}, "html": map[string]any{"type": "string", "minLength": 1}, "text": map[string]any{"type": "string"}}}
		}
	case strings.Contains(path, "suppressions"):
		props["kind"] = map[string]any{"type": "string", "enum": []string{"email", "domain"}}
		props["value"] = map[string]any{"type": "string", "description": "A plain email address with a full domain for email rules, or a domain without @ for domain rules."}
		if strings.HasSuffix(path, "/{id}") {
			props["reason"] = map[string]any{"type": "string", "enum": []string{"hard_bounce", "soft_bounce_limit", "complaint", "manual", "ses_sync"}}
		} else {
			required = []string{"kind", "value"}
		}
	case strings.HasSuffix(path, "/login"):
		for _, s := range []string{"email", "password", "totp", "turnstile_token"} {
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
		add("text", "string")
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
	respond(w, 200, map[string]any{"openapi": "3.0.3", "info": map[string]string{"title": "Postbox API", "version": "1.0.0", "description": "JSON API with brand-scoped read/write bearer keys and authenticated CSRF-protected user sessions. Scalar uses your signed-in session and adds CSRF tokens to writes. Enter a bearer key to test brand-scoped permissions. User sessions need brand_id for brand-scoped endpoints. Keyset list responses are arrays; use the last row id as after. Timestamps are Unix milliseconds. Secrets are returned only when created. [Read the guides](/docs)."}, "servers": []map[string]string{{"url": "/", "description": "This Postbox instance"}}, "tags": apiTags(), "paths": a.paths, "components": map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]string{"type": "http", "scheme": "bearer", "description": "Brand-scoped API key"}, "cookieAuth": map[string]string{"type": "apiKey", "in": "cookie", "name": "postbox_session", "description": "Your active Postbox session. The browser sends this HttpOnly cookie automatically."}}}})
}

func apiTags() []map[string]string {
	return []map[string]string{
		{
			"name":        "Authentication and users",
			"description": "Sign in, manage your session and configure password, TOTP or passkey authentication. Administrators can manage users, brand assignments, permissions and per-user sending limits.",
		},
		{
			"name":        "Brands and settings",
			"description": "A brand keeps its sender identity, audiences, campaigns, templates and AWS connection together. Use these endpoints to manage brands, configure Amazon SES credentials and domain identities, set up delivery events and control public signup at brand or application level. Brand management and AWS configuration changes require an administrator session; each endpoint documents its access requirements.",
		},
		{
			"name":        "Campaigns",
			"description": "Create newsletter drafts, select an audience and preview personalised email content before sending. Run pre-send checks, send test messages, start or schedule delivery and follow campaign progress through reports and live event streams.",
		},
		{
			"name":        "Subscribers and audiences",
			"description": "Manage subscriber profiles, list memberships, targeting segments and delivery suppressions within a brand. Import or export contacts, review subscriber activity and configure list signup forms and opt-in behaviour. Subscription consent and delivery suppression are separate checks that both affect sending eligibility.",
		},
		{
			"name":        "Templates",
			"description": "Templates are reusable, named emails stored in the selected brand's library. List, create, update or delete them, then use their HTML and optional plain-text override as the starting point for a campaign. Creating or updating a template requires both name and html; saving a template does not send an email.",
		},
		{
			"name":        "API access",
			"description": "Create and revoke brand-scoped bearer keys for integrations, with read or write permissions. Copy a key's secret when it is created, as it is not returned again. The OpenAPI download requires a signed-in user session.",
		},
		{
			"name":        "Webhooks",
			"description": "Configure brand-scoped webhook destinations to notify external services about subscriptions, unsubscribes, bounces, complaints and campaign starts or finishes. Choose the events each destination receives and enable, update or remove it as your integration changes.",
		},
		{
			"name":        "Operations and reports",
			"description": "Read brand overview metrics, manage custom fields and inspect operational settings. Use maintenance endpoints to clean up old unconfirmed subscriptions, and review failed background jobs when troubleshooting processing or delivery.",
		},
		{
			"name":        "Public subscriptions",
			"description": "Hosted signup forms and confirmation links let subscribers join a list without a Postbox account. Signup requires both the application and brand public-subscription switches to be enabled; double opt-in memberships require confirmation before they become eligible for marketing.",
		},
	}
}

func apiTag(path string) string {
	if !strings.HasPrefix(path, "/api/v1/") {
		return "Public subscriptions"
	}
	resource := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")[0]
	switch resource {
	case "auth", "users":
		return "Authentication and users"
	case "brands", "aws-settings", "aws-identity", "subscription-settings", "application-settings", "job-settings":
		return "Brands and settings"
	case "campaigns", "email-preview":
		return "Campaigns"
	case "subscribers", "lists", "segments", "suppressions":
		return "Subscribers and audiences"
	case "templates":
		return "Templates"
	case "api-keys", "openapi.json":
		return "API access"
	case "webhooks":
		return "Webhooks"
	default:
		return "Operations and reports"
	}
}

func overviewResponseSchema() map[string]any {
	integer := map[string]any{"type": "integer", "format": "int64"}
	nullableInteger := map[string]any{"type": "integer", "format": "int64", "nullable": true}
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
		"period":  object(map[string]any{"days": nullableInteger, "basis": map[string]any{"type": "string", "enum": []string{"campaign_start"}}, "timezone": map[string]any{"type": "string", "enum": []string{"UTC"}}, "chart_timezone": map[string]any{"type": "string"}, "current_start": nullableInteger, "previous_start": nullableInteger, "end": integer}),
		"current": object(kpi), "previous": object(kpi), "change": object(change),
		"growth":            array(map[string]any{"day": integer, "end": integer, "active": integer, "subscribed": integer, "unsubscribed": integer, "approximate": map[string]any{"type": "boolean"}}),
		"hourly":            array(map[string]any{"hour": integer, "sent": integer}),
		"hourly_comparison": array(map[string]any{"hour": integer, "today": integer, "yesterday": integer, "today_start": nullableInteger, "today_end": nullableInteger, "yesterday_start": nullableInteger, "yesterday_end": nullableInteger}),
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
