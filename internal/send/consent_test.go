package send

import (
	"context"
	"postbox/internal/subscribers"
	"testing"
)

func TestBrandOptOutSelectionClaimAndResume(t *testing.T) {
	for _, kind := range []string{"list", "segment", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db, e, f := fixture(t, 1)
			if _, err := db.Writer.Exec(`INSERT INTO segments(id,brand_id,name,rules,created_at) VALUES(1,1,'All','{"field":"email","op":"eq","value":"reader1@example.org"}',1),(2,1,'Excluded','{"field":"email","op":"eq","value":"other@other.org"}',1)`); err != nil {
				t.Fatal(err)
			}
			if kind == "segment" {
				if _, err := db.Writer.Exec(`DELETE FROM campaign_targets WHERE campaign_id=1`); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "list" {
				if _, err := db.Writer.Exec(`INSERT INTO campaign_targets VALUES(1,'segment',1,'include'),(1,'segment',2,'exclude')`); err != nil {
					t.Fatal(err)
				}
			}
			c, err := e.prepare(ctx, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.snapshot(ctx, c); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Writer.Exec(`UPDATE campaigns SET status='sending' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			items, err := e.claim(ctx, 1, 0)
			if err != nil || len(items) != 1 {
				t.Fatalf("claim %v %v", items, err)
			}
			// Consent changes after claim must be checked at the uncertainty boundary.
			if err = subscribers.New(db).Unsubscribe(ctx, 1, 1, 0); err != nil {
				t.Fatal(err)
			}
			c.audience, c.audienceArgs, err = e.audienceSQL(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			started, err := e.beginAttempt(ctx, c, items[0], 1)
			if err != nil || started {
				t.Fatalf("claimed opt-out dispatch %v %v", started, err)
			}
			estimate, err := e.Estimate(ctx, 1)
			if err != nil || estimate.Recipients != 0 {
				t.Fatalf("selection %+v %v", estimate, err)
			}
			if _, err = db.Writer.Exec(`UPDATE campaigns SET status='paused' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if err = e.Resume(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if err = e.RunCampaign(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if len(f.Recipients) != 0 {
				t.Fatalf("resumed campaign sent %+v", f.Recipients)
			}
			var attempts int
			if err = db.Reader.QueryRow(`SELECT count(*) FROM send_attempts`).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatalf("attempts %d %v", attempts, err)
			}
		})
	}
}

func TestPreflightCountsBrandConsentWithoutRewritingTargets(t *testing.T) {
	db, e, _ := fixture(t, 4)
	for _, q := range []string{
		`UPDATE subscribers SET status='bounced' WHERE id=1`,
		`UPDATE subscribers SET marketing_unsubscribed_at=2 WHERE id=2`,
		`INSERT INTO campaign_targets VALUES(1,'segment',1,'exclude')`,
	} {
		if q == `INSERT INTO campaign_targets VALUES(1,'segment',1,'exclude')` {
			if _, err := db.Writer.Exec(`INSERT INTO segments(id,brand_id,name,rules,created_at) VALUES(1,1,'exclude','{"field":"email","op":"eq","value":"reader4@example.org"}',1)`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Recipients != 1 || got.Checks.Inactive != 1 || got.Checks.Suppressed != 1 || got.Checks.Skipped != 2 {
		t.Fatalf("counts %+v", got)
	}
}
