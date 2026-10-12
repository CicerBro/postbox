package migrate

import (
	"context"
	"strings"
	"testing"
)

const engagementFixture = `
INSERT INTO apps(id,app_name,from_name,from_email) VALUES(1,'Brand','Sender','sender@example.com');
INSERT INTO campaigns(id,app,title,html_text,sent,recipients,opens) VALUES
 (1,1,'Historical','<p>past</p>',1788254101,100,'80:DE,136:CH,80:US,007:CH,'),
 (2,1,'Empty','<p>empty</p>',0,0,NULL);
INSERT INTO links(id,campaign_id,ares_emails_id,link,clicks) VALUES
 (10,1,NULL,'https://example.com/a','80,136,80,'),
 (11,1,0,'https://example.com/b','136,19,'),
 (12,1,NULL,'https://example.com/a','19,'),
 (13,1,NULL,'https://example.com/single','7'),
 (14,1,123,'https://example.com/autoresponder','80,');
`

func TestHistoricalSendDate(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{"", 0}, {"0", 0}, {"1", 0}, {"sent", 0},
		{"1788254101", 1788254101000}, {"1788254101000", 1788254101000},
		{"999999999999999999", 0},
	} {
		raw := tc.raw
		got := campaignSentAt(Row{Values: map[string]*string{"sent": &raw}})
		if tc.want == 0 {
			if got != nil {
				t.Fatalf("%q became a historical timestamp", raw)
			}
		} else if got == nil || *got != tc.want {
			t.Fatalf("%q: got %v want %d", raw, got, tc.want)
		}
	}
}

func TestEngagementFormats(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		countries     bool
		total, unique int64
		bad           bool
	}{
		{"", true, 0, 0, false},
		{"80:DE,80:US,007:CH,", true, 3, 2, false},
		{"80:,", true, 1, 1, false},
		{"7", false, 1, 1, false},
		{"7,007,8,", false, 3, 2, false},
		{"80:DE,,", true, 0, 0, true},
		{"80:DE,invalid", true, 0, 0, true},
		{"0,", false, 1, 1, false},
		{"0:US,0:DE,", true, 2, 1, false},
		{"-1,", false, 0, 0, true},
		{"1.5", false, 0, 0, true},
		{"80:US:extra,", true, 0, 0, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			total, ids, _, err := parseEngagement(tc.raw, tc.countries)
			if (err != nil) != tc.bad || total != tc.total || int64(len(ids)) != tc.unique {
				t.Fatalf("total=%d unique=%d error=%v", total, len(ids), err)
			}
		})
	}
}

func TestImportedSendyEngagementAndArchivedRecovery(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(engagementFixture)}, Options{SourceID: "sendy"})
	if err != nil {
		t.Fatal(err)
	}
	id := scalar(t, db, `SELECT target_id FROM migration_identities WHERE source='sendy' AND entity='campaigns' AND source_id='1'`)
	assert := func() {
		t.Helper()
		for column, want := range map[string]int64{"opens_total": 4, "opens_unique": 3, "clicks_total": 7, "clicks_unique": 4, "sent": 100} {
			if got := scalar(t, db, "SELECT "+column+" FROM campaign_stats WHERE campaign_id=?", id); got != want {
				t.Fatalf("%s=%d want %d", column, got, want)
			}
		}
		if got := scalar(t, db, "SELECT clicks_total FROM link_stats JOIN links ON links.id=link_id WHERE url='https://example.com/a'"); got != 4 {
			t.Fatalf("URL clicks=%d", got)
		}
		if got := scalar(t, db, "SELECT clicks_unique FROM link_stats JOIN links ON links.id=link_id WHERE url='https://example.com/a'"); got != 3 {
			t.Fatalf("URL unique=%d", got)
		}
		if got := scalar(t, db, "SELECT opens FROM campaign_countries WHERE campaign_id=? AND country='CH'", id); got != 2 {
			t.Fatalf("CH opens=%d", got)
		}
		if scalar(t, db, "SELECT count(*) FROM campaign_recipients") != 0 || scalar(t, db, "SELECT count(*) FROM jobs") != 0 {
			t.Fatal("created send work")
		}
	}
	assert()
	// Simulate a production import from the old adapter. Unrelated counters and
	// edited content must survive recovery; source subscriber rows are unnecessary.
	_, err = db.Writer.Exec(`UPDATE campaign_stats SET opens_total=0,opens_unique=0,clicks_total=0,clicks_unique=0,delivered=77,opens_human_unique=12; UPDATE campaigns SET subject='Locally edited'; DELETE FROM migration_records WHERE entity='apps';`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetSetting(ctx, "sending_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	preview, err := RecoverCampaignStats(ctx, db, "sendy", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Campaigns) != 2 || preview.Campaigns[0].After != (EngagementStats{4, 3, 7, 4}) {
		t.Fatalf("preview: %+v", preview)
	}
	if scalar(t, db, "SELECT sum(opens_total) FROM campaign_stats") != 0 {
		t.Fatal("preview wrote stats")
	}
	for range 2 {
		if _, err = RecoverCampaignStats(ctx, db, "sendy", false); err != nil {
			t.Fatal(err)
		}
		assert()
	}
	if scalar(t, db, "SELECT count(*) FROM links") != 3 {
		t.Fatal("retry duplicated links or mapped autoresponder")
	}
	if scalar(t, db, "SELECT delivered FROM campaign_stats WHERE campaign_id=?", id) != 77 || scalar(t, db, "SELECT opens_human_unique FROM campaign_stats WHERE campaign_id=?", id) != 12 {
		t.Fatal("overwrote unrelated counters")
	}
	if scalar(t, db, "SELECT count(*) FROM campaigns WHERE subject='Locally edited'") != 2 {
		t.Fatal("overwrote content")
	}
	enabled, err := db.Setting(ctx, "sending_enabled")
	if err != nil || enabled != "true" {
		t.Fatalf("sending setting: %q %v", enabled, err)
	}
}

func TestRecoveryRollsBackAndScopesSource(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for _, source := range []string{"first", "second"} {
		if _, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(engagementFixture)}, Options{SourceID: source}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Writer.Exec(`UPDATE campaign_stats SET opens_total=0,opens_unique=0,clicks_total=0,clicks_unique=0; UPDATE migration_records SET payload=json_set(payload,'$.opens','malformed') WHERE source='first' AND entity='campaigns' AND source_id='2'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RecoverCampaignStats(ctx, db, "first", false); err == nil {
		t.Fatal("accepted malformed archive")
	}
	if scalar(t, db, "SELECT sum(opens_total) FROM campaign_stats") != 0 {
		t.Fatal("failed repair committed partial stats")
	}
	if _, err = RecoverCampaignStats(ctx, db, "second", false); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT sum(opens_total) FROM campaign_stats") != 4 {
		t.Fatal("repair crossed source boundary")
	}
	if _, err = RecoverCampaignStats(ctx, db, "missing", true); err == nil {
		t.Fatal("accepted unknown source")
	}
	// Native recipient activity prevents an archive from replacing live counters.
	_, err = db.Writer.Exec(`UPDATE campaigns SET status='sending' WHERE id IN (SELECT target_id FROM migration_identities WHERE source='second' AND entity='campaigns')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RecoverCampaignStats(ctx, db, "second", false); err == nil {
		t.Fatal("repaired an active campaign")
	}
}

func TestArchivedSubscriberOutcomesAndEstimatedDelivery(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	dump := engagementFixture + `
 INSERT INTO lists(id,app,name) VALUES(1,1,'Main');
 INSERT INTO subscribers(id,list,email,confirmed,last_campaign,unsubscribed,bounced,complaint,bounce_soft) VALUES
 (1,1,'one@example.com',1,1,1,0,0,0),
 (2,1,'ONE@example.com',1,1,1,1,0,0),
 (3,1,'spam@example.com',1,1,0,0,1,0),
 (4,1,'soft@example.com',1,1,0,0,0,1),
 (5,1,'other@example.com',1,2,1,1,1,0),
 (6,1,'unknown@example.com',1,NULL,1,1,1,0),
 (7,1,'flag@example.com',1,1,2,2,2,0);
 `
	for _, source := range []string{"sendy", "other"} {
		if _, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: source}); err != nil {
			t.Fatal(err)
		}
	}
	id := scalar(t, db, `SELECT target_id FROM migration_identities WHERE source='sendy' AND entity='campaigns' AND source_id='1'`)
	check := func() {
		t.Helper()
		for column, want := range map[string]int64{"unsubscribes": 2, "bounces_hard": 1, "bounces_soft": 0, "complaints": 1, "delivered": 99} {
			if got := scalar(t, db, "SELECT "+column+" FROM campaign_stats WHERE campaign_id=?", id); got != want {
				t.Fatalf("%s=%d want %d", column, got, want)
			}
		}
	}
	check()
	if _, err := db.Writer.Exec(`UPDATE campaign_stats SET unsubscribes=0,bounces_hard=0,complaints=0,delivered=0`); err != nil {
		t.Fatal(err)
	}
	preview, err := RecoverCampaignStats(ctx, db, "sendy", true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Campaigns[0].AfterOutcomes != (OutcomeStats{99, 2, 1, 1}) || !preview.Campaigns[0].DeliveryEstimated {
		t.Fatalf("preview: %+v", preview)
	}
	if scalar(t, db, "SELECT sum(delivered) FROM campaign_stats") != 0 {
		t.Fatal("preview mutated stats")
	}
	for range 2 {
		if _, err := RecoverCampaignStats(ctx, db, "sendy", false); err != nil {
			t.Fatal(err)
		}
		check()
	}
	if scalar(t, db, `SELECT sum(delivered) FROM campaign_stats WHERE campaign_id IN (SELECT target_id FROM migration_identities WHERE source='other' AND entity='campaigns')`) != 0 {
		t.Fatal("crossed source boundary")
	}
	// Explicit numeric counters take precedence over snapshot status rows.
	if _, err := db.Writer.Exec(`UPDATE migration_records SET payload=json_set(payload,'$.unsubscribes','8','$.bounces','4','$.complaints','3','$.delivered','90') WHERE source='sendy' AND entity='campaigns' AND source_id='1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverCampaignStats(ctx, db, "sendy", false); err != nil {
		t.Fatal(err)
	}
	for column, want := range map[string]int64{"unsubscribes": 8, "bounces_hard": 4, "complaints": 3, "delivered": 90} {
		if got := scalar(t, db, "SELECT "+column+" FROM campaign_stats WHERE campaign_id=?", id); got != want {
			t.Fatalf("%s=%d", column, got)
		}
	}
	if scalar(t, db, "SELECT count(*) FROM campaign_recipients") != 0 || scalar(t, db, "SELECT count(*) FROM jobs") != 0 {
		t.Fatal("created send work")
	}
}
