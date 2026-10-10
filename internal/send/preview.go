package send

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/mail"
	"postbox/internal/render"
	"postbox/internal/tracking/token"
	"strconv"
	"time"
)

func (e *Engine) Preview(ctx context.Context, id, subscriberID int64) (string, string, string, error) {
	c, err := e.prepare(ctx, id, false)
	if err != nil {
		return "", "", "", err
	}
	r := recipient{ID: subscriberID, Email: "preview@example.org", Name: "there", Fields: "{}"}
	if subscriberID > 0 {
		err = e.db.Reader.QueryRowContext(ctx, `SELECT coalesce(r.snapshot_email,s.email),coalesce(r.snapshot_name,s.name),coalesce(r.snapshot_fields,s.fields) FROM subscribers s LEFT JOIN campaign_recipients r ON r.subscriber_id=s.id AND r.campaign_id=? WHERE s.id=? AND s.brand_id=?`, id, subscriberID, c.brand).Scan(&r.Email, &r.Name, &r.Fields)
		if err != nil {
			return "", "", "", err
		}
	}
	var fields map[string]any
	if err = json.Unmarshal([]byte(r.Fields), &fields); err != nil {
		return "", "", "", err
	}
	v := map[string]string{}
	for k, x := range fields {
		v[k] = fmt.Sprint(x)
	}
	tok := token.Encode(e.cfg.TokenSecret, id, subscriberID)
	return c.prepared.Render(render.Recipient{ID: subscriberID, Email: r.Email, Name: r.Name, Fields: v, Token: tok, UnsubscribeURL: c.trackingURL + "/u/" + tok, WebVersionURL: c.trackingURL + "/w/" + strconv.FormatInt(id, 10) + "/" + tok})
}
func (e *Engine) TestSend(ctx context.Context, id int64, addresses []string) error {
	return e.TestCampaign(ctx, id, addresses, 0)
}
func (e *Engine) TestCampaign(ctx context.Context, id int64, addresses []string, subscriberID int64) error {
	if !e.enabled.Load() {
		return fmt.Errorf("global sending is disabled")
	}
	if len(addresses) == 0 || len(addresses) > 20 {
		return fmt.Errorf("test sends require 1 to 20 addresses")
	}
	normalized := make([]string, 0, len(addresses))
	seen := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		email, err := render.NormalizeEmail(address)
		if err != nil {
			return err
		}
		if !seen[email] {
			seen[email] = true
			normalized = append(normalized, email)
		}
	}
	addresses = normalized
	c, err := e.prepare(ctx, id, false)
	if err != nil {
		return err
	}
	delivery, err := e.resolveBrand(ctx, c.brand)
	if err != nil {
		return err
	}
	q, err := delivery.sender.GetAccount(ctx)
	if err != nil {
		return err
	}
	if err = delivery.setQuota(q); err != nil {
		return err
	}
	r := recipient{ID: subscriberID, Name: "Test recipient", Fields: "{}"}
	if subscriberID > 0 {
		err = e.db.Reader.QueryRowContext(ctx, `SELECT name,fields FROM subscribers WHERE id=? AND brand_id=?`, subscriberID, c.brand).Scan(&r.Name, &r.Fields)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	if err = ReserveUserTestSend(ctx, e.db, id, int64(len(addresses))); err != nil {
		return err
	}
	for _, address := range addresses {
		r.Email = address
		raw, err := e.renderWithSubjectPrefix(c, r, "[Test] ")
		if err != nil {
			return err
		}
		if err = delivery.wait(ctx); err != nil {
			return err
		}
		if !e.enabled.Load() {
			return fmt.Errorf("global sending is disabled")
		}
		t := time.Now()
		e.markRequest()
		_, err = delivery.sender.Send(ctx, Request{From: (&mail.Address{Name: c.fromName, Address: c.fromEmail}).String(), To: r.Email, Raw: raw, ConfigurationSet: c.configSet, BrandID: c.brand, CampaignID: id})
		e.observe(time.Since(t), false)
		e.requests.Add(1)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendNotification sends an already built MIME notification through the same account limiter.
func (e *Engine) SendNotification(ctx context.Context, r Request) (string, error) {
	return e.SendNotificationChecked(ctx, r, nil)
}

// NotificationNotSentError confirms that the notification never reached Sender.Send.
type NotificationNotSentError struct{ Err error }

func (e *NotificationNotSentError) Error() string { return e.Err.Error() }
func (e *NotificationNotSentError) Unwrap() error { return e.Err }

// SendNotificationChecked rechecks notification consent immediately after rate limiting.
func (e *Engine) SendNotificationChecked(ctx context.Context, r Request, check func(context.Context) error) (string, error) {
	if !e.enabled.Load() {
		return "", &NotificationNotSentError{fmt.Errorf("global sending is disabled")}
	}
	delivery, err := e.resolveBrand(ctx, r.BrandID)
	if err != nil {
		return "", &NotificationNotSentError{err}
	}
	q, err := delivery.sender.GetAccount(ctx)
	if err != nil {
		return "", &NotificationNotSentError{err}
	}
	if err = delivery.setQuota(q); err != nil {
		return "", &NotificationNotSentError{err}
	}
	if err = delivery.wait(ctx); err != nil {
		return "", &NotificationNotSentError{err}
	}
	if !e.enabled.Load() {
		return "", &NotificationNotSentError{fmt.Errorf("global sending is disabled")}
	}
	if check != nil {
		if err = check(ctx); err != nil {
			return "", &NotificationNotSentError{err}
		}
	}
	start := time.Now()
	e.markRequest()
	id, err := delivery.sender.Send(ctx, r)
	e.observe(time.Since(start), false)
	e.requests.Add(1)
	return id, err
}
