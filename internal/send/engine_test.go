package send

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func fixture(t testing.TB, n int) (*store.DB, *Engine, *Fake) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seedFixture(t, db, n)
	f := NewFake(FakeConfig{Rate: 10000, Latency: time.Millisecond})
	e := New(db, f, Config{MaxWorkers: 16, ClaimBatch: 20, TokenSecret: []byte("test-secret-longer-than-32-bytes"), BaseURL: "https://track.example.org", FlushInterval: 10 * time.Millisecond})
	return db, e, f
}
func seedFixture(t testing.TB, db *store.DB, n int) {
	t.Helper()
	err := db.Immediate(context.Background(), func(c *sql.Conn) error {
		for _, q := range []string{`INSERT INTO brands(id,name,from_name,from_email,tracking_domain,created_at) VALUES(1,'Test','Sender','sender@example.org','track.example.org',1)`, `INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list-1','All','single',1)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,inline_css,created_at,updated_at) VALUES(1,1,'campaign-1','draft','Hello [Name]','<html><body>Hello [Name] <a href="https://example.org">Read</a><a href="[unsubscribe]">Unsubscribe</a></body></html>',0,1,1)`, `INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',1,'include')`} {
			if _, err := c.ExecContext(context.Background(), q); err != nil {
				return err
			}
		}
		for i := 1; i <= n; i++ {
			if _, err := c.ExecContext(context.Background(), `INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(?,1,?,'Test',1,1)`, i, fmt.Sprintf("reader%d@example.org", i)); err != nil {
				return err
			}
			if _, err := c.ExecContext(context.Background(), `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,?,'subscribed',1,1)`, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestPipelineHTTPAndSnapshot(t *testing.T) {
	db, e, f := fixture(t, 100)
	f.Config.RejectEvery = 13
	server := httptest.NewServer(f.Handler())
	defer server.Close()
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fake", "fake", ""), HTTPClient: server.Client(), Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 1; o.RateLimiter = ratelimit.None })
	}}
	e.sender = &AWS{Client: sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })}
	estimate, err := e.Estimate(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Recipients != 100 || !estimate.FitsDailyQuota {
		t.Fatalf("estimate %+v", estimate)
	}
	var frozen int
	if err = db.Reader.QueryRow(`SELECT count(*) FROM campaign_preparations`).Scan(&frozen); err != nil || frozen != 0 {
		t.Fatalf("estimate froze %d preparations: %v", frozen, err)
	}
	if err = e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	m := e.Metrics()
	if m.Sent != 93 || m.Failed != 7 {
		t.Fatalf("metrics %+v", m)
	}
	var sent, failed, attempts int
	_ = db.Reader.QueryRow(`SELECT sent,failed FROM campaign_stats WHERE campaign_id=1`).Scan(&sent, &failed)
	_ = db.Reader.QueryRow(`SELECT count(*) FROM send_attempts WHERE outcome IN ('sent','failed')`).Scan(&attempts)
	if sent != 93 || failed != 7 || attempts != 100 {
		t.Fatalf("counters %d %d attempts %d", sent, failed, attempts)
	}
	for email, count := range f.Recipients {
		if count != 1 {
			t.Fatalf("duplicate %s: %d", email, count)
		}
	}
}
func TestRecoveryAndSkip(t *testing.T) {
	db, e, f := fixture(t, 30)
	c, err := e.prepare(context.Background(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`UPDATE campaign_recipients SET status='claimed' WHERE subscriber_id<=5`); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`UPDATE list_subscribers SET status='unsubscribed' WHERE subscriber_id=6`); err != nil {
		t.Fatal(err)
	}
	if err = e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var unknown, skipped int
	_ = db.Reader.QueryRow(`SELECT unknown,skipped FROM campaign_stats WHERE campaign_id=1`).Scan(&unknown, &skipped)
	if unknown != 5 || skipped != 1 || f.Accepted.Load() != 24 {
		t.Fatalf("unknown=%d skipped=%d accepted=%d", unknown, skipped, f.Accepted.Load())
	}
}
func TestTimeoutUnknownAndThrottleRetry(t *testing.T) {
	db, e, f := fixture(t, 20)
	f.Config.TimeoutEvery = 7
	f.Config.Timeout = time.Millisecond
	f.Config.ThrottleEvery = 5
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var unknown, started int
	_ = db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE status='unknown'`).Scan(&unknown)
	_ = db.Reader.QueryRow(`SELECT count(*) FROM send_attempts WHERE outcome='started'`).Scan(&started)
	if unknown == 0 || started != 0 {
		t.Fatalf("unknown=%d unfinished=%d", unknown, started)
	}
	for to, count := range f.Recipients {
		if count > 1 {
			t.Fatalf("ambiguous acceptance resent %s", to)
		}
	}
	if e.Metrics().Errors["throttled"] == 0 {
		t.Fatal("throttle was not recorded")
	}
}
func TestPauseResumeCancelKillSwitch(t *testing.T) {
	db, e, f := fixture(t, 80)
	f.Config.Latency = 5 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- e.RunCampaign(context.Background(), 1) }()
	deadline := time.Now().Add(3 * time.Second)
	for f.Calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := e.Pause(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var claimed int
	_ = db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE status='claimed'`).Scan(&claimed)
	if claimed != 0 {
		t.Fatalf("claimed after drain %d", claimed)
	}
	if err := e.Resume(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if f.Accepted.Load() != 80 {
		t.Fatalf("pause/resume lost recipients: %d", f.Accepted.Load())
	}
	if err := e.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := e.TestSend(context.Background(), 1, []string{"a@example.org"}); err == nil {
		t.Fatal("kill switch allowed test send")
	}
	for to, count := range f.Recipients {
		if count > 1 {
			t.Fatalf("duplicate after resume %s", to)
		}
	}
}
func TestLimiterAIMD(t *testing.T) {
	_, e, _ := fixture(t, 1)
	if err := e.setQuota(Quota{MaxSendRate: 175}); err != nil {
		t.Fatal(err)
	}
	if e.limiter.Burst() > 175 {
		t.Fatal("more than one second burst")
	}
	e.adjust(true)
	if float64(e.limiter.Limit()) >= 175 {
		t.Fatal("no multiplicative decrease")
	}
	for i := 0; i < 100; i++ {
		e.adjust(false)
	}
	if float64(e.limiter.Limit()) != 175 || e.limiter.Burst() > 175 {
		t.Fatal("additive recovery failed")
	}
}

type crashSender struct{}

func (crashSender) GetAccount(context.Context) (Quota, error) { return Quota{100, 10000, 0}, nil }
func (crashSender) Send(_ context.Context, r Request) (string, error) {
	_ = os.WriteFile(os.Getenv("POSTBOX_TEST_CRASH_DB")+".accepted", []byte(r.To), 0600)
	os.Exit(17)
	return "", nil
}
func TestCrashChild(t *testing.T) {
	path := os.Getenv("POSTBOX_TEST_CRASH_DB")
	if path == "" {
		return
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := New(db, crashSender{}, Config{MaxWorkers: 1, ClaimBatch: 10, TokenSecret: []byte("long-enough-test-secret-value")})
	_ = e.RunCampaign(context.Background(), 1)
	t.Fatal("child did not crash")
}
func TestProcessCrashDefaultDoesNotResend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedFixture(t, db, 10)
	db.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$")
	cmd.Env = append(os.Environ(), "POSTBOX_TEST_CRASH_DB="+path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("child did not crash: %s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 17 {
		t.Fatalf("unexpected child exit %v %s", err, out)
	}
	if data, err := os.ReadFile(path + ".accepted"); err != nil || len(data) == 0 {
		t.Fatalf("no acceptance before crash: %v", err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := NewFake(FakeConfig{Rate: 100})
	e := New(db, f, Config{TokenSecret: []byte("long-enough-test-secret-value")})
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var unknown int
	_ = db.Reader.QueryRow(`SELECT unknown FROM campaign_stats WHERE campaign_id=1`).Scan(&unknown)
	if unknown != 10 || f.Calls.Load() != 0 {
		t.Fatalf("unknown=%d resent=%d", unknown, f.Calls.Load())
	}
}
func BenchmarkResultsWriter(b *testing.B) {
	db, e, _ := fixture(b, 500)
	c, err := e.prepare(context.Background(), 1, true)
	if err != nil {
		b.Fatal(err)
	}
	if err = e.snapshot(context.Background(), c); err != nil {
		b.Fatal(err)
	}
	batch := make([]result, 500)
	for i := range batch {
		batch[i] = result{id: int64(i + 1), status: "sent", messageID: fmt.Sprintf("benchmark-%d", i), at: time.Now().UnixMilli()}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err = db.Writer.Exec(`UPDATE campaign_recipients SET status='claimed',message_id=NULL`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err = e.writeResults(context.Background(), 1, batch); err != nil {
			b.Fatal(err)
		}
	}
}
func TestCrossBrandAndExcludedSnapshot(t *testing.T) {
	db, e, _ := fixture(t, 8)
	_, err := db.Writer.Exec(`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(2,1,'exclude','Exclude','single',1); INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(2,1,'subscribed',1,1); INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',2,'exclude'); INSERT INTO suppressions(kind,value,reason,created_at) VALUES('email','reader2@example.org','manual',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if e.Metrics().Sent != 6 {
		t.Fatalf("excluded/suppressed were sent: %+v", e.Metrics())
	}
	_, err = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(2,'Other','Other','other@example.org',1); INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(3,2,'foreign','Other','single',1); UPDATE campaign_targets SET target_id=3 WHERE target_id=1`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Estimate(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "another brand") {
		t.Fatalf("foreign list estimate error %v", err)
	}
}

type blockingAccount struct {
	Sender
	entered, release chan struct{}
}

func (s blockingAccount) GetAccount(ctx context.Context) (Quota, error) {
	close(s.entered)
	select {
	case <-ctx.Done():
		return Quota{}, ctx.Err()
	case <-s.release:
		return s.Sender.GetAccount(ctx)
	}
}
func TestControlDuringPreparationDoesNotResurrect(t *testing.T) {
	for _, control := range []string{"pause", "cancel", "kill"} {
		t.Run(control, func(t *testing.T) {
			db, e, f := fixture(t, 10)
			entered, release := make(chan struct{}), make(chan struct{})
			e.sender = blockingAccount{f, entered, release}
			done := make(chan error, 1)
			go func() { done <- e.RunCampaign(context.Background(), 1) }()
			<-entered
			var err error
			expected := "paused"
			switch control {
			case "pause":
				err = e.Pause(context.Background(), 1)
			case "cancel":
				err = e.Cancel(context.Background(), 1)
				expected = "cancelled"
			case "kill":
				err = e.SetEnabled(context.Background(), false)
			}
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			if err = <-done; err == nil {
				t.Fatal("interrupted campaign ran")
			}
			var status string
			_ = db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status)
			if control == "kill" && status == "draft" {
				expected = "draft"
			}
			if status != expected || f.Calls.Load() != 0 {
				t.Fatalf("status=%s expected=%s calls=%d", status, expected, f.Calls.Load())
			}
		})
	}
}
func TestCancelSkipsUnsent(t *testing.T) {
	db, e, f := fixture(t, 80)
	f.Config.Latency = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- e.RunCampaign(context.Background(), 1) }()
	for f.Calls.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	if err := e.Cancel(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var status string
	var pending, claimed, sent, skipped int
	_ = db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status)
	_ = db.Reader.QueryRow(`SELECT sum(status='pending'),sum(status='claimed'),sum(status='sent'),sum(status='skipped') FROM campaign_recipients`).Scan(&pending, &claimed, &sent, &skipped)
	if status != "cancelled" || pending != 0 || claimed != 0 || sent+skipped != 80 || skipped == 0 {
		t.Fatalf("status=%s p=%d c=%d sent=%d skipped=%d", status, pending, claimed, sent, skipped)
	}
}
func TestListSpecificConsentAtAttempt(t *testing.T) {
	db, e, _ := fixture(t, 1)
	_, err := db.Writer.Exec(`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(2,1,'other-list','Other','single',1); INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(2,1,'subscribed',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.prepare(context.Background(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	items, err := e.claim(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.audience, c.audienceArgs, err = e.audienceSQL(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Writer.Exec(`UPDATE campaigns SET status='sending' WHERE id=1; UPDATE list_subscribers SET status='unsubscribed' WHERE list_id=1`)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := e.beginAttempt(context.Background(), c, items[0], 1)
	if err != nil || ok {
		t.Fatalf("send consent=%v err=%v", ok, err)
	}
}
func TestHTTPAmbiguousAcceptedNotRetried(t *testing.T) {
	_, e, f := fixture(t, 10)
	f.Config.TimeoutEvery = 3
	f.Config.Timeout = time.Millisecond
	server := httptest.NewServer(f.Handler())
	defer server.Close()
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fake", "fake", ""), HTTPClient: server.Client(), Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 1; o.RateLimiter = ratelimit.None })
	}}
	e.sender = &AWS{Client: sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })}
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if e.Metrics().Unknown != 3 || f.Accepted.Load() != 10 {
		t.Fatalf("metrics %+v accepted %d", e.Metrics(), f.Accepted.Load())
	}
	for to, n := range f.Recipients {
		if n != 1 {
			t.Fatalf("duplicate accepted HTTP recipient %s: %d", to, n)
		}
	}
}
func TestWriterFailureRetainsAcceptedResults(t *testing.T) {
	db, e, f := fixture(t, 30)
	if _, err := db.Writer.Exec(`CREATE TRIGGER fail_sent BEFORE UPDATE OF status ON campaign_recipients WHEN NEW.status='sent' BEGIN SELECT RAISE(ABORT,'injected writer failure'); END`); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(100 * time.Millisecond); _, _ = db.Writer.Exec(`DROP TRIGGER fail_sent`) }()
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(context.Background(), 1); err != nil && !strings.Contains(err.Error(), "campaign is sent") {
		t.Fatal(err)
	}
	var sent, claimed int
	_ = db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE status='sent'`).Scan(&sent)
	_ = db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE status='claimed'`).Scan(&claimed)
	if sent != 30 || claimed != 0 || f.Accepted.Load() != 30 {
		t.Fatalf("sent=%d claimed=%d accepted=%d", sent, claimed, f.Accepted.Load())
	}
}
