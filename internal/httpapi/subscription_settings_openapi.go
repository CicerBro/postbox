package httpapi

func documentSubscriptionSettings(method, path string, op map[string]any) {
	if path != "/api/v1/subscription-settings" && path != "/api/v1/application-settings" {
		return
	}
	props := map[string]any{publicSubscriptionsKey: map[string]any{"type": "boolean", "default": true}}
	if path == "/api/v1/subscription-settings" {
		props["application_enabled"] = map[string]any{"type": "boolean"}
		props["enabled"] = map[string]any{"type": "boolean"}
		op["description"] = "Read or change the selected brand's public signup switch. Both application and brand switches default to enabled and must be enabled for hosted signup GET/POST and form generation. Confirmation and unsubscribe remain available. enabled is the effective application AND brand value."
	} else {
		op["description"] = "Administrator session required. Read or change the application-wide public signup switch; defaults to enabled. Disabling it blocks hosted signup and form generation for every brand, preserving confirmation and unsubscribe links."
	}
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Current subscription settings", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props}}}}
	if method == "PATCH" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{publicSubscriptionsKey}, "additionalProperties": false, "properties": map[string]any{publicSubscriptionsKey: map[string]any{"type": "boolean"}}}}}}
	}
}
