package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func seedDeletionAudiences(t *testing.T, db *DB, status string) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.com',0)`,
		`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list','List','single',0)`,
		`INSERT INTO segments(id,brand_id,name,rules,created_at) VALUES(1,1,'Segment','{}',0)`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer.Exec(`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign',?,'Subject','<p>Hello</p>',0,0)`, status); err != nil {
		t.Fatal(err)
	}
	// List and segment IDs can coincide, so deleting one must preserve the other.
	if _, err := db.Writer.Exec(`INSERT INTO campaign_targets VALUES(1,'list',1,'include'),(1,'segment',1,'exclude')`); err != nil {
		t.Fatal(err)
	}
}

func TestAudienceDeletionCampaignStates(t *testing.T) {
	for _, kind := range []string{"list", "segment"} {
		for _, status := range []string{"draft", "scheduled", "preparing", "sending", "paused", "sent", "cancelled"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				db := testDB(t)
				seedDeletionAudiences(t, db, status)
				_, err := db.Writer.Exec("DELETE FROM " + kind + "s WHERE brand_id=1 AND id=1")
				active := status != "draft" && status != "sent" && status != "cancelled"
				if active {
					if err == nil || !strings.Contains(err.Error(), "Cancel the campaign") {
						t.Fatalf("expected active campaign protection, got %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				var targets, other, audiences int
				if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_targets WHERE target_type=?`, kind).Scan(&targets); err != nil {
					t.Fatal(err)
				}
				if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_targets WHERE target_type<>?`, kind).Scan(&other); err != nil {
					t.Fatal(err)
				}
				if err := db.Reader.QueryRow("SELECT count(*) FROM " + kind + "s").Scan(&audiences); err != nil {
					t.Fatal(err)
				}
				wantTargets, wantAudiences := 1, 0
				if status == "draft" {
					wantTargets = 0
				}
				if active {
					wantAudiences = 1
				}
				if targets != wantTargets || other != 1 || audiences != wantAudiences {
					t.Fatalf("targets=%d other=%d audiences=%d, want %d,1,%d", targets, other, audiences, wantTargets, wantAudiences)
				}
			})
		}
	}
}

func TestAudienceDeletionAllowsBrandCascade(t *testing.T) {
	db := testDB(t)
	seedDeletionAudiences(t, db, "sending")
	for _, q := range []string{
		`INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'reader@example.com',0,0)`,
		`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',0,0)`,
		`INSERT INTO campaign_recipients(campaign_id,subscriber_id) VALUES(1,1)`,
		`INSERT INTO campaign_stats(campaign_id) VALUES(1)`,
		`INSERT INTO templates(brand_id,name,html,created_at,updated_at) VALUES(1,'Template','HTML',0,0)`,
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(2,'Other','Sender','other@example.com',0)`,
		`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(2,2,'other-list','Other list','single',0)`,
		`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(2,2,'other-campaign','sending','Subject','HTML',0,0)`,
		`INSERT INTO campaign_targets VALUES(2,'list',2,'include')`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer.Exec(`DELETE FROM brands WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"subscribers", "list_subscribers", "segments", "campaign_recipients", "campaign_stats", "templates"} {
		var count int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	for _, table := range []string{"brands", "lists", "campaigns", "campaign_targets"} {
		var count int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v, expected only the other brand's record", table, count, err)
		}
	}
}

func TestAudienceDeletionMigrationCleansExistingDrafts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedDeletionAudiences(t, db, "draft")
	for _, q := range []string{
		`DELETE FROM schema_migrations WHERE name='003_audience_deletion.sql'`,
		`DROP TRIGGER lists_protect_active_campaigns`,
		`DROP TRIGGER lists_remove_draft_targets`,
		`DROP TRIGGER segments_protect_active_campaigns`,
		`DROP TRIGGER segments_remove_draft_targets`,
		`INSERT INTO campaign_targets VALUES(1,'list',99,'exclude'),(1,'segment',99,'include')`,
		`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(2,1,'historical','sent','Subject','HTML',0,0)`,
		`INSERT INTO campaign_targets VALUES(2,'list',99,'include')`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct{ campaign, id, want int }{{1, 99, 0}, {1, 1, 2}, {2, 99, 1}} {
		var count int
		if err := db.Reader.QueryRow(`SELECT count(*) FROM campaign_targets WHERE campaign_id=? AND target_id=?`, check.campaign, check.id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != check.want {
			t.Fatalf("campaign %d target %d: count=%d want=%d", check.campaign, check.id, count, check.want)
		}
	}
}
