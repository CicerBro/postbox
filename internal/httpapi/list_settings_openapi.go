package httpapi

func documentListSettings(method, path string, op map[string]any) {
	if path != "/api/v1/lists/{id}/settings" {
		return
	}
	props := map[string]any{}
	for _, name := range listSettingNames {
		schema := map[string]any{"type": "string", "nullable": true, "description": "Null or an empty string clears the list override."}
		if name == "confirmation_email" || name == "thankyou_email" || name == "goodbye_email" {
			schema = map[string]any{"type": "object", "nullable": true, "additionalProperties": false, "required": []string{"subject", "html"}, "properties": map[string]any{"subject": map[string]any{"type": "string", "minLength": 1}, "html": map[string]any{"type": "string", "minLength": 1}, "text": map[string]any{"type": "string"}}, "description": "Null clears the list override."}
		}
		props[name] = schema
	}
	settings := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	op["description"] = "List notification overrides, scoped to the selected brand. Omitted PATCH fields remain unchanged. Null clears an override and restores the brand default. Confirmation email supports {{ confirmation_url }}. confirmation_page_html is the hosted pending-confirmation page. Thank-you email requires confirmed list consent; notification customization does not change consent or opt-in. Responses contain explicit overrides and effective values; missing effective values use built-in page/confirmation defaults or disable optional emails."
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "List overrides and inherited settings", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"overrides", "effective"}, "properties": map[string]any{"overrides": settings, "effective": settings}}}}}
	if method == "PATCH" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": settings}}}
	}
}
