package jobs

import (
	"context"
	"testing"
	"time"
)

func TestPurgeUnconfirmedUsesCurrentConfirmationPeriodAndRemovesTokens(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	_, err := r.db.Writer.Exec(`
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',0);
INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list','List','double',0);
INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES
(1,1,'fresh@example.org','active',0,0),(2,1,'expired@example.org','active',0,0),
(3,1,'subscribed@example.org','active',0,0),(4,1,'legacy@example.org','active',0,0);
INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at,confirmation_started_at) VALUES
(1,1,'unconfirmed',1,1000,1000),(1,2,'unconfirmed',1,9999,1),
(1,3,'subscribed',1,1,NULL),(1,4,'unconfirmed',1,1000,NULL);
INSERT INTO subscription_confirmations(token_hash,list_id,subscriber_id,expires_at) VALUES
(X'01',1,1,99999),(X'02',1,2,99999),(X'03',1,3,99999),(X'04',1,4,99999);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PurgeUnconfirmed(ctx, time.UnixMilli(500), 1); err != nil {
		t.Fatal(err)
	}
	var members, tokens, expired int
	if err := r.db.Reader.QueryRow(`SELECT count(*),count(*) FILTER(WHERE subscriber_id=2) FROM list_subscribers`).Scan(&members, &expired); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Reader.QueryRow(`SELECT count(*) FROM subscription_confirmations`).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if members != 3 || tokens != 3 || expired != 0 {
		t.Fatalf("members=%d tokens=%d expired=%d", members, tokens, expired)
	}
	if err := r.PurgeUnconfirmed(ctx, time.UnixMilli(1001), 1); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Reader.QueryRow(`SELECT count(*) FROM list_subscribers`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Reader.QueryRow(`SELECT count(*) FROM subscription_confirmations`).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if members != 1 || tokens != 1 {
		t.Fatalf("expired batches left members=%d tokens=%d", members, tokens)
	}
}

func TestHousekeepingRecursAfterDatabaseRepair(t *testing.T) {
	r := testRunner(t)
	r.cfg.MaxAttempts = 1
	ctx := context.Background()
	if _, err := r.db.Writer.Exec(`
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',0);
INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list','List','single',0);
INSERT INTO list_counters(list_id) VALUES(1);
CREATE TRIGGER rollup_unavailable BEFORE INSERT ON list_daily_stats BEGIN SELECT RAISE(FAIL,'rollup unavailable'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterMaintenance(MaintenanceConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := r.ScheduleMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	j, h, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.execute(ctx, j, h)
	if _, err := r.db.Writer.Exec(`DROP TRIGGER rollup_unavailable; UPDATE recurring_jobs SET next_run_at=0 WHERE queue='housekeeping'`); err != nil {
		t.Fatal(err)
	}
	next, h, err := r.claim(ctx)
	if err != nil || next.ID == j.ID || next.Attempts != 1 {
		t.Fatalf("housekeeping did not recur: %+v %v", next, err)
	}
	r.execute(ctx, next, h)
	var failed, completed, snapshots int
	if err := r.db.Reader.QueryRow(`SELECT count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='done') FROM jobs WHERE queue='housekeeping'`).Scan(&failed, &completed); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Reader.QueryRow(`SELECT count(*) FROM list_daily_stats`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if failed != 1 || completed != 1 || snapshots != 1 {
		t.Fatalf("failed=%d completed=%d snapshots=%d", failed, completed, snapshots)
	}
}
