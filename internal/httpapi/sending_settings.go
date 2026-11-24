package httpapi

import (
	"fmt"
	"net/http"

	"postbox/internal/send"
)

func (a *API) sendingSettings(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodPatch {
		var in struct {
			GmailSendsPerMinute *int `json:"gmail_sends_per_minute"`
		}
		if !decode(w, r, &in) {
			return
		}
		rate := in.GmailSendsPerMinute
		if rate == nil || *rate < 0 || *rate > send.MaxGmailSendsPerMinute {
			fail(w, http.StatusBadRequest, fmt.Errorf("gmail_sends_per_minute must be an integer from 0 to %d", send.MaxGmailSendsPerMinute))
			return
		}
		if err := a.engine.SetGmailSendsPerMinute(ctx, *rate); err != nil {
			result(w, nil, err)
			return
		}
	}
	rate, err := a.engine.GmailSendsPerMinute(ctx)
	result(w, map[string]int{send.GmailSendsPerMinuteKey: rate}, err)
}

func documentSendingSettings(method, path string, op map[string]any) {
	if path != "/api/v1/sending-settings" {
		return
	}
	key := send.GmailSendsPerMinuteKey
	property := map[string]any{"type": "integer", "minimum": 0, "maximum": send.MaxGmailSendsPerMinute, "default": 0}
	schema := map[string]any{"type": "object", "required": []string{key}, "additionalProperties": false, "properties": map[string]any{key: property}}
	content := map[string]any{"application/json": map[string]any{"schema": schema}}
	op["description"] = "Administrator session required. Read or change the campaign-wide Gmail send cap across brands. Zero disables the Gmail cap. Changes apply to active campaigns without restarting Postbox. Paused campaigns use the current cap on resume."
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Current sending settings", "content": content}
	if method == http.MethodPatch {
		op["requestBody"] = map[string]any{"required": true, "content": content}
	}
}
