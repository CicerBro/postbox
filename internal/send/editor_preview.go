package send

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"postbox/internal/previewtoken"
	"postbox/internal/render"
	"strings"
	"time"
)

// EditorPreview renders unsaved source without creating campaigns or tracking links.
// Saved campaigns receive isolated, encrypted preview action URLs.
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
	}
	if options.CampaignID > 0 {
		appBase := strings.TrimRight(e.cfg.BaseURL, "/")
		parsed, err := url.Parse(appBase)
		if err != nil || parsed.Host == "" {
			return "", "", "", fmt.Errorf("invalid preview base URL")
		}
		tok, err := previewtoken.Encode(e.cfg.TokenSecret, previewtoken.Claims{Brand: brandID, Campaign: options.CampaignID, Subscriber: subscriberID, Host: parsed.Host, Expires: time.Now().Add(time.Hour).Unix()})
		if err != nil {
			return "", "", "", err
		}
		r.Token = tok
		r.UnsubscribeURL = appBase + "/preview/unsubscribe/" + tok
		r.WebVersionURL = appBase + "/preview/campaign/" + tok
	}

	options.Fields = fields
	p, err := render.Prepare(options)
	if err != nil {
		return "", "", "", err
	}
	return p.Render(r)
}

// AudienceTarget identifies one included or excluded campaign audience.
type AudienceTarget struct {
	Type string `json:"target_type"`
	ID   int64  `json:"target_id"`
	Mode string `json:"mode"`
}

type PreviewRecipient struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// EditorRecipients uses the same eligibility predicate as sending without
// persisting unsaved targets or creating a recipient snapshot.
func (e *Engine) EditorRecipients(ctx context.Context, brandID int64, targets []AudienceTarget, after int64) ([]PreviewRecipient, error) {
	query, args, err := e.audienceTargetsSQL(ctx, brandID, targets)
	if err != nil {
		return nil, err
	}
	args = append(args, after)
	rows, err := e.db.Reader.QueryContext(ctx, `SELECT s.id,s.email,s.name `+query+` AND s.id>? ORDER BY s.id LIMIT 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreviewRecipient{}
	for rows.Next() {
		var recipient PreviewRecipient
		if err := rows.Scan(&recipient.ID, &recipient.Email, &recipient.Name); err != nil {
			return nil, err
		}
		out = append(out, recipient)
	}
	return out, rows.Err()
}

// PreviewIdentity validates a capability against current campaign and subscriber scope.
func (e *Engine) PreviewIdentity(ctx context.Context, tok string) (previewtoken.Claims, error) {
	c, err := previewtoken.Decode(e.cfg.TokenSecret, tok)
	if err != nil {
		return c, err
	}
	var exists int
	err = e.db.Reader.QueryRowContext(ctx, `SELECT 1 FROM campaigns WHERE id=? AND brand_id=?`, c.Campaign, c.Brand).Scan(&exists)
	if err == nil && c.Subscriber > 0 {
		err = e.db.Reader.QueryRowContext(ctx, `SELECT 1 FROM subscribers WHERE id=? AND brand_id=? AND status='active'`, c.Subscriber, c.Brand).Scan(&exists)
	}
	return c, err
}

func (e *Engine) PreviewListToken(c previewtoken.Claims, list int64) (string, error) {
	c.List = list
	return previewtoken.Encode(e.cfg.TokenSecret, c)
}

// SavedEditorPreview always reads saved edits rather than delivery preparations.
func (e *Engine) SavedEditorPreview(ctx context.Context, c previewtoken.Claims) (string, error) {
	var o render.Options
	var inline int
	err := e.db.Reader.QueryRowContext(ctx, `SELECT subject,html,coalesce(text,''),inline_css FROM campaigns WHERE id=? AND brand_id=?`, c.Campaign, c.Brand).Scan(&o.Subject, &o.HTML, &o.Text, &inline)
	if err != nil {
		return "", err
	}
	o.CampaignID, o.InlineCSS = c.Campaign, inline != 0
	_, body, _, err := e.EditorPreview(ctx, c.Brand, c.Subscriber, o)
	return body, err
}
