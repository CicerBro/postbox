package send

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/render"
	"postbox/internal/tracking/token"
	"strconv"
	"strings"
)

// EditorPreview renders unsaved source without creating campaigns or tracking links.
// A scoped, saved campaign and real subscriber enable recipient action URLs.
func (e *Engine) EditorPreview(ctx context.Context, brandID, subscriberID int64, options render.Options) (string, string, string, error) {
	var base string
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT coalesce(tracking_domain,'') FROM brands WHERE id=?`, brandID).Scan(&base); err != nil {
		return "", "", "", err
	}
	if base == "" {
		base = e.cfg.BaseURL
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	rows, err := e.db.Reader.QueryContext(ctx, `SELECT key FROM custom_fields WHERE brand_id=?`, brandID)
	if err != nil {
		return "", "", "", err
	}
	defer rows.Close()
	var fields []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return "", "", "", err
		}
		fields = append(fields, key)
	}
	if err := rows.Err(); err != nil {
		return "", "", "", err
	}
	r := render.Recipient{
		Name: "there", Email: "preview@example.org", Token: "preview",
		UnsubscribeURL: base + "/u/preview", WebVersionURL: base + "/w/0/preview",
	}
	if subscriberID > 0 {
		var rawFields string
		if err := e.db.Reader.QueryRowContext(ctx, `SELECT name,email,fields FROM subscribers WHERE id=? AND brand_id=?`, subscriberID, brandID).Scan(&r.Name, &r.Email, &rawFields); err != nil {
			return "", "", "", err
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(rawFields), &values); err != nil {
			return "", "", "", err
		}
		r.ID = subscriberID
		r.Fields = map[string]string{}
		for key, value := range values {
			if value != nil {
				r.Fields[key] = fmt.Sprint(value)
			}
		}
		if options.CampaignID > 0 {
			tok := token.Encode(e.cfg.TokenSecret, options.CampaignID, subscriberID)
			r.Token = tok
			r.UnsubscribeURL = base + "/u/" + tok
			r.WebVersionURL = base + "/w/" + strconv.FormatInt(options.CampaignID, 10) + "/" + tok
		}
	}
	options.Fields = fields
	p, err := render.Prepare(options)
	if err != nil {
		return "", "", "", err
	}
	return p.Render(r)
}
