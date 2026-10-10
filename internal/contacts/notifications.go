package contacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

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
	var raw string
	if e := c.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", fmt.Sprintf("brand.%d.confirmation_email", brandID)).Scan(&raw); e == nil {
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
