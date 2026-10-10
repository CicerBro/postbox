package httpapi

func documentSubscriberRead(method, path string, op map[string]any) {
	if method == "POST" && path == "/api/v1/subscribers/{id}/unsubscribe" {
		op["description"] = "Set list_id to opt out of one list only. Omit list_id or set it to zero to persist a brand-wide marketing opt-out. Brand-wide opt-out blocks list and segment campaigns, including prepared recipients, until an explicit consent-based resubscription. It does not alter other brands."
	}
	if (method == "POST" && path == "/api/v1/subscribers") || (method == "PATCH" && path == "/api/v1/subscribers/{id}") {
		op["description"] = "Profile and membership updates do not clear a brand-wide marketing opt-out. Explicit resubscribe=true requires list_ids and fresh consent (consent=true or a consent timestamp later than the opt-out). Single opt-in may clear the brand opt-out after suppression checks; double opt-in requires fresh confirmation before marketing eligibility."
	}
	if method != "GET" {
		return
	}
	if path == "/api/v1/subscribers/{id}" {
		op["description"] = "Returns a one-element subscriber array. For compatibility, fields is the raw JSON string; marketing_unsubscribed_at is a timestamp or null."
	}
	if path == "/api/v1/subscribers/by-email" {
		op["description"] = "Returns a subscriber object with decoded fields and marketing_unsubscribed_at when brand-wide opt-out is set."
	}
	if path == "/api/v1/lists" || path == "/api/v1/lists/{id}" {
		op["description"] = "Returns an array of list rows including top-level active, subscribed, unsubscribed, bounced and complained counters. Counters come from durable list_counters; missing counters return zero. Subscribed and unsubscribed are cumulative transitions, not current membership totals."
	}
	if path != "/api/v1/subscribers" {
		return
	}
	params := op["parameters"].([]map[string]any)
	params = append(params,
		map[string]any{"name": "q", "in": "query", "description": "ASCII email prefix, trimmed and lowercased. Matches literal characters, including % and _. Uses an indexed binary range across the entire brand before pagination.", "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "list_id", "in": "query", "description": "Restrict to members of a list in this brand, including all membership states unless membership_status is supplied. Unknown or other-brand lists return 404.", "schema": map[string]any{"type": "integer", "minimum": 1}},
		map[string]any{"name": "status", "in": "query", "description": "Global subscriber delivery status.", "schema": map[string]any{"type": "string", "enum": []string{"active", "bounced", "complained", "blocklisted"}}},
		map[string]any{"name": "membership_status", "in": "query", "description": "List membership state; requires list_id.", "schema": map[string]any{"type": "string", "enum": []string{"subscribed", "unsubscribed", "unconfirmed"}}},
		map[string]any{"name": "sort", "in": "query", "description": "Sort column, with subscriber ID as the tie breaker. Missing consent timestamps sort as zero.", "schema": map[string]any{"type": "string", "default": "id", "enum": []string{"id", "email", "name", "status", "gdpr_consent_at", "created_at"}}},
		map[string]any{"name": "direction", "in": "query", "description": "Direction for both the sort value and ID tie breaker.", "schema": map[string]any{"type": "string", "default": "asc", "enum": []string{"asc", "desc"}}},
	)
	op["parameters"] = params
	op["description"] = "Returns subscriber rows, with fields decoded as JSON objects. Defaults to ascending subscriber ID. Filters apply before keyset pagination. Continue with after equal to the last returned ID and preserve the same filters, sort and direction. For custom sorting, after resolves the anchor's current sort value inside this brand; a deleted or other-brand anchor returns an empty array, and an edited anchor moves the pagination boundary. Concurrent edits can therefore skip or repeat rows across pages. Empty results are an empty array."
}
