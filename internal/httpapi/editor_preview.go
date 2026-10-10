package httpapi

import (
	"errors"
	"net/http"
	"postbox/internal/render"
)

func (a *API) editorPreview(w http.ResponseWriter, r *http.Request) {
	brand, ok := a.scoped(w, r)
	if !ok {
		return
	}
	var input struct {
		Subject      string `json:"subject"`
		Text         string `json:"text"`
		SubscriberID int64  `json:"subscriber_id"`
		CampaignID   int64  `json:"campaign_id"`
		HTML         string `json:"html"`
		InlineCSS    bool   `json:"inline_css"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.SubscriberID < 0 || input.CampaignID < 0 {
		fail(w, 400, errors.New("subscriber_id and campaign_id must be zero or a positive integer"))
		return
	}
	if input.CampaignID > 0 {
		var exists int
		if err := a.db.Reader.QueryRowContext(r.Context(), `SELECT 1 FROM campaigns WHERE id=? AND brand_id=?`, input.CampaignID, brand).Scan(&exists); err != nil {
			result(w, nil, err)
			return
		}
	}
	subject, body, text, err := a.engine.EditorPreview(r.Context(), brand, input.SubscriberID, render.Options{CampaignID: input.CampaignID, Subject: input.Subject, HTML: input.HTML, Text: input.Text, InlineCSS: input.InlineCSS})
	result(w, map[string]string{"subject": subject, "html": body, "text": text}, err)
}
