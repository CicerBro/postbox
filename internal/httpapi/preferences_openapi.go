package httpapi

func documentPreferences(method, path string, op map[string]any) {
	if path != "/api/v1/auth/preferences" && path != "/api/v1/auth/session" {
		return
	}
	op["security"] = []map[string]any{{"cookieAuth": []string{}}}
	zone := map[string]any{"type": "string", "maxLength": 255, "description": "IANA timezone such as Europe/Amsterdam or UTC. Empty string uses the browser timezone.", "example": "Europe/Amsterdam"}
	if method == "PATCH" {
		op["description"] = "Save the signed-in user's display timezone across the app. Requires a user session and its CSRF token. API keys cannot update preferences. Dates remain stored as UTC timestamps."
		schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"timezone"}, "properties": map[string]any{"timezone": zone}}
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		op["responses"].(map[string]any)["200"] = map[string]any{"description": "Saved display preferences", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		return
	}
	op["parameters"] = []map[string]any{}
	op["description"] = "Read the signed-in user's session, authentication capabilities, and saved display timezone. API keys cannot read user sessions."
	props := map[string]any{"user_id": map[string]any{"type": "integer"}, "admin": map[string]any{"type": "boolean"}, "csrf_token": map[string]any{"type": "string"}, "totp_enabled": map[string]any{"type": "boolean"}, "passkeys_enabled": map[string]any{"type": "boolean"}, "passkeys_available": map[string]any{"type": "boolean"}, "passkey_count": map[string]any{"type": "integer"}, "timezone": zone}
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Current session", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"user_id", "admin", "csrf_token", "totp_enabled", "passkeys_enabled", "passkeys_available", "passkey_count", "timezone"}, "properties": props}}}}
}
