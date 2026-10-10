package jobs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type webhookTask struct {
	WebhookID int64           `json:"webhook_id"`
	Body      json.RawMessage `json:"body"`
}
type Event struct {
	Type    string `json:"type"`
	BrandID int64  `json:"brand_id"`
	At      int64  `json:"at"`
	Data    any    `json:"data"`
}

// Emit persists the event deliveries together, so a failed enqueue cannot lose only some recipients.
func (r *Runner) Emit(ctx context.Context, brandID int64, event string, data any) error {
	return r.db.Immediate(ctx, func(c *sql.Conn) error { return r.EmitTx(ctx, c, brandID, event, data) })
}

// EmitTx enqueues webhooks in the caller's mutation transaction.
func EmitTx(ctx context.Context, c *sql.Conn, brandID int64, event string, data any) error {
	switch event {
	case "subscribe", "unsubscribe", "bounce", "complaint", "campaign_started", "campaign_finished":
	default:
		return fmt.Errorf("unsupported webhook event %q", event)
	}
	body, err := json.Marshal(Event{event, brandID, time.Now().UnixMilli(), data})
	if err != nil {
		return err
	}

	rows, err := c.QueryContext(ctx, "SELECT id,events FROM webhooks WHERE brand_id=? AND is_active=1", brandID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var events []string
		if err = json.Unmarshal([]byte(raw), &events); err != nil {
			rows.Close()
			return errors.New("webhook events must be a JSON array")
		}
		for _, e := range events {
			if e == event {
				ids = append(ids, id)
				break
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	for _, id := range ids {
		payload, _ := json.Marshal(webhookTask{id, body})
		if _, err = c.ExecContext(ctx, "INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('outbound_webhook',?,'pending',?,?)", string(payload), time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// EmitTx includes configured campaign notification callbacks in the same transaction.
func (r *Runner) EmitTx(ctx context.Context, c *sql.Conn, brandID int64, event string, data any) error {
	if err := EmitTx(ctx, c, brandID, event, data); err != nil {
		return err
	}
	if event != "campaign_started" && event != "campaign_finished" {
		return nil
	}
	r.mu.RLock()
	_, notify := r.handlers["email_notification"]
	r.mu.RUnlock()
	if !notify {
		return nil
	}
	body, err := json.Marshal(Event{event, brandID, time.Now().UnixMilli(), data})
	if err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('email_notification',?,'pending',?,?)", string(body), time.Now().UnixMilli(), time.Now().UnixMilli())
	return err
}

// Signature is HMAC-SHA256 over timestamp + '.' + the exact request body.
func Signature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("webhook URL is invalid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("webhook URL must use http or https")
	}
	if u.User != nil || u.Fragment != "" {
		return errors.New("webhook URL credentials and fragments are prohibited")
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return errors.New("webhook hostname is not public")
	}
	if a, err := netip.ParseAddr(h); err == nil && !publicIP(a) {
		return errors.New("webhook IP address is not public")
	}
	return nil
}

var forbiddenPrefixes = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16")}

func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range forbiddenPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// DNS is resolved and checked at every connection, then the checked IP is dialed directly.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid webhook address")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("webhook DNS lookup failed")
	}
	if len(ips) == 0 {
		return nil, errors.New("webhook DNS has no addresses")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("webhook DNS resolves to a non-public address")
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, errors.New("webhook connection failed")
}
func (r *Runner) deliverWebhook(ctx context.Context, j Job) error {
	var task webhookTask
	if err := json.Unmarshal(j.Payload, &task); err != nil {
		return errors.New("invalid webhook job payload")
	}
	var endpoint, secret string
	var active int
	err := r.db.Reader.QueryRowContext(ctx, "SELECT url,secret,is_active FROM webhooks WHERE id=?", task.WebhookID).Scan(&endpoint, &secret, &active)
	if errors.Is(err, sql.ErrNoRows) || active == 0 && err == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if err = ValidateWebhookURL(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(task.Body))
	if err != nil {
		return errors.New("could not construct webhook request")
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postbox-Timestamp", timestamp)
	req.Header.Set("X-Postbox-Signature", "sha256="+Signature(secret, timestamp, task.Body))
	req.Header.Set("X-Postbox-Delivery", strconv.FormatInt(j.ID, 10))
	transport := &http.Transport{DialContext: publicDial, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("webhook redirects are prohibited") }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("webhook request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
