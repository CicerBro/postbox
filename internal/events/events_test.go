package events

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"postbox/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }

func TestSNSRealSigningCertificateChain(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	leaf := read("sns-signing.pem")
	issuer := read("amazon-rsa-2048-m04.der")
	rootPEM := read("amazon-root-ca-1.pem")
	rootBlock, _ := pem.Decode(rootPEM)
	if rootBlock == nil {
		t.Fatal("invalid root fixture PEM")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("invalid root fixture")
	}
	for _, trusted := range []bool{true, false} {
		t.Run(fmt.Sprintf("trusted-root-%t", trusted), func(t *testing.T) {
			requests := 0
			v := &Verifier{Roots: roots, Now: func() time.Time { return time.Date(2026, 10, 11, 16, 30, 0, 0, time.UTC) }}
			if !trusted {
				v.Roots = x509.NewCertPool()
			}
			v.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				requests++
				var b []byte
				switch r.URL.String() {
				case "https://sns.eu-central-1.amazonaws.com/SimpleNotificationService-1e59c4574facfe41babdb2d652f8ebef.pem":
					b = leaf
				case "https://crt.r2m04.amazontrust.com/r2m04.cer":
					b = issuer
				case "https://crt.rootca1.amazontrust.com/rootca1.cer":
					b = rootBlock.Bytes
				default:
					t.Fatalf("unexpected certificate request: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
			})}
			raw := "https://sns.eu-central-1.amazonaws.com/SimpleNotificationService-1e59c4574facfe41babdb2d652f8ebef.pem"
			_, err := v.certificate(context.Background(), raw)
			if trusted && err != nil {
				t.Fatal(err)
			}
			if !trusted && err == nil {
				t.Fatal("downloaded issuer became a trusted root")
			}
			if trusted {
				if _, err := v.certificate(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				if requests != 2 {
					t.Fatalf("cache fetched certificates again: %d requests", requests)
				}
			}
		})
	}
}

func TestSNSIssuerURLAllowlist(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/issuer.cer", "https://crt.r2m04.amazontrust.com.attacker.test/r2m04.cer",
		"https://crt.r2m04.amazontrust.com@127.0.0.1/r2m04.cer", "https://crt.r2m04.amazontrust.com:8443/r2m04.cer",
		"https://crt.r2m04.amazontrust.com/r2m04.cer?token=x", "https://crt.r2m04.amazontrust.com/../r2m04.cer",
	} {
		if _, err := issuerURL(raw); err == nil {
			t.Fatalf("accepted unsafe issuer URL %s", raw)
		}
	}
	u, err := issuerURL("http://crt.r2m04.amazontrust.com/r2m04.cer")
	if err != nil || u.String() != "https://crt.r2m04.amazontrust.com/r2m04.cer" {
		t.Fatalf("issuer HTTPS upgrade: %v %v", u, err)
	}
}

func TestSNSFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(context.Context, string) (int64, error)
		stage   string
	}{
		{"unknown topic", func(context.Context, string) (int64, error) { return 0, errors.New("topic not found") }, "topic_lookup"},
		{"invalid signature", func(context.Context, string) (int64, error) { return 1, nil }, "signature_verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := New(nil, &Config{ResolveTopic: tc.resolve, Logger: slog.New(slog.NewJSONHandler(&output, nil))})
			w := httptest.NewRecorder()
			s.SNS(w, httptest.NewRequest(http.MethodPost, "/events/sns", strings.NewReader(`{"TopicArn":"arn:aws:sns:eu-central-1:123:events","Token":"private-token","Message":"private-body"}`)))
			if w.Code != http.StatusForbidden || !strings.Contains(output.String(), tc.stage) {
				t.Fatalf("status=%d log=%s", w.Code, output.String())
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatal("logged SNS payload")
			}
		})
	}
	var output bytes.Buffer
	s := New(nil, &Config{Logger: slog.New(slog.NewJSONHandler(&output, nil))})
	s.logSNSFailure(context.Background(), "subscription_confirmation", &url.Error{Op: "Get", URL: "https://sns.eu-central-1.amazonaws.com/?Token=private-token", Err: errors.New("connection refused")})
	if strings.Contains(output.String(), "private-token") || strings.Contains(output.String(), "https://") || !strings.Contains(output.String(), "connection refused") {
		t.Fatalf("unsafe or missing transport diagnostic: %s", output.String())
	}
}

func TestUnknownSNSTopicsDoNotPopulateVerifierCache(t *testing.T) {
	s := New(nil, &Config{
		ResolveTopic: func(context.Context, string) (int64, error) { return 0, errors.New("unknown topic") },
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for i := range 1000 {
		w := httptest.NewRecorder()
		s.SNS(w, httptest.NewRequest(http.MethodPost, "/events/sns", strings.NewReader(fmt.Sprintf(`{"TopicArn":"arn:aws:sns:eu-west-1:123:unknown-%d"}`, i))))
		if w.Code != http.StatusForbidden {
			t.Fatalf("unknown topic status=%d", w.Code)
		}
	}
	if len(s.verifiers) != 0 {
		t.Fatalf("unknown topics cached: %d", len(s.verifiers))
	}
}

func TestOwnedSNSTopicVerifierCacheIsBounded(t *testing.T) {
	s := New(nil, nil)
	v := s.topicVerifier("owned")
	if s.topicVerifier("owned") != v {
		t.Fatal("repeated topic did not reuse certificate cache")
	}
	for i := range maxTopicVerifiers * 2 {
		s.topicVerifier(fmt.Sprintf("owned-%d", i))
		if len(s.verifiers) > maxTopicVerifiers {
			t.Fatalf("cache exceeded bound: %d", len(s.verifiers))
		}
	}
}

func TestOwnedSNSTopicVerifierCacheConcurrentCallers(t *testing.T) {
	s := New(nil, nil)
	shared := s.topicVerifier("shared")
	var callers sync.WaitGroup
	for range 16 {
		callers.Go(func() {
			if got := s.topicVerifier("shared"); got != shared {
				t.Error("concurrent callers created duplicate verifiers")
			}
		})
	}
	callers.Wait()
	for worker := range 16 {
		callers.Go(func() {
			for i := range maxTopicVerifiers {
				topic := fmt.Sprintf("owned-%d-%d", worker, i)
				if v := s.topicVerifier(topic); v.TopicARN != topic {
					t.Errorf("wrong topic verifier: %s", v.TopicARN)
				}
			}
		})
	}
	callers.Wait()
	if len(s.verifiers) > maxTopicVerifiers {
		t.Fatalf("concurrent cache exceeded bound: %d", len(s.verifiers))
	}
}

func TestSNSVerification(t *testing.T) {
	now := time.Now()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.eu-west-1.amazonaws.com"}, DNSNames: []string{"sns.eu-west-1.amazonaws.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	requests := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))))}, nil
	})}
	v := &Verifier{TopicARN: "arn:aws:sns:eu-west-1:123:events", Client: client, Roots: roots, Now: func() time.Time { return now }}
	m := SNSMessage{Type: "Notification", MessageID: "unique", TopicARN: v.TopicARN, Message: `{"eventType":"Delivery"}`, Timestamp: now.Format(time.RFC3339), SignatureVersion: "2", SigningCertURL: "https://sns.eu-west-1.amazonaws.com/SimpleNotificationService-test.pem"}
	b, _ := canonical(m)
	h := sha256.Sum256(b)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	if e = v.Verify(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	if e = v.Verify(context.Background(), m); e != nil || requests != 1 {
		t.Fatalf("certificate cache requests=%d err=%v", requests, e)
	}
	service := New(eventDB(t), &Config{ResolveTopic: func(_ context.Context, arn string) (int64, error) {
		if arn != v.TopicARN {
			return 0, fmt.Errorf("unknown topic")
		}
		return 1, nil
	}})
	service.verifiers = map[string]*Verifier{v.TopicARN: v}
	m.MessageID = "routed"
	m.Message = `{"eventType":"Delivery","mail":{"messageId":"ses-id","tags":{"brand_id":["1"]}},"delivery":{"recipients":["user@example.com"]}}`
	b, _ = canonical(m)
	h = sha256.Sum256(b)
	sig, _ = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	body, _ := json.Marshal(m)
	w := httptest.NewRecorder()
	service.SNS(w, httptest.NewRequest(http.MethodPost, "/events/sns", strings.NewReader(string(body))))
	if w.Code != http.StatusNoContent {
		t.Fatalf("brand SNS route returned %d: %s", w.Code, w.Body.String())
	}
	m.Message = "forged"
	if v.Verify(context.Background(), m) == nil {
		t.Fatal("accepted forged message")
	}
	m.SigningCertURL = "https://sns.eu-west-1.amazonaws.com.attacker.test/key.pem"
	if v.Verify(context.Background(), m) == nil || requests != 1 {
		t.Fatal("fetched forged cert URL")
	}
	m.SigningCertURL = "https://sns.eu-west-1.amazonaws.com@127.0.0.1/key.pem"
	if v.Verify(context.Background(), m) == nil {
		t.Fatal("accepted credentials URL")
	}
	m.SigningCertURL = "https://sns.eu-west-1.amazonaws.com/SimpleNotificationService-test.pem"
	m.Type = "SubscriptionConfirmation"
	m.Token = "confirmation-token"
	m.SubscribeURL = "https://attacker.example/confirm"
	m.Message = "confirm"
	b, _ = canonical(m)
	h = sha256.Sum256(b)
	sig, _ = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	if v.Confirm(context.Background(), m) == nil || requests != 1 {
		t.Fatal("requested forged subscription URL")
	}
	m.Timestamp = now.Add(-25 * time.Hour).Format(time.RFC3339)
	if v.Verify(context.Background(), m) == nil {
		t.Fatal("accepted stale signed message")
	}

}
func eventDB(t *testing.T) *store.DB {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	statements := []string{`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','','from@example.com',1),(2,'Other','','other@example.com',1)`, `INSERT INTO subscribers(id,brand_id,email,created_at,updated_at,tracking_legacy) VALUES(1,1,'user@example.com',1,1,1),(2,2,'user@example.com',1,1,1)`, `INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'one','One','single',1),(2,2,'two','Two','single',1)`, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',1,1),(2,2,'subscribed',1,1)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at,tracking_legacy) VALUES(1,1,'campaign','sending','Hi','Hi',1,1,1),(2,2,'other','sending','Hi','Hi',1,1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id,snapshot_email) VALUES(1,1,'sent','ses-id','user@example.com'),(2,2,'pending',NULL,'user@example.com')`, `INSERT INTO campaign_stats(campaign_id) VALUES(1),(2)`}
	for _, q := range statements {
		if _, e = db.Writer.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return db
}
func TestComplaintDedupInferredBrandSuppression(t *testing.T) {
	db := eventDB(t)
	s := New(db, &Config{})
	payload := []byte(`{"eventType":"Complaint","mail":{"messageId":"ses-id"},"complaint":{"complainedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	for i := 0; i < 2; i++ {
		if e := s.Process(context.Background(), "sns-id", payload); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE brand_id=1 AND reason='complaint'`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("suppression %d %v", n, e)
	}
	db.Reader.QueryRow(`SELECT count(*) FROM subscribers WHERE status='complained'`).Scan(&n)
	if n != 1 {
		t.Fatalf("brand status %d", n)
	}
	db.Reader.QueryRow(`SELECT count(*) FROM list_subscribers WHERE status='unsubscribed'`).Scan(&n)
	if n != 1 {
		t.Fatalf("memberships %d", n)
	}
	db.Reader.QueryRow(`SELECT complaints FROM campaign_stats WHERE campaign_id=1`).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate complaint count %d", n)
	}
	var status string
	db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=2`).Scan(&status)
	if status != "pending" {
		t.Fatalf("other brand pending status %s", status)
	}
}

func TestBrandTopicRejectsAnotherBrandsEvent(t *testing.T) {
	db := eventDB(t)
	s := New(db, &Config{})
	payload := []byte(`{"eventType":"Complaint","mail":{"messageId":"ses-id","tags":{"brand_id":["2"]}},"complaint":{"complainedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	if e := s.process(context.Background(), 1, "wrong-brand", payload); e == nil {
		t.Fatal("accepted an event tagged for another brand")
	}
	var count int
	if e := db.Reader.QueryRow("SELECT count(*) FROM ses_events").Scan(&count); e != nil || count != 0 {
		t.Fatalf("rejected event was persisted: %d %v", count, e)
	}
}
func TestBounceThreshold(t *testing.T) {
	db := eventDB(t)
	s := New(db, &Config{SoftBounceLimit: 2})
	payload := []byte(`{"eventType":"Bounce","mail":{"messageId":"ses-id"},"bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	for _, id := range []string{"a", "a", "b"} {
		if e := s.Process(context.Background(), id, payload); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	db.Reader.QueryRow(`SELECT soft_bounce_count FROM subscribers WHERE id=1`).Scan(&n)
	if n != 2 {
		t.Fatalf("count %d", n)
	}
	db.Reader.QueryRow(`SELECT count(*) FROM suppressions WHERE reason='soft_bounce_limit'`).Scan(&n)
	if n != 1 {
		t.Fatalf("threshold suppression %d", n)
	}
	var ev sesEvent
	if e := json.Unmarshal(payload, &ev); e != nil {
		t.Fatal(e)
	}
}

func TestFallbackUsesFrozenAddressBeforeMessageIDPersists(t *testing.T) {
	db := eventDB(t)
	if _, e := db.Writer.Exec(`INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(1,1,1,1,'started')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Writer.Exec(`UPDATE campaign_recipients SET message_id=NULL WHERE campaign_id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Writer.Exec(`UPDATE subscribers SET email='changed@example.com' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	s := New(db, &Config{})
	payload := []byte(`{"eventType":"Complaint","mail":{"messageId":"early-ses-id","tags":{"campaign_id":["1"],"brand_id":["1"]}},"complaint":{"complainedRecipients":[{"emailAddress":"user@example.com"}]}}`)
	if e := s.Process(context.Background(), "early-sns", payload); e != nil {
		t.Fatal(e)
	}
	var n int
	db.Reader.QueryRow(`SELECT complaints FROM campaign_stats WHERE campaign_id=1`).Scan(&n)
	if n != 1 {
		t.Fatal("frozen-address event unmatched")
	}
	var status string
	db.Reader.QueryRow(`SELECT status FROM subscribers WHERE id=1`).Scan(&status)
	if status != "complained" {
		t.Fatalf("changed profile not suppressed: %s", status)
	}
}
