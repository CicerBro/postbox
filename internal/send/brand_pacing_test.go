package send

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrandAndGmailCampaignPacing(t *testing.T) {
	db, e, fake := fixture(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.Writer.Exec(`UPDATE brands SET sends_per_minute=300 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE subscribers SET email=CASE id
		WHEN 1 THEN 'first@gmail.com' WHEN 2 THEN 'second@googlemail.com' ELSE 'other@example.org' END`); err != nil {
		t.Fatal(err)
	}
	if err := e.SetGmailSendsPerMinute(ctx, 150); err != nil {
		t.Fatal(err)
	}
	if estimate, err := e.Estimate(ctx, 1); err != nil || estimate.DurationSeconds != 0.8 {
		t.Fatalf("estimate = %+v, %v", estimate, err)
	}
	if err := e.RunCampaign(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var brandSpacing, gmailSpacing int64
	if err := db.Reader.QueryRow(`WITH attempts AS (
		SELECT started_at, s.email,
			started_at-lag(started_at) OVER (ORDER BY started_at) AS spacing
		FROM send_attempts a JOIN subscribers s ON s.id=a.subscriber_id WHERE a.campaign_id=1
	)
	SELECT min(spacing), (SELECT max(started_at)-min(started_at)
		FROM attempts WHERE `+gmailRecipientSQL("email")+`) FROM attempts`).Scan(&brandSpacing, &gmailSpacing); err != nil {
		t.Fatal(err)
	}
	if fake.Accepted.Load() != 3 || brandSpacing < 200 || gmailSpacing < 400 {
		t.Fatalf("sent=%d, shortest brand interval=%dms, Gmail interval=%dms", fake.Accepted.Load(), brandSpacing, gmailSpacing)
	}
}

func TestBrandPacingSharesHistoryAcrossSendPaths(t *testing.T) {
	db, e, fake := fixture(t, 1)
	if _, err := db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at)
		VALUES(2,'Other brand','Sender','sender@example.org',1)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.TestCampaign(ctx, 1, []string{"test@example.org"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE brands SET sends_per_minute=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelWait()
	if _, err := e.SendNotification(waitCtx, Request{BrandID: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("notification should wait after the test send: %v", err)
	}
	if _, err := e.SendNotification(ctx, Request{BrandID: 2}); err != nil {
		t.Fatalf("other brand could not send: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- e.RunCampaign(ctx, 1) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := db.Reader.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "sending" {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("campaign did not wait for the brand cap: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if fake.Accepted.Load() != 2 {
		t.Fatal("campaign sent before the brand cap was cleared")
	}
	if _, err := db.Writer.Exec(`UPDATE brands SET sends_per_minute=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || fake.Accepted.Load() != 3 {
		t.Fatalf("active campaign did not use the new cap: sent=%d, %v", fake.Accepted.Load(), err)
	}
}
