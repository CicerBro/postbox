package httpapi

func documentCampaignReads(method, path string, op map[string]any) {
	if method != "GET" {
		return
	}
	integer := map[string]any{"type": "integer"}
	text := map[string]any{"type": "string"}
	if path == "/api/v1/campaigns/options" {
		op["description"] = "Campaign ID and subject options within the selected brand. Uses ID keyset pagination and omits all message content."
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Campaign choices", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"id", "subject"}, "additionalProperties": false, "properties": map[string]any{"id": integer, "subject": text}}}}}}
	}
	if path == "/api/v1/campaigns" {
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "summary", "in": "query", "schema": map[string]any{"type": "boolean", "default": false}, "description": "Return only id, subject, status, created_at, scheduled_at and a plain-text excerpt of up to 110 Unicode characters. Omits HTML and plain-text message content."})
	}
}
