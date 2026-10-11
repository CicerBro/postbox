package httpapi

func documentAWSIdentity(method, path string, op map[string]any) {
	if path != "/api/v1/aws-settings/identity" && path != "/api/v1/aws-settings/identity/mail-from" {
		return
	}
	text := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	domain := map[string]any{"type": "string", "maxLength": 253, "description": "Domain identity, such as example.com. Email addresses and identity ARNs are not accepted."}
	op["security"] = []map[string]any{{"cookieAuth": []string{}}}
	op["parameters"] = []map[string]any{{"name": "brand_id", "in": "query", "required": true, "schema": map[string]any{"type": "integer", "minimum": 1}}}
	op["description"] = "Manage an SES domain identity using the selected brand's saved AWS account and region. GET requires a user session with brand read access. Writes require an administrator session and CSRF. Requests are bounded and limited to six per minute per user, brand, and operation. DNS records must be published at the DNS provider. Raw AWS errors and credentials are never returned."
	if method == "GET" {
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "identity", "in": "query", "required": true, "schema": domain})
	} else {
		op["parameters"] = append(op["parameters"].([]map[string]any), map[string]any{"name": "X-CSRF-Token", "in": "header", "required": true, "schema": text})
		props := map[string]any{"identity": domain}
		required := []string{"identity"}
		if method == "PATCH" {
			props["mail_from_domain"] = map[string]any{"type": "string", "maxLength": 253, "description": "Subdomain of the identity. An empty string disables custom MAIL FROM."}
			props["mail_from_behavior"] = map[string]any{"type": "string", "enum": []string{"USE_DEFAULT_VALUE", "REJECT_MESSAGE"}}
			required = append(required, "mail_from_domain", "mail_from_behavior")
		}
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}}}
	}
	props := map[string]any{
		"identity": text, "identity_type": text, "region": text, "custom_endpoint": boolean,
		"sending_enabled": boolean, "verification_status": text, "dkim_status": text,
		"dkim_tokens": map[string]any{"type": "array", "items": text}, "dkim_origin": text,
		"dkim_hosted_zone": text, "dkim_signing_enabled": boolean,
		"mail_from_domain": text, "mail_from_status": text, "mail_from_behavior": text,
		"checked_at":  map[string]any{"type": "integer", "format": "int64"},
		"dns_records": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"type": text, "name": text, "value": text, "priority": map[string]any{"type": "integer"}}, "required": []string{"type", "name", "value"}, "additionalProperties": false}},
	}
	if method == "POST" {
		op["description"] = op["description"].(string) + " Creates an identity with Easy DKIM. If it already exists, returns its current settings without changing DKIM configuration."
	}
	if method == "PATCH" {
		props = map[string]any{"configured": boolean}
		op["description"] = op["description"].(string) + " Updates or disables custom MAIL FROM. Refresh the identity separately to read current DNS records and AWS status."
	}
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "SES identity metadata or MAIL FROM setup result", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props, "additionalProperties": false}}}}
	op["responses"].(map[string]any)["502"] = map[string]any{"description": "AWS request failed; check brand credentials, region and SES identity permissions"}
	op["responses"].(map[string]any)["503"] = map[string]any{"description": "Brand AWS configuration unavailable"}
}
