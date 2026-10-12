package send

import (
	"context"
	"testing"

	"postbox/internal/store"
)

func testAttemptRecipient(t testing.TB, db *store.DB, id int64) recipient {
	t.Helper()
	r := recipient{ID: id}
	if err := db.Reader.QueryRow(`SELECT email,tracking_identity FROM subscribers WHERE id=?`, id).Scan(&r.Email, &r.TrackingIdentity); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStaleAttemptAndResultsCannotAffectReplacementIdentity(t *testing.T) {
	for _, replacement := range []string{"subscriber", "campaign"} {
		for _, audience := range []bool{false, true} {
			t.Run(replacement+map[bool]string{false: "/global", true: "/audience"}[audience], func(t *testing.T) {
				db, engine, _ := fixture(t, 1)
				ctx := context.Background()
				if err := engine.ensureDispatchLedger(ctx); err != nil {
					t.Fatal(err)
				}
				c, err := engine.prepare(ctx, 1, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.snapshot(ctx, c); err != nil {
					t.Fatal(err)
				}
				claimed, err := engine.claim(ctx, 1, 0)
				if err != nil || len(claimed) != 1 {
					t.Fatalf("claim=%v error=%v", claimed, err)
				}
				stale := claimed[0]
				staleCampaign := c
				if audience {
					c.audience, c.audienceArgs, err = engine.audienceSQL(ctx, c)
					if err != nil {
						t.Fatal(err)
					}
				}
				queries := []string{`UPDATE campaigns SET status='sending' WHERE id=1`}
				if replacement == "subscriber" {
					queries = append(queries,
						`DELETE FROM subscribers WHERE id=1`,
						`INSERT INTO subscribers(id,brand_id,email,name,created_at,updated_at) VALUES(1,1,'reader1@example.org','Replacement',1,1)`,
						`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',1,1)`,
					)
				} else {
					queries = append(queries,
						`DELETE FROM campaigns WHERE id=1`,
						`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign-1','sending','Replacement','Body',1,1)`,
						`INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',1,'include')`,
						`INSERT INTO campaign_stats(campaign_id) VALUES(1)`,
					)
				}
				queries = append(queries, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'claimed')`)
				for _, query := range queries {
					if _, err := db.Writer.Exec(query); err != nil {
						t.Fatal(err)
					}
				}
				if started, err := engine.beginAttempt(ctx, c, stale, 1); started {
					t.Fatalf("stale worker started replacement dispatch: %v", err)
				}
				for _, state := range []string{"pending", "sent", "unknown", "skipped"} {
					if err := engine.writeResults(ctx, 1, []result{{id: 1, status: state, attempt: 1, messageID: "stale", at: 1, campaignIdentity: c.trackingIdentity, subscriberIdentity: stale.TrackingIdentity}}); err != nil {
						t.Fatal(err)
					}
				}
				var status string
				var attempts int
				if err := db.Reader.QueryRow(`SELECT status,attempts FROM campaign_recipients WHERE campaign_id=1 AND subscriber_id=1`).Scan(&status, &attempts); err != nil || status != "claimed" || attempts != 0 {
					t.Fatalf("replacement modified: status=%s attempts=%d error=%v", status, attempts, err)
				}
				for _, table := range []string{"send_attempts", "campaign_dispatch_ledger", "send_hourly"} {
					var count int
					if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("stale worker created %s rows=%d error=%v", table, count, err)
					}
				}
				var stats int
				if err := db.Reader.QueryRow(`SELECT sent+failed+unknown+skipped FROM campaign_stats WHERE campaign_id=1`).Scan(&stats); err != nil || stats != 0 {
					t.Fatalf("stale worker changed stats=%d error=%v", stats, err)
				}
				// Both identities must be provided, including by internal callers.
				fresh := testAttemptRecipient(t, db, 1)
				if err := db.Reader.QueryRow(`SELECT tracking_identity FROM campaigns WHERE id=1`).Scan(&c.trackingIdentity); err != nil {
					t.Fatal(err)
				}
				missing := fresh
				missing.TrackingIdentity = ""
				if started, err := engine.beginAttempt(ctx, c, missing, 1); started || err == nil {
					t.Fatalf("missing recipient identity accepted: %v %v", started, err)
				}
				missingCampaign := c
				missingCampaign.trackingIdentity = ""
				if started, err := engine.beginAttempt(ctx, missingCampaign, fresh, 1); started || err == nil {
					t.Fatalf("missing campaign identity accepted: %v %v", started, err)
				}
				if started, err := engine.beginAttempt(ctx, c, fresh, 1); !started || err != nil {
					t.Fatalf("current replacement identity blocked: %v %v", started, err)
				}
				// An old network completion must not finish the replacement's new
				// attempt, even when its numeric attempt number is identical.
				if err := engine.finishAttempt(ctx, staleCampaign, stale, 1, "failed", "stale", "stale completion"); err != nil {
					t.Fatal(err)
				}
				var outcome string
				if err := db.Reader.QueryRow(`SELECT outcome FROM send_attempts WHERE campaign_id=1 AND subscriber_id=1 AND attempt=1`).Scan(&outcome); err != nil || outcome != "started" {
					t.Fatalf("stale completion finished new attempt: outcome=%s error=%v", outcome, err)
				}
				if err := db.Reader.QueryRow(`SELECT outcome FROM campaign_dispatch_ledger WHERE campaign_id=1`).Scan(&outcome); err != nil || outcome != "started" {
					t.Fatalf("stale completion finished new ledger reservation: outcome=%s error=%v", outcome, err)
				}
				if err := engine.finishAttempt(ctx, c, fresh, 1, "failed", "", "current completion"); err != nil {
					t.Fatal(err)
				}
				if err := db.Reader.QueryRow(`SELECT outcome FROM campaign_dispatch_ledger WHERE campaign_id=1`).Scan(&outcome); err != nil || outcome != "rejected" {
					t.Fatalf("current completion blocked: outcome=%s error=%v", outcome, err)
				}
			})
		}
	}
}
