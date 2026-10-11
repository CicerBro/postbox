package store

import (
	"context"
	"testing"
)

func TestImportedCampaignsFinishedMigration(t *testing.T) {
	db := testDB(t)
	_, err := db.Writer.Exec(`
 DELETE FROM schema_migrations WHERE name='002_imported_campaigns_finished.sql';
 INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',1);
 INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at,finished_at) VALUES
 (1,1,'imported','cancelled','Imported','<p>History</p>',1,2,NULL),
 (2,1,'local','cancelled','Local','<p>Local</p>',1,2,3),
 (3,1,'active','scheduled','Scheduled','<p>Future</p>',1,2,NULL);
 INSERT INTO migration_identities(source,entity,source_id,target_id) VALUES('sendy','campaigns','100',1),('sendy','campaigns','101',3);
 INSERT INTO campaign_stats(campaign_id,recipients,sent,opens_total) VALUES(1,42,42,10);
 `)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]string{1: "sent", 2: "cancelled", 3: "scheduled"} {
		var status string
		if err = db.Reader.QueryRow("SELECT status FROM campaigns WHERE id=?", id).Scan(&status); err != nil || status != want {
			t.Fatalf("campaign %d status %q, want %q: %v", id, status, want, err)
		}
	}
	var finished, sent, opens, recipients, jobs int
	if err = db.Reader.QueryRow("SELECT finished_at FROM campaigns WHERE id=1").Scan(&finished); err != nil || finished != 2 {
		t.Fatalf("finished %d: %v", finished, err)
	}
	if err = db.Reader.QueryRow("SELECT sent,opens_total FROM campaign_stats WHERE campaign_id=1").Scan(&sent, &opens); err != nil || sent != 42 || opens != 10 {
		t.Fatalf("stats %d/%d: %v", sent, opens, err)
	}
	if err = db.Reader.QueryRow("SELECT count(*) FROM campaign_recipients").Scan(&recipients); err != nil || recipients != 0 {
		t.Fatalf("recipients %d: %v", recipients, err)
	}
	if err = db.Reader.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs %d: %v", jobs, err)
	}
	if err = db.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
