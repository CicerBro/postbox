package send

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"postbox/internal/store"
)

type policySender struct {
	errors    []error
	calls     int
	requests  []Request
	afterSend func()
}

func (s *policySender) GetAccount(context.Context) (Quota, error) {
	return Quota{10000, 100000, 0}, nil
}
func (s *policySender) Send(_ context.Context, r Request) (string, error) {
	s.calls++
	s.requests = append(s.requests, r)
	if s.afterSend != nil {
		defer s.afterSend()
	}
	if s.calls <= len(s.errors) {
		return "", s.errors[s.calls-1]
	}
	return "accepted", nil
}

func assertPolicyJournal(t *testing.T, db *store.DB, outcomes []string) {
	t.Helper()
	rows, err := db.Reader.Query(`SELECT attempt,outcome,error,finished_at FROM send_attempts ORDER BY attempt`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var attempt, finished int64
		var outcome string
		var detail *string
		if err := rows.Scan(&attempt, &outcome, &detail, &finished); err != nil {
			t.Fatal(err)
		}
		if i >= len(outcomes) || attempt != int64(i+1) || outcome != outcomes[i] || finished == 0 {
			t.Fatalf("attempt %d outcome=%s finished=%d expected=%v", attempt, outcome, finished, outcomes)
		}
		if outcome == "unknown" && (detail == nil || *detail == "") {
			t.Fatal("uncertain attempt missing error")
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(outcomes) {
		t.Fatalf("journal count=%d expected=%d", i, len(outcomes))
	}
}

func TestAmbiguousAcceptanceNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"timeout", context.DeadlineExceeded},
		{"dropped_connection", &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}},
		{"eof", io.EOF},
		{"server_fault", fakeError("InternalServiceError", true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, e, _ := fixture(t, 1)
			s := &policySender{errors: []error{tc.err}}
			e.sender = s
			if err := e.RunCampaign(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			m := e.Metrics()
			if s.calls != 1 || m.Requests != 1 || m.Sent != 0 || m.Unknown != 1 || m.Failed != 0 {
				t.Fatalf("calls=%d metrics=%+v", s.calls, m)
			}
			assertPolicyJournal(t, db, []string{"unknown"})
			// Even a stale/manual pending reset cannot bypass the durable attempt history.
			if _, err := db.Writer.Exec(`UPDATE campaigns SET status='paused'; UPDATE campaign_recipients SET status='pending'`); err != nil {
				t.Fatal(err)
			}
			if err := e.Resume(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if err := e.RunCampaign(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if s.calls != 1 {
				t.Fatalf("uncertain acceptance resent: %d calls", s.calls)
			}
			assertPolicyJournal(t, db, []string{"unknown"})
		})
	}
}

func TestDefinitiveThrottleRetries(t *testing.T) {
	db, e, _ := fixture(t, 1)
	s := &policySender{errors: []error{fakeError("TooManyRequestsException", false)}}
	e.sender = s
	started := time.Now()
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if s.calls != 2 || e.Metrics().Sent != 1 || time.Since(started) < 50*time.Millisecond {
		t.Fatalf("calls=%d metrics=%+v", s.calls, e.Metrics())
	}
	assertPolicyJournal(t, db, []string{"retry", "sent"})
}

func TestPermanentPolicyDoesNotRetry(t *testing.T) {
	db, e, _ := fixture(t, 1)
	s := &policySender{errors: []error{fakeError("MessageRejected", false)}}
	e.sender = s
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 || e.Metrics().Failed != 1 {
		t.Fatalf("calls=%d metrics=%+v", s.calls, e.Metrics())
	}
	assertPolicyJournal(t, db, []string{"failed"})
}

func TestHTTPAcceptedEOFNeverRetries(t *testing.T) {
	db, e, f := fixture(t, 1)
	f.Config.TimeoutEvery = 2
	f.Config.Timeout = time.Millisecond
	f.Calls.Store(1)
	server := httptest.NewServer(f.Handler())
	defer server.Close()
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fake", "fake", ""), HTTPClient: server.Client(), Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 3; o.RateLimiter = ratelimit.None })
	}}
	e.sender = &AWS{Client: sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })}
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	server.Close()
	m := e.Metrics()
	if f.Accepted.Load() != 1 || f.Recipients["reader1@example.org"] != 1 || m.Requests != 1 || m.Sent != 0 || m.Unknown != 1 {
		t.Fatalf("accepted=%d recipients=%v metrics=%+v", f.Accepted.Load(), f.Recipients, m)
	}
	assertPolicyJournal(t, db, []string{"unknown"})
}

func TestUnsafeRecoveryPolicyRejected(t *testing.T) {
	_, e, _ := fixture(t, 1)
	e.cfg.RecoveryPolicy = "retry"
	if err := e.Recover(context.Background()); err == nil {
		t.Fatal("unsafe recovery accepted")
	}
	if err := e.RunCampaign(context.Background(), 1); err == nil {
		t.Fatal("unsafe campaign configuration accepted")
	}
	if Classify(errors.New("dropped connection")) != Ambiguous {
		t.Fatal("network uncertainty classification changed")
	}
}

func TestUncertainHistoryBlocksNewAttempt(t *testing.T) {
	for _, outcome := range []string{"started", "unknown", "sent"} {
		t.Run(outcome, func(t *testing.T) {
			db, e, _ := fixture(t, 1)
			ctx := context.Background()
			if err := e.ensureDispatchLedger(ctx); err != nil {
				t.Fatal(err)
			}
			c, err := e.prepare(ctx, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.snapshot(ctx, c); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Writer.Exec(`UPDATE campaigns SET status='sending'; UPDATE campaign_recipients SET status='claimed',attempts=1`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Writer.Exec(`INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(1,1,1,1,?)`, outcome); err != nil {
				t.Fatal(err)
			}
			ok, err := e.beginAttempt(ctx, c, testAttemptRecipient(t, db, 1), 2)
			if ok || !errors.Is(err, errUnsafeAttempt) {
				t.Fatalf("new attempt allowed: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestConcurrentAttemptInitiation(t *testing.T) {
	db, e, _ := fixture(t, 1)
	ctx := context.Background()
	if err := e.ensureDispatchLedger(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`UPDATE campaigns SET status='sending'; UPDATE campaign_recipients SET status='claimed'`); err != nil {
		t.Fatal(err)
	}
	other := New(db, e.sender, e.cfg)
	results := make(chan bool, 2)
	recipient := testAttemptRecipient(t, db, 1)
	for _, engine := range []*Engine{e, other} {
		go func(engine *Engine) {
			ok, _ := engine.beginAttempt(ctx, c, recipient, 1)
			results <- ok
		}(engine)
	}
	winners := 0
	for i := 0; i < 2; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("attempt winners=%d", winners)
	}
}

func TestDeliberateTestsDeduplicateAndLabel(t *testing.T) {
	_, e, _ := fixture(t, 1)
	s := &policySender{}
	e.sender = s
	addresses := []string{" Reader@EXAMPLE.org ", "reader@example.org"}
	for i := 0; i < 2; i++ {
		if err := e.TestSend(context.Background(), 1, addresses); err != nil {
			t.Fatal(err)
		}
	}
	if s.calls != 2 {
		t.Fatalf("deliberate tests or within-request dedup changed: calls=%d", s.calls)
	}
	for _, r := range s.requests {
		if r.To != "reader@example.org" || !strings.Contains(string(r.Raw), "Subject: [Test] Hello") {
			t.Fatalf("test request: to=%s raw=%s", r.To, r.Raw)
		}
	}
	if err := e.TestSend(context.Background(), 1, []string{"reader@example.org", "invalid"}); err == nil {
		t.Fatal("invalid address accepted")
	}
	if s.calls != 2 {
		t.Fatal("validation sent before rejecting invalid address")
	}
}

func TestCampaignDispatchLedgerDeduplicatesSnapshotEmails(t *testing.T) {
	db, e, f := fixture(t, 2)
	ctx := context.Background()
	c, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`UPDATE campaign_recipients SET snapshot_email='reader1@example.org' WHERE campaign_id=1`); err != nil {
		t.Fatal(err)
	}
	if err = e.RunCampaign(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if f.Accepted.Load() != 1 || f.Recipients["reader1@example.org"] != 1 {
		t.Fatalf("duplicate snapshot email sent: accepted=%d recipients=%v", f.Accepted.Load(), f.Recipients)
	}
}
