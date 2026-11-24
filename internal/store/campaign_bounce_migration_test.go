package store

import (
	"context"
	"testing"
)

func TestCampaignBounceOutcomeMigration(t *testing.T) {
	db := testDB(t)
	if _, err := db.Writer.Exec(`
ALTER TABLE campaign_recipients DROP COLUMN bounce_kind;
DELETE FROM schema_migrations WHERE name='015_campaign_bounce_outcomes.sql';
INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.com',0);
INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at)
  VALUES(1,1,'campaign','sent','Subject','Body',0,0),(2,1,'imported','sent','Subject','Body',0,0);
INSERT INTO subscribers(id,brand_id,email,created_at,updated_at)
  VALUES(1,1,'one@example.com',0,0),(2,1,'two@example.com',0,0),(3,1,'three@example.com',0,0);
INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,delivered_at,bounced_at)
  VALUES(1,1,'sent',500,1000),(1,2,'sent',1000,2000),(1,3,'sent',2000,1000);
INSERT INTO campaign_stats(campaign_id,delivered,bounces_hard,bounces_soft)
  VALUES(1,3,2,2),(2,30,7,8);
INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES
  (1,1,'bounce',1000,'{"campaign_id":1,"subtype":"Transient"}'),
  (1,1,'bounce',1000,'{"campaign_id":1,"subtype":"Transient"}'),
  (1,1,'delivery',2000,'{"campaign_id":1}'),
  (1,2,'bounce',2000,'{"campaign_id":1,"subtype":"Permanent"}');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{
		`SELECT count(*) FROM campaign_recipients WHERE subscriber_id=1 AND delivered_at=2000 AND bounced_at IS NULL AND bounce_kind=''`,
		`SELECT count(*) FROM campaign_recipients WHERE subscriber_id=2 AND delivered_at=1000 AND bounced_at=2000 AND bounce_kind='hard'`,
		`SELECT count(*) FROM campaign_recipients WHERE subscriber_id=3 AND delivered_at=2000 AND bounced_at=1000 AND bounce_kind=''`,
		`SELECT count(*) FROM campaign_stats WHERE campaign_id=1 AND delivered=3 AND bounces_hard=2 AND bounces_soft=0`,
		`SELECT count(*) FROM campaign_stats WHERE campaign_id=2 AND delivered=30 AND bounces_hard=7 AND bounces_soft=8`,
	} {
		var count int
		if err := db.Reader.QueryRow(check).Scan(&count); err != nil || count != 1 {
			t.Errorf("%s: count=%d, err=%v", check, count, err)
		}
	}
}
