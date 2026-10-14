package jobs

import (
	"context"
	"testing"
	"time"
)

func TestPurgeSendHistoryPreservesStatsAndDispatchProtection(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if _, err := r.db.Writer.Exec(`
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',0);
INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','sent','Subject','HTML',0,0);
INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES
(1,1,'one@example.org','active',0,0),(2,1,'two@example.org','active',0,0),
(3,1,'three@example.org','active',0,0),(4,1,'four@example.org','active',0,0),
(5,1,'five@example.org','active',0,0),(6,1,'six@example.org','active',0,0),
(7,1,'seven@example.org','active',0,0),(8,1,'eight@example.org','active',0,0);
INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,attempts,message_id,sent_at,delivered_at,first_opened_at,first_clicked_at,snapshot_email) VALUES
(1,1,'sent',1,'old-one',1,2,3,4,'one@example.org'),
(1,2,'sent',1,'old-two',2,NULL,NULL,NULL,'two@example.org'),
(1,3,'sent',1,'old-three',3,NULL,NULL,NULL,'three@example.org'),
(1,4,'unknown',1,NULL,NULL,NULL,NULL,NULL,'four@example.org'),
(1,5,'sent',1,'recent',501,NULL,NULL,NULL,'five@example.org'),
(1,6,'sent',1,'boundary',500,NULL,NULL,NULL,'six@example.org'),
(1,7,'claimed',1,'claimed',1,NULL,NULL,NULL,'seven@example.org'),
(1,8,'sent',1,'undated',NULL,NULL,NULL,NULL,'eight@example.org');
INSERT INTO send_attempts(id,campaign_id,subscriber_id,attempt,started_at,finished_at,outcome,message_id) VALUES
(1,1,1,1,0,1,'sent','old-one'),(2,1,2,1,0,2,'failed',NULL),
(3,1,3,1,0,3,'retry',NULL),(4,1,4,1,0,4,'unknown',NULL),
(5,1,5,1,0,501,'sent','recent'),(6,1,6,1,0,500,'sent','boundary'),
(7,1,7,1,0,1,'started',NULL),(8,1,8,1,0,NULL,'unknown',NULL);
INSERT INTO campaign_stats(campaign_id,recipients,sent,delivered,opens_total,clicks_total,unknown) VALUES(1,8,6,1,42,9,1);
INSERT INTO engagement_hourly(campaign_id,hour,deliveries,opens,clicks) VALUES(1,0,1,42,9);
INSERT INTO send_hourly(brand_id,hour,sent) VALUES(1,0,6);
INSERT INTO campaign_dispatch_ledger(campaign_id,email_hash,outcome,generation,owner_subscriber_id,owner_attempt,updated_at) VALUES(1,zeroblob(32),'unknown',1,4,1,0);`); err != nil {
		t.Fatal(err)
	}
	for range 2 { // A second pass must leave both the retained data and counters unchanged.
		if err := r.PurgeSendHistory(ctx, time.UnixMilli(500), 1); err != nil {
			t.Fatal(err)
		}
		checks := []struct {
			query string
			want  int
		}{
			{`SELECT count(*) FROM campaign_recipients`, 8},
			{`SELECT sum(attempts) FROM campaign_recipients`, 8},
			{`SELECT count(*) FROM campaign_recipients WHERE subscriber_id<=3 AND message_id IS NULL AND status='sent'`, 3},
			{`SELECT count(*) FROM campaign_recipients WHERE subscriber_id>=5 AND message_id IS NOT NULL`, 4},
			{`SELECT count(*) FROM campaign_recipients WHERE subscriber_id=1 AND sent_at=1 AND delivered_at=2 AND first_opened_at=3 AND first_clicked_at=4 AND snapshot_email='one@example.org'`, 1},
			{`SELECT count(*) FROM send_attempts`, 4},
			{`SELECT count(*) FROM send_attempts WHERE id<=4`, 0},
			{`SELECT count(*) FROM campaign_stats WHERE recipients=8 AND sent=6 AND delivered=1 AND opens_total=42 AND clicks_total=9 AND unknown=1`, 1},
			{`SELECT count(*) FROM engagement_hourly WHERE deliveries=1 AND opens=42 AND clicks=9`, 1},
			{`SELECT sent FROM send_hourly WHERE brand_id=1 AND hour=0`, 6},
			{`SELECT count(*) FROM campaign_dispatch_ledger WHERE outcome='unknown' AND generation=1 AND owner_subscriber_id=4 AND owner_attempt=1 AND updated_at=0`, 1},
		}
		for _, check := range checks {
			var got int
			if err := r.db.Reader.QueryRow(check.query).Scan(&got); err != nil || got != check.want {
				t.Fatalf("%s: got=%d want=%d error=%v", check.query, got, check.want, err)
			}
		}
	}
	for _, batch := range []int{0, -1, 10001} {
		if err := r.PurgeSendHistory(ctx, time.Now(), batch); err == nil {
			t.Fatalf("accepted invalid batch size %d", batch)
		}
	}
}

func TestHousekeepingExpiresSendHistoryAfter180Days(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if err := r.RegisterMaintenance(MaintenanceConfig{BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-181 * 24 * time.Hour).UnixMilli()
	recent := time.Now().Add(-179 * 24 * time.Hour).UnixMilli()
	if _, err := r.db.Writer.Exec(`
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',0);
INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','sent','Subject','HTML',0,0);
INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES(1,1,'one@example.org','active',0,0),(2,1,'two@example.org','active',0,0);
INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id,sent_at) VALUES(1,1,'sent','old',?),(1,2,'sent','recent',?);
INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,finished_at,outcome,message_id) VALUES(1,1,1,0,?,'sent','old'),(1,2,1,0,?,'sent','recent');
INSERT INTO campaign_stats(campaign_id,recipients,sent) VALUES(1,2,2);`, old, recent, old, recent); err != nil {
		t.Fatal(err)
	}
	if err := r.handlers["housekeeping"](ctx, Job{}); err != nil {
		t.Fatal(err)
	}
	var ids, attempts, sent int
	if err := r.db.Reader.QueryRow(`SELECT
(SELECT count(*) FROM campaign_recipients WHERE message_id='recent'),
(SELECT count(*) FROM send_attempts),
(SELECT sent FROM campaign_stats WHERE campaign_id=1)`).Scan(&ids, &attempts, &sent); err != nil || ids != 1 || attempts != 1 || sent != 2 {
		t.Fatalf("message IDs=%d attempts=%d sent=%d error=%v", ids, attempts, sent, err)
	}
	var expired int
	if err := r.db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE message_id='old'`).Scan(&expired); err != nil || expired != 0 {
		t.Fatalf("expired message IDs=%d error=%v", expired, err)
	}
}
