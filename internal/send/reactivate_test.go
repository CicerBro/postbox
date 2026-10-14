package send

import (
	"context"
	"errors"
	"testing"
)

func TestReactivateAfterSendingDisabled(t *testing.T) {
	db, e, f := fixture(t, 3)
	ctx := context.Background()
	if err := e.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE campaigns SET status='scheduled',scheduled_at=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(ctx, 1); err == nil {
		t.Fatal("sending ran while disabled")
	}
	// Include a prepared audience to verify that cancellation's skipped rows
	// do not carry into the next send.
	c, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.snapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := e.Cancel(ctx, 1); err != nil {
		t.Fatal(err)
	}
	allowed, err := e.CanReactivate(ctx, 1)
	if err != nil || !allowed {
		t.Fatalf("can reactivate=%v err=%v", allowed, err)
	}
	if err := e.Reactivate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var status, identity, subject string
	var scheduled, started, finished any
	if err := db.Reader.QueryRow(`SELECT status,tracking_identity,subject,scheduled_at,started_at,finished_at FROM campaigns WHERE id=1`).Scan(&status, &identity, &subject, &scheduled, &started, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || identity != c.trackingIdentity || subject != c.subject || scheduled != nil || started != nil || finished != nil {
		t.Fatalf("draft state: %s identity=%s subject=%s timestamps=%v/%v/%v", status, identity, subject, scheduled, started, finished)
	}
	for _, table := range []string{"campaign_preparations", "campaign_recipients", "campaign_stats", "links", "campaign_lifecycle_events"} {
		var count int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table + " WHERE campaign_id=1").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	if f.Calls.Load() != 0 {
		t.Fatal("reactivation sent mail")
	}
	if err := e.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCampaign(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if f.Accepted.Load() != 3 {
		t.Fatalf("accepted=%d; saved audience was not sent", f.Accepted.Load())
	}
}

func TestReactivateRejectsDeliveryEvidence(t *testing.T) {
	for name, evidence := range map[string]string{
		"sent counter":             `INSERT INTO campaign_stats(campaign_id,sent) VALUES(1,1)`,
		"unknown counter":          `INSERT INTO campaign_stats(campaign_id,unknown) VALUES(1,1)`,
		"sent timestamp":           `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,sent_at) VALUES(1,1,'skipped',1)`,
		"claimed recipient":        `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'claimed')`,
		"unknown recipient":        `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'unknown')`,
		"active attempt":           `INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(1,1,1,1,'started')`,
		"sent attempt":             `INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(1,1,1,1,'sent')`,
		"erased subscriber ledger": `INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(1,zeroblob(32),'sent',1,999,1,1)`,
		"uncertain ledger":         `INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(1,zeroblob(32),'unknown',1,999,1,1)`,
	} {
		t.Run(name, func(t *testing.T) {
			db, e, _ := fixture(t, 1)
			ctx := context.Background()
			if err := e.Cancel(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Writer.Exec(evidence); err != nil {
				t.Fatal(err)
			}
			if allowed, err := e.CanReactivate(ctx, 1); err != nil || allowed {
				t.Fatalf("can reactivate=%v err=%v", allowed, err)
			}
			if err := e.Reactivate(ctx, 1); !errors.Is(err, ErrCannotReactivate) {
				t.Fatalf("reactivate err=%v", err)
			}
			var status string
			if err := db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil || status != "cancelled" {
				t.Fatalf("status=%s err=%v", status, err)
			}
		})
	}
}

func TestReactivateRejectsActiveRunAndNonCancelledCampaign(t *testing.T) {
	_, e, _ := fixture(t, 1)
	ctx := context.Background()
	if err := e.Reactivate(ctx, 1); !errors.Is(err, ErrCannotReactivate) {
		t.Fatalf("draft reactivated: %v", err)
	}
	if err := e.Cancel(ctx, 1); err != nil {
		t.Fatal(err)
	}
	e.running[1] = func() {}
	if allowed, err := e.CanReactivate(ctx, 1); err != nil || allowed {
		t.Fatalf("active run can reactivate=%v err=%v", allowed, err)
	}
	if err := e.Reactivate(ctx, 1); !errors.Is(err, ErrCannotReactivate) {
		t.Fatalf("active run reactivated: %v", err)
	}
}
