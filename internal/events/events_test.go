package events

import (
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
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"postbox/internal/store"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }
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
	service.verifiers.Store(v.TopicARN, v)
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
	statements := []string{`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','','from@example.com',1),(2,'Other','','other@example.com',1)`, `INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'user@example.com',1,1),(2,2,'user@example.com',1,1)`, `INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'one','One','single',1),(2,2,'two','Two','single',1)`, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',1,1),(2,2,'subscribed',1,1)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','sending','Hi','Hi',1,1),(2,2,'other','sending','Hi','Hi',1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id,snapshot_email) VALUES(1,1,'sent','ses-id','user@example.com'),(2,2,'pending',NULL,'user@example.com')`, `INSERT INTO campaign_stats(campaign_id) VALUES(1),(2)`}
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

type fakeQueue struct {
	body    string
	cancel  context.CancelFunc
	deleted int
	db      *store.DB
	durable bool
}

func (f *fakeQueue) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if in.WaitTimeSeconds != 20 {
		panic("not long polling")
	}
	f.cancel()
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: aws.String(f.body), ReceiptHandle: aws.String("receipt")}}}, nil
}
func (f *fakeQueue) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.deleted++
	var count int
	f.db.Reader.QueryRow(`SELECT count(*) FROM ses_events WHERE sns_message_id='poll-event'`).Scan(&count)
	f.durable = count == 1
	return &sqs.DeleteMessageOutput{}, nil
}

// Cancel only when Delete runs, so successful processing completes before the polling loop stops.
type committedQueue struct{ fakeQueue }

func (f *committedQueue) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if in.WaitTimeSeconds != 20 {
		panic("not long polling")
	}
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: aws.String(f.body), ReceiptHandle: aws.String("receipt")}}}, nil
}
func (f *committedQueue) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, options ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	out, e := f.fakeQueue.DeleteMessage(ctx, in, options...)
	f.cancel()
	return out, e
}
func TestSQSAcknowledgesOnlyCommittedEvents(t *testing.T) {
	db := eventDB(t)
	cfg := &Config{QueueURL: "https://sqs.eu-west-1.amazonaws.com/123/queue", TopicARN: "arn:aws:sns:eu-west-1:123:events"}
	s := New(db, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	envelope, _ := json.Marshal(SNSMessage{Type: "Notification", MessageID: "poll-event", TopicARN: cfg.TopicARN, Message: `{"eventType":"Delivery","mail":{"messageId":"ses-id"},"delivery":{"recipients":["user@example.com"]}}`})
	q := &committedQueue{fakeQueue{body: string(envelope), cancel: cancel, db: db}}
	s.Poll(ctx, q)
	if q.deleted != 1 || !q.durable {
		t.Fatal("acknowledged before durable commit")
	}
	ctx, cancel = context.WithCancel(context.Background())
	bad := &fakeQueue{body: string(envelope), cancel: cancel, db: db}
	s.Poll(ctx, bad)
	if bad.deleted != 0 {
		t.Fatal("acknowledged failed/cancelled transaction")
	}
}
func TestFallbackUsesFrozenAddressBeforeMessageIDPersists(t *testing.T) {
	db := eventDB(t)
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
