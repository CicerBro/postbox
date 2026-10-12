package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestDeleteCampaignFencesStateAndRemovesOwnedWork(t *testing.T) {
	for _, status := range []string{"draft", "sent", "cancelled", "scheduled", "preparing", "sending", "paused"} {
		t.Run(status, func(t *testing.T) {
			db := testDB(t)
			seedDeletionAudiences(t, db, status)
			for _, query := range []string{
				`INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(1,1,0)`,
				`INSERT INTO clicks(campaign_id,link_id,subscriber_id,at) VALUES(1,1,1,0)`,
				`INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('campaign','{"campaign_id":1}','pending',0,0),('email_notification','{"brand_id":1,"data":{"campaign_id":1}}','pending',0,0),('outbound_webhook','{"body":{"brand_id":1,"data":{"campaign_id":1}}}','pending',0,0),('maintenance','{}','pending',0,0)`,
			} {
				if _, err := db.Writer.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if err := db.DeleteCampaign(ctx, 1, 1, "outdated"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("stale state was not rejected: %v", err)
			}
			err := db.DeleteCampaign(ctx, 1, 1, status)
			deletable := status == "draft" || status == "sent" || status == "cancelled"
			if deletable && err != nil {
				t.Fatal(err)
			}
			if !deletable && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("active campaign deletion was not rejected: %v", err)
			}
			for _, table := range []string{"campaigns", "opens", "clicks", "jobs"} {
				want := 1
				if table == "jobs" {
					if !deletable {
						want = 4
					}
				} else if deletable {
					want = 0
				}
				var count int
				if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
					t.Fatalf("%s: got %d want %d err=%v", table, count, want, err)
				}
			}
		})
	}
}

func TestDeleteSegmentHonorsAudienceProtectionAndScope(t *testing.T) {
	for _, status := range []string{"draft", "sending"} {
		t.Run(status, func(t *testing.T) {
			db := testDB(t)
			seedDeletionAudiences(t, db, status)
			ctx := context.Background()
			if err := db.DeleteSegment(ctx, 2, 1); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("cross-brand delete: %v", err)
			}
			err := db.DeleteSegment(ctx, 1, 1)
			if status == "sending" && err == nil {
				t.Fatal("deleted active audience")
			}
			if status == "draft" && err != nil {
				t.Fatal(err)
			}
			var targets int
			if err := db.Reader.QueryRow("SELECT count(*) FROM campaign_targets").Scan(&targets); err != nil {
				t.Fatal(err)
			}
			want := 2
			if status == "draft" {
				want = 1
			}
			if targets != want {
				t.Fatalf("target cleanup: got %d want %d", targets, want)
			}
		})
	}
}
