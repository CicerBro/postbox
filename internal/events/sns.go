package events

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type SNSMessage struct {
	Type             string
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Message          string
	Timestamp        string
	SignatureVersion string
	Signature        string
	SigningCertURL   string
	SubscribeURL     string
	Token            string
	Subject          *string
}
type cachedCertificate struct {
	cert    *x509.Certificate
	expires time.Time
}
type Verifier struct {
	TopicARN string
	Client   *http.Client
	Roots    *x509.CertPool
	Now      func() time.Time
	MaxAge   time.Duration
	mu       sync.Mutex
	cache    map[string]cachedCertificate
}

var snsHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)
var certPath = regexp.MustCompile(`^/SimpleNotificationService-[A-Za-z0-9_-]+\.pem$`)

func trustedURL(raw string, certificate bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !snsHost.MatchString(u.Hostname()) || u.Fragment != "" {
		return nil, errors.New("untrusted SNS URL")
	}
	if certificate && (!certPath.MatchString(u.Path) || u.RawQuery != "") {
		return nil, errors.New("invalid SNS certificate URL")
	}
	return u, nil
}
func canonical(m SNSMessage) ([]byte, error) {
	var keys []string
	switch m.Type {
	case "Notification":
		keys = []string{"Message", "MessageId"}
		if m.Subject != nil {
			keys = append(keys, "Subject")
		}
		keys = append(keys, "Timestamp", "TopicArn", "Type")
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		keys = []string{"Message", "MessageId", "SubscribeURL", "Timestamp", "Token", "TopicArn", "Type"}
	default:
		return nil, errors.New("unsupported SNS message type")
	}
	values := map[string]string{"Message": m.Message, "MessageId": m.MessageID, "Timestamp": m.Timestamp, "TopicArn": m.TopicARN, "Type": m.Type, "SubscribeURL": m.SubscribeURL, "Token": m.Token}
	if m.Subject != nil {
		values["Subject"] = *m.Subject
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\n" + values[k] + "\n")
	}
	return []byte(b.String()), nil
}
func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}
func (v *Verifier) client() *http.Client {
	c := http.Client{Timeout: 10 * time.Second}
	if v.Client != nil {
		c = *v.Client
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("SNS redirects prohibited") }
	return &c
}
func (v *Verifier) Verify(ctx context.Context, m SNSMessage) error {
	if v.TopicARN == "" || m.TopicARN != v.TopicARN || m.MessageID == "" {
		return errors.New("unexpected SNS topic or empty message ID")
	}
	t, e := time.Parse(time.RFC3339, m.Timestamp)
	if e != nil {
		return errors.New("invalid SNS timestamp")
	}
	age := v.MaxAge
	if age == 0 {
		age = 24 * time.Hour
	}
	if t.After(v.now().Add(5*time.Minute)) || v.now().Sub(t) > age {
		return errors.New("stale SNS message")
	}
	u, e := trustedURL(m.SigningCertURL, true)
	if e != nil {
		return e
	}
	arn := strings.Split(m.TopicARN, ":")
	if len(arn) != 6 || u.Hostname() != "sns."+arn[3]+".amazonaws.com" && u.Hostname() != "sns."+arn[3]+".amazonaws.com.cn" {
		return errors.New("certificate region mismatch")
	}
	data, e := canonical(m)
	if e != nil {
		return e
	}
	sig, e := base64.StdEncoding.DecodeString(m.Signature)
	if e != nil {
		return e
	}
	cert, e := v.certificate(ctx, u.String())
	if e != nil {
		return e
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("SNS certificate is not RSA")
	}
	switch m.SignatureVersion {
	case "1":
		h := sha1.Sum(data)
		return rsa.VerifyPKCS1v15(key, crypto.SHA1, h[:], sig)
	case "2":
		h := sha256.Sum256(data)
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, h[:], sig)
	default:
		return errors.New("unsupported SNS signature version")
	}
}
func (v *Verifier) certificate(ctx context.Context, raw string) (*x509.Certificate, error) {
	v.mu.Lock()
	entry, ok := v.cache[raw]
	v.mu.Unlock()
	if ok && v.now().Before(entry.expires) {
		return entry.cert, nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, e
	}
	resp, e := v.client().Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SNS certificate HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("invalid SNS certificate response")
	}
	p, _ := pem.Decode(b)
	if p == nil || p.Type != "CERTIFICATE" {
		return nil, errors.New("invalid SNS certificate PEM")
	}
	c, e := x509.ParseCertificate(p.Bytes)
	if e != nil {
		return nil, e
	}
	intermediates := x509.NewCertPool()
	remainder := b
	for {
		block, rest := pem.Decode(remainder)
		if block == nil {
			break
		}
		remainder = rest
		other, err := x509.ParseCertificate(block.Bytes)
		if err == nil && !other.Equal(c) {
			intermediates.AddCert(other)
		}
	}
	// SNS signing certificates are for message signing, not TLS hostname authentication.
	// The HTTPS transport authenticates the strictly allowlisted download hostname.
	if _, e = c.Verify(x509.VerifyOptions{Roots: v.Roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, CurrentTime: v.now()}); e != nil {
		return nil, e
	}
	expiry := v.now().Add(time.Hour)
	if c.NotAfter.Before(expiry) {
		expiry = c.NotAfter
	}
	v.mu.Lock()
	if v.cache == nil {
		v.cache = map[string]cachedCertificate{}
	}
	if len(v.cache) >= 32 {
		v.cache = map[string]cachedCertificate{}
	}
	v.cache[raw] = cachedCertificate{c, expiry}
	v.mu.Unlock()
	return c, nil
}
func (v *Verifier) Confirm(ctx context.Context, m SNSMessage) error {
	if m.Type != "SubscriptionConfirmation" {
		return errors.New("not a subscription confirmation")
	}
	if e := v.Verify(ctx, m); e != nil {
		return e
	}
	u, e := trustedURL(m.SubscribeURL, false)
	if e != nil {
		return e
	}
	certURL, _ := url.Parse(m.SigningCertURL)
	if u.Hostname() != certURL.Hostname() || u.Path != "/" || u.Query().Get("Action") != "ConfirmSubscription" || u.Query().Get("TopicArn") != m.TopicARN || u.Query().Get("Token") != m.Token {
		return errors.New("invalid SNS confirmation URL")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return e
	}
	resp, e := v.client().Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("SNS confirmation HTTP %d", resp.StatusCode)
	}
	return nil
}
func DecodeSNS(b []byte) (SNSMessage, error) {
	var m SNSMessage
	e := json.Unmarshal(b, &m)
	return m, e
}
