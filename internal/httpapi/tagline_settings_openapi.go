package httpapi

func taglineSettingsSchema() map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"enabled", "text"}, "additionalProperties": false,
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean", "default": true},
			"text":    map[string]any{"type": "string", "maxLength": 500, "default": defaultTagline, "description": "Plain text, rendered literally. Must not be blank when enabled."},
		},
	}
}

func documentTaglineSettings(method, path string, op map[string]any) {
	if path != "/api/v1/tagline-settings" {
		return
	}
	op["security"] = []map[string]any{{"cookieAuth": []string{}}}
	op["parameters"] = []map[string]any{}
	op["description"] = "Administrator session required. Read or replace the application-wide sign-in tagline. Defaults to enabled with the original text. Text is rendered literally, without HTML or Markdown formatting. Hiding the tagline preserves its text."
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Current tagline settings", "content": map[string]any{"application/json": map[string]any{"schema": taglineSettingsSchema()}}}
	if method == "PATCH" {
		op["parameters"] = []map[string]any{{"name": "X-CSRF-Token", "in": "header", "required": true, "schema": map[string]any{"type": "string"}}}
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": taglineSettingsSchema()}}}
	}
}
