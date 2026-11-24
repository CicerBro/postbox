package send

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestLowerGmailRateHonorsLastUncappedAttempt(t *testing.T) {
	db, e, _ := fixture(t, 0)
	ctx := context.Background()
	reserve := func(at int64) error {
		return db.Immediate(ctx, func(conn *sql.Conn) error {
			return reserveGmailAttempt(ctx, conn, at)
		})
	}
	const last = 100_000
	if err := reserve(last); err != nil {
		t.Fatal(err)
	}
	if err := e.SetGmailSendsPerMinute(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := reserve(last + 59_999); !errors.Is(err, errGmailPaced) {
		t.Fatalf("early attempt at the new rate: %v", err)
	}
	if err := reserve(last + 60_000); err != nil {
		t.Fatalf("attempt after one minute: %v", err)
	}
}

func TestGmailPacingDoesNotHoldOtherRecipientsAndPauses(t *testing.T) {
	db, e, fake := fixture(t, 3)
	if _, err := db.Writer.Exec(`UPDATE subscribers SET email=CASE id
		WHEN 1 THEN 'first@gmail.com' WHEN 2 THEN 'second@GOOGLEMAIL.COM' ELSE 'other@example.org' END`); err != nil {
		t.Fatal(err)
	}
	e.cfg.MaxWorkers = 1
	ctx := context.Background()
	if err := e.SetGmailSendsPerMinute(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// Simulate a recent attempt before this run. Even with one worker, the
	// non-Gmail recipient must go out while both Gmail recipients wait.
	if err := db.SetSetting(ctx, gmailLastAttemptKey, strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.RunCampaign(runCtx, 1) }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for fake.Accepted.Load() == 0 {
		select {
		case <-runCtx.Done():
			t.Fatal("non-Gmail recipient was blocked")
		case <-ticker.C:
		}
	}
	if err := e.Pause(ctx, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-runCtx.Done():
		t.Fatal("pause did not interrupt Gmail pacing")
	}
	if fake.Accepted.Load() != 1 || fake.Recipients["other@example.org"] != 1 {
		t.Fatalf("unexpected submissions: %v", fake.Recipients)
	}
	var pending int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE campaign_id=1 AND status='pending' AND attempts=0`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending recipients: %d, %v", pending, err)
	}
	if err := e.SetGmailSendsPerMinute(ctx, 240); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil || status != "paused" {
		t.Fatalf("campaign status: %q, %v", status, err)
	}
	if err := e.Resume(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(runCtx, 1); err != nil || fake.Accepted.Load() != 3 {
		t.Fatalf("resume did not use the new rate: accepted=%d, %v", fake.Accepted.Load(), err)
	}
}

func TestGmailAndGooglemailSharePacing(t *testing.T) {
	db, e, fake := fixture(t, 2)
	if _, err := db.Writer.Exec(`UPDATE subscribers SET email=CASE id WHEN 1 THEN 'first@gmail.com' ELSE 'second@GOOGLEMAIL.COM' END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.SetGmailSendsPerMinute(ctx, 240); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var spacing, last int64
	if err := db.Reader.QueryRow(`SELECT max(started_at)-min(started_at),max(started_at) FROM send_attempts WHERE campaign_id=1`).Scan(&spacing, &last); err != nil {
		t.Fatal(err)
	}
	if fake.Accepted.Load() != 2 || spacing < 250 {
		t.Fatalf("accepted=%d, spacing=%dms", fake.Accepted.Load(), spacing)
	}
	var persisted string
	if err := db.Reader.QueryRow(`SELECT value FROM settings WHERE key=?`, gmailLastAttemptKey).Scan(&persisted); err != nil || persisted != strconv.FormatInt(last, 10) {
		t.Fatalf("last Gmail attempt was not persisted: %q, %v", persisted, err)
	}
}

func TestGmailPacingChangesWhileCampaignIsSending(t *testing.T) {
	db, e, fake := fixture(t, 3)
	if _, err := db.Writer.Exec(`UPDATE subscribers SET email=CASE id
		WHEN 1 THEN 'first@gmail.com' WHEN 2 THEN 'second@googlemail.com' ELSE 'other@example.org' END;
		UPDATE campaigns SET status='sending' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := e.SetGmailSendsPerMinute(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, gmailLastAttemptKey, strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Start(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil && err != context.Canceled {
			t.Error(err)
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for fake.Accepted.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("other recipient did not send")
		case <-tick.C:
		}
	}
	if fake.Accepted.Load() != 1 {
		t.Fatal("Gmail messages bypassed the initial send limit")
	}
	if err := e.SetGmailSendsPerMinute(ctx, 0); err != nil {
		t.Fatal(err)
	}
	for {
		var status string
		if err := db.Reader.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "sent" {
			break
		}
		if status != "sending" {
			t.Fatalf("changing the limit unexpectedly changed campaign status to %q", status)
		}
		select {
		case <-ctx.Done():
			t.Fatal("active campaign did not use the new rate")
		case <-tick.C:
		}
	}
	if fake.Accepted.Load() != 3 {
		t.Fatalf("received %d sends, wanted 3", fake.Accepted.Load())
	}
}
