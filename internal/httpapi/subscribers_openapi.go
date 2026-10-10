package httpapi

func documentSubscriberRead(method, path string, op map[string]any) {
	if method != "GET" {
		return
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
	)
	op["parameters"] = params
	op["description"] = "Returns subscriber rows, with fields decoded as JSON objects, ordered by ascending subscriber ID. Filters apply before keyset pagination. Continue with after equal to the last returned ID and preserve the same filters. Empty results are an empty array."
}
