package httpapi

import "strings"

func (a *API) documentPasskeys() {
	for path, value := range a.paths {
		if !strings.HasPrefix(path, "/api/v1/auth/passkeys") {
			continue
		}
		for method, value := range value.(map[string]any) {
			op := value.(map[string]any)
			op["security"] = []map[string]any{{"cookieAuth": []string{}}}
			op["description"] = "WebAuthn with discoverable credentials and required user verification. Ceremonies expire after five minutes, are browser-bound, and are consumed once. Registration requires the current password, an enrolled TOTP code, a session, and CSRF. Removal requires the same factors and revokes sessions. Finish bodies are the browser PublicKeyCredential JSON (base64url binary fields)."
			props := map[string]any{}
			required := []string{}
			if strings.Contains(path, "/login/") {
				op["security"] = []any{}
				op["description"] = "Passwordless WebAuthn sign-in requires authenticator PIN or biometric verification. Begin returns publicKey options. Finish accepts PublicKeyCredential JSON and issues a session only after signature, challenge, RP, exact origin, UV, and ownership validation. The HttpOnly ceremony cookie binds the single-use five-minute challenge to this browser."
			}
			switch {
			case strings.HasSuffix(path, "login/begin"):
				props["turnstile_token"] = map[string]any{"type": "string", "maxLength": 2048, "description": "Fresh login Turnstile token required when Turnstile is configured"}
				op["description"] = op["description"].(string) + " When Turnstile is configured, begin requires a verified turnstile_token before issuing a ceremony."
			case strings.HasSuffix(path, "register/begin"):
				props["password"] = map[string]any{"type": "string"}
				props["code"] = map[string]any{"type": "string", "description": "Current TOTP code required if TOTP is enabled"}
				props["name"] = map[string]any{"type": "string", "maxLength": 100}
				required = []string{"password"}
			case method == "delete":
				props["password"] = map[string]any{"type": "string"}
				props["code"] = map[string]any{"type": "string", "description": "Current TOTP code required if TOTP is enabled"}
				required = []string{"password"}
			case strings.HasSuffix(path, "/finish"):
				props["id"] = map[string]any{"type": "string"}
				props["rawId"] = map[string]any{"type": "string", "format": "base64url"}
				props["type"] = map[string]any{"type": "string", "enum": []string{"public-key"}}
				props["response"] = map[string]any{"type": "object", "description": "Authenticator attestation or assertion response with base64url fields"}
				props["clientExtensionResults"] = map[string]any{"type": "object"}
				props["authenticatorAttachment"] = map[string]any{"type": "string", "nullable": true}
				required = []string{"id", "rawId", "type", "response"}
			}
			if len(props) > 0 {
				op["requestBody"] = map[string]any{"required": !strings.HasSuffix(path, "login/begin"), "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": props, "required": required}}}}
			} else if strings.HasSuffix(path, "login/begin") {
				delete(op, "requestBody")
			}
		}
	}
}
