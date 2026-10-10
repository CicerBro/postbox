package httpapi

func documentAWSSettings(method, path string, op map[string]any) {
	if path != "/api/v1/aws-settings" && path != "/api/v1/aws-settings/check" {
		return
	}
	text := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	number := map[string]any{"type": "number"}
	integer := map[string]any{"type": "integer", "format": "int64"}
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	}
	metadata := obj(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"static"}}, "region": text, "configured": boolean, "has_credentials": boolean, "has_session_token": boolean, "updated_at": integer})
	op["description"] = "Brand AWS settings expose metadata only. Each brand requires its own access keys before sending. Credentials are encrypted at rest and never returned. Reads require access to the selected brand. Writes require an administrator session and CSRF token. Connection checks require a user session with brand read access and CSRF token. Blank credential fields preserve saved values; clear_session_token explicitly removes a saved session token. DELETE removes the brand credentials and disables its AWS connection."
	op["parameters"] = []map[string]any{{"name": "brand_id", "in": "query", "required": true, "schema": map[string]any{"type": "integer", "minimum": 1}}}
	response := metadata
	if method != "GET" {
		op["security"] = []map[string]any{{"cookieAuth": []string{}}}
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "X-CSRF-Token", "in": "header", "required": true, "schema": text})
	}
	if method == "PATCH" {
		secret := func(max int) map[string]any {
			return map[string]any{"type": "string", "writeOnly": true, "maxLength": max, "description": "Blank preserves the saved value."}
		}
		schema := obj(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"static"}}, "region": map[string]any{"type": "string", "maxLength": 64}, "access_key_id": secret(128), "secret_access_key": secret(256), "session_token": secret(8192), "clear_session_token": boolean})
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	if path == "/api/v1/aws-settings/check" {
		delete(op, "requestBody")
		op["description"] = op["description"].(string) + " Performs bounded read-only STS/SES account and identity requests without sending mail. Limited to six checks per minute per user and brand."
		identity := obj(map[string]any{"identity": text, "identity_type": text, "sending_enabled": boolean, "verification_status": text})
		response = obj(map[string]any{"account_id": text, "region": text, "sending_enabled": boolean, "production_access_enabled": boolean, "enforcement_status": text, "checked_at": integer, "quota": obj(map[string]any{"max_send_rate": number, "max_24_hour_send": number, "sent_last_24_hours": number}), "identities": map[string]any{"type": "array", "items": identity}})
		op["responses"].(map[string]any)["502"] = map[string]any{"description": "AWS check failed; credential values and raw upstream errors are never exposed"}
	}
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "AWS metadata without credential values", "content": map[string]any{"application/json": map[string]any{"schema": response}}}
	op["responses"].(map[string]any)["503"] = map[string]any{"description": "Brand AWS configuration unavailable"}
}
