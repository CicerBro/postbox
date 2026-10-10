package contacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"postbox/internal/render"
	"strings"
	"time"
)

// NotificationSetting resolves a list override before its brand default.
// Callers must validate that listID belongs to brandID before using it.
func NotificationSetting(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, brandID, listID int64, name string) (string, error) {
	var value string
	e := q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key IN (?,?) ORDER BY CASE WHEN key=? THEN 0 ELSE 1 END LIMIT 1", fmt.Sprintf("list.%d.%s", listID, name), fmt.Sprintf("brand.%d.%s", brandID, name), fmt.Sprintf("list.%d.%s", listID, name)).Scan(&value)
	return value, e
}

// CreateConfirmationTx replaces earlier tokens so only the newest link is usable.
func CreateConfirmationTx(ctx context.Context, c *sql.Conn, listID, subscriberID int64) (string, error) {
	token := randomToken()
	hash := sha256.Sum256([]byte(token))
	if _, e := c.ExecContext(ctx, "DELETE FROM subscription_confirmations WHERE list_id=? AND subscriber_id=?", listID, subscriberID); e != nil {
		return "", e
	}
	_, e := c.ExecContext(ctx, "INSERT INTO subscription_confirmations(token_hash,list_id,subscriber_id,expires_at) VALUES(?,?,?,?)", hash[:], listID, subscriberID, time.Now().Add(7*24*time.Hour).UnixMilli())
	return token, e
}

// EnqueueConfirmationTx persists the email with the subscription transaction.
func EnqueueConfirmationTx(ctx context.Context, c *sql.Conn, brandID, listID, subscriberID int64, token, baseURL string) error {
	var email string
	if e := c.QueryRowContext(ctx, "SELECT email FROM subscribers WHERE id=? AND brand_id=?", subscriberID, brandID).Scan(&email); e != nil {
		return e
	}
	url := strings.TrimRight(baseURL, "/") + "/confirm/" + token
	subject := "Confirm your subscription"
	body := "<p>Please <a href=\"" + html.EscapeString(url) + "\">confirm your subscription</a>.</p>"
	text := "Confirm your subscription: " + url
	if raw, e := NotificationSetting(ctx, c, brandID, listID, "confirmation_email"); e == nil {
		var defaults struct {
			Subject string `json:"subject"`
			HTML    string `json:"html"`
			Text    string `json:"text"`
		}
		if e = json.Unmarshal([]byte(raw), &defaults); e != nil {
			return e
		}
		if defaults.Subject != "" {
			subject = defaults.Subject
		}
		if defaults.HTML != "" {
			body = defaults.HTML
		}
		if defaults.Text != "" {
			text = defaults.Text
		}
	} else if e != sql.ErrNoRows {
		return e
	}
	for suffix, target := range map[string]*string{"confirmation_subject": &subject, "confirmation_html": &body, "confirmation_text": &text} {
		var custom string
		e := c.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", fmt.Sprintf("list.%d.%s", listID, suffix)).Scan(&custom)
		if e == nil {
			*target = strings.ReplaceAll(custom, "{{ confirmation_url }}", html.EscapeString(url))
		} else if e != sql.ErrNoRows {
			return e
		}
	}
	subject = strings.ReplaceAll(subject, "{{ confirmation_url }}", url)
	body = strings.ReplaceAll(body, "{{ confirmation_url }}", html.EscapeString(url))
	text = strings.ReplaceAll(text, "{{ confirmation_url }}", url)
	payload, e := json.Marshal(map[string]any{"brand_id": brandID, "subscriber_id": subscriberID, "confirmation_token": token, "list_id": listID, "to": email, "subject": subject, "html": body, "text": text})
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('transactional_email',?,'pending',?,?)", string(payload), nowMS(), nowMS())
	return e
}

// EnqueueThankyouTx records a welcome message only with a subscribed transition.
func EnqueueThankyouTx(ctx context.Context, c *sql.Conn, brandID, listID, subscriberID int64) error {
	raw, e := NotificationSetting(ctx, c, brandID, listID, "thankyou_email")
	if e == sql.ErrNoRows || (e == nil && raw == "") {
		return nil
	}
	if e != nil {
		return e
	}
	var message struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
		Text    string `json:"text"`
	}
	if e = json.Unmarshal([]byte(raw), &message); e != nil {
		return e
	}
	var email, name, fields string
	if e = c.QueryRowContext(ctx, "SELECT email,name,fields FROM subscribers WHERE id=? AND brand_id=?", subscriberID, brandID).Scan(&email, &name, &fields); e != nil {
		return e
	}
	var values map[string]any
	if e = json.Unmarshal([]byte(fields), &values); e != nil {
		return e
	}
	replacements := map[string]string{}
	allowed := []string{}
	for key, value := range values {
		replacements[key] = fmt.Sprint(value)
		allowed = append(allowed, key)
	}
	prepared, e := render.Prepare(render.Options{Subject: message.Subject, HTML: message.HTML, Text: message.Text, Fields: allowed})
	if e != nil {
		return e
	}
	subject, body, text, e := prepared.Render(render.Recipient{Email: email, Name: name, Fields: replacements})
	if e != nil {
		return e
	}
	payload, e := json.Marshal(map[string]any{"purpose": "thankyou", "brand_id": brandID, "subscriber_id": subscriberID, "list_id": listID, "to": email, "subject": subject, "html": body, "text": text})
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('transactional_email',?,'pending',?,?)", string(payload), nowMS(), nowMS())
	return e
}
