package httpapi

func documentCampaignReport(method, path string, op map[string]any) {
	if method != "GET" || path != "/api/v1/campaigns/{id}/report" {
		return
	}
	// Report data is bounded by the campaign, not a keyset page.
	params := op["parameters"].([]map[string]any)
	kept := []map[string]any{}
	for _, param := range params {
		if param["name"] != "after" && param["name"] != "limit" {
			kept = append(kept, param)
		}
	}
	op["parameters"] = kept
	op["description"] = "Campaign counters, link metrics, country and hourly rollups. Recent contains up to 10 distinct campaign recipients per opened, unsubscribed, bounced and complained category, ordered by durable event timestamp descending then subscriber ID descending. Opened timestamps are the first open, including machine opens. Recipient email and name prefer the send snapshot. Timestamps are Unix milliseconds; samples survive raw-event retention. Historical imports use the campaign started_at as first_sent_at when recipient timestamps are unavailable. historical identifies imported campaigns; delivered_estimated identifies imports without an explicit archived delivery counter. Delivery contains the first and last recipient sent_at and the preparation sender when present, otherwise the current brand sender with sender_snapshotted=false. Target names reflect current lists and segments and are null when removed."
	integer := map[string]any{"type": "integer"}
	text := map[string]any{"type": "string"}
	nullableTime := map[string]any{"type": "integer", "nullable": true, "description": "Unix milliseconds"}
	recentItem := map[string]any{"type": "object", "required": []string{"id", "email", "name", "at"}, "properties": map[string]any{"id": integer, "email": text, "name": text, "at": integer}}
	recent := map[string]any{}
	for _, event := range campaignRecentEvents {
		recent[event.name] = map[string]any{"type": "array", "maxItems": 10, "items": recentItem}
	}
	properties := map[string]any{}
	for _, key := range []string{"stats", "links", "countries", "hourly"} {
		properties[key] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	}
	properties["recent"] = map[string]any{"type": "object", "required": []string{"opened", "unsubscribed", "bounced", "complained"}, "properties": recent}
	properties["delivery"] = map[string]any{"type": "object", "properties": map[string]any{"first_sent_at": nullableTime, "last_sent_at": nullableTime, "from_name": text, "from_email": text, "sender_snapshotted": map[string]any{"type": "boolean"}, "historical": map[string]any{"type": "boolean"}, "delivered_estimated": map[string]any{"type": "boolean"}}}
	properties["targets"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"target_type": map[string]any{"type": "string", "enum": []string{"list", "segment"}}, "target_id": integer, "mode": map[string]any{"type": "string", "enum": []string{"include", "exclude"}}, "name": map[string]any{"type": "string", "nullable": true}}}}
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Campaign report", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": properties}}}}
}
