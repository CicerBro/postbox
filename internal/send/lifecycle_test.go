package send

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"postbox/internal/jobs"
)

func attachLifecycleJobs(t *testing.T, e *Engine) {
	t.Helper()
	r := jobs.New(e.db, jobs.Config{})
	if err := r.RegisterNotifications(func(context.Context, jobs.Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.cfg.EventHook = r.EmitTx
	if _, err := e.db.Writer.Exec(`INSERT INTO webhooks(brand_id,url,events,secret) VALUES(1,'https://example.org/hook','["campaign_started","campaign_finished"]','secret')`); err != nil {
		t.Fatal(err)
	}
}

func lifecycleCount(t *testing.T, e *Engine, queue, event string) int {
	t.Helper()
	var n int
	field := "$.type"
	if queue == "outbound_webhook" {
		field = "$.body.type"
	}
	if err := e.db.Reader.QueryRow(`SELECT count(*) FROM jobs WHERE queue=? AND json_extract(payload,?)=?`, queue, field, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLifecyclePauseResumeEmitsOnce(t *testing.T) {
	_, e, f := fixture(t, 80)
	attachLifecycleJobs(t, e)
	f.Config.Latency = 10 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- e.RunCampaign(context.Background(), 1) }()
	deadline := time.Now().Add(3 * time.Second)
	for f.Calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.Calls.Load() == 0 {
		t.Fatal("campaign did not begin")
	}
	if err := e.Pause(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, queue := range []string{"outbound_webhook", "email_notification"} {
		if n := lifecycleCount(t, e, queue, "campaign_started"); n != 1 {
			t.Fatalf("%s starts=%d", queue, n)
		}
		if n := lifecycleCount(t, e, queue, "campaign_finished"); n != 0 {
			t.Fatalf("%s finishes while paused=%d", queue, n)
		}
	}
	if err := e.Resume(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Cancel(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for _, queue := range []string{"outbound_webhook", "email_notification"} {
		for _, event := range []string{"campaign_started", "campaign_finished"} {
			if n := lifecycleCount(t, e, queue, event); n != 1 {
				t.Fatalf("%s %s=%d", queue, event, n)
			}
		}
	}
}

func TestLifecycleCancellationIsAtomicAndIdempotent(t *testing.T) {
	db, e, _ := fixture(t, 1)
	attachLifecycleJobs(t, e)
	hook := e.cfg.EventHook
	e.cfg.EventHook = func(ctx context.Context, c *sql.Conn, brand int64, event string, data any) error {
		if err := hook(ctx, c, brand, event, data); err != nil {
			return err
		}
		return errors.New("injected enqueue failure")
	}
	if err := e.Cancel(context.Background(), 1); err == nil {
		t.Fatal("cancel accepted failed event enqueue")
	}
	var status string
	if err := db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	var markers, queued int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_lifecycle_events`).Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRow(`SELECT count(*) FROM jobs`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if markers != 0 || queued != 0 {
		t.Fatalf("rollback left markers=%d jobs=%d", markers, queued)
	}
	e.cfg.EventHook = hook
	for i := 0; i < 2; i++ {
		if err := e.Cancel(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, queue := range []string{"outbound_webhook", "email_notification"} {
		if n := lifecycleCount(t, e, queue, "campaign_started"); n != 0 {
			t.Fatalf("cancelled draft emitted %d starts", n)
		}
		if n := lifecycleCount(t, e, queue, "campaign_finished"); n != 1 {
			t.Fatalf("%s finishes=%d", queue, n)
		}
	}
}

func TestLifecycleStartRollsBackWithEnqueue(t *testing.T) {
	db, e, f := fixture(t, 1)
	e.cfg.EventHook = func(context.Context, *sql.Conn, int64, string, any) error { return errors.New("enqueue failed") }
	if err := e.RunCampaign(context.Background(), 1); err == nil {
		t.Fatal("campaign ran with failed lifecycle enqueue")
	}
	var status string
	var started sql.NullInt64
	if err := db.Reader.QueryRow(`SELECT status,started_at FROM campaigns WHERE id=1`).Scan(&status, &started); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || started.Valid || f.Calls.Load() != 0 {
		t.Fatalf("status=%s started=%v calls=%d", status, started, f.Calls.Load())
	}
	e.cfg.EventHook = nil
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
}
