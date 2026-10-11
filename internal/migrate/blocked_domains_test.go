package migrate

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBlockedDomainsPreserveScopeAndDoNotQueueNotifications(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	base := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'One','sender@one.example'),(2,'Two','sender@two.example');
INSERT INTO lists(id,app,name) VALUES(1,1,'One'),(2,2,'Two');
INSERT INTO subscribers(id,list,email,confirmed) VALUES(1,1,'one@blocked.example',1),(2,2,'two@blocked.example',1),(3,2,'reader@bücher.de',1);`
	opt := Options{SourceID: "blocked-domains"}
	if _, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(base)}, opt); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) SELECT id,'pending-'||id,'sending','Subject','<p>Hello</p>',1,1 FROM brands`,
		`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) SELECT c.id,s.id,'pending' FROM campaigns c JOIN subscribers s ON s.brand_id=c.brand_id`,
		`INSERT INTO webhooks(brand_id,url,events,secret) SELECT id,'https://example.org/webhook','["subscribe","unsubscribe"]','secret' FROM brands`,
	} {
		if _, err := db.Writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	domains := `CREATE TABLE blocked_domains (id int,app int,domain varchar(100),timestamp varchar(100),block_attempts int);
INSERT INTO blocked_domains VALUES(1,1,' BLOCKED.EXAMPLE ','1700000000',4),(2,NULL,'bücher.de','1700000000',0),(3,1,'blocked.example','1700000000',0);`
	preview, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(base + domains)}, Options{SourceID: opt.SourceID, DryRun: true})
	if err != nil || preview.Imported["blocked_domains"] != 3 || preview.Suppressed != 3 {
		t.Fatalf("domain preview: %+v, %v", preview, err)
	}
	if scalar(t, db, "SELECT count(*) FROM suppressions") != 0 || scalar(t, db, "SELECT count(*) FROM campaign_recipients WHERE status='pending'") != 3 {
		t.Fatal("preview changed destination suppressions or recipients")
	}
	for attempt := 0; attempt < 2; attempt++ {
		report, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(domains + base)}, opt)
		if err != nil {
			t.Fatal(err)
		}
		if report.Imported["blocked_domains"] != 3 || report.Suppressed != 3 {
			t.Fatalf("unexpected domain counts: %+v", report)
		}
		if scalar(t, db, "SELECT count(*) FROM suppressions") != 2 {
			t.Fatal("domain import duplicated suppressions")
		}
		if scalar(t, db, `SELECT count(*) FROM suppressions p JOIN brands b ON b.id=p.brand_id WHERE b.name='One' AND p.kind='domain' AND p.value='blocked.example' AND p.reason='manual'`) != 1 {
			t.Fatal("brand domain scope lost")
		}
		if scalar(t, db, `SELECT count(*) FROM suppressions WHERE brand_id IS NULL AND kind='domain' AND value='xn--bcher-kva.de'`) != 1 {
			t.Fatal("global IDN domain not normalized")
		}
		for email, status := range map[string]string{"one@blocked.example": "skipped", "two@blocked.example": "pending", "reader@xn--bcher-kva.de": "skipped"} {
			if scalar(t, db, `SELECT count(*) FROM campaign_recipients r JOIN subscribers s ON s.id=r.subscriber_id WHERE s.email=? AND r.status=?`, email, status) != 1 {
				t.Fatalf("incorrect pending recipient state for %s", email)
			}
		}
		if scalar(t, db, "SELECT count(*) FROM jobs") != 0 || scalar(t, db, "SELECT count(*) FROM subscription_confirmations") != 0 {
			t.Fatal("migration created outbound jobs or confirmations")
		}
	}
	// A retry observes new restrictions even for an already mapped source ID.
	changed := strings.Replace(domains, " BLOCKED.EXAMPLE ", "new.example", 1)
	if _, err := Import(ctx, db, DumpSource{Reader: strings.NewReader(base + changed)}, opt); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT count(*) FROM suppressions") != 3 {
		t.Fatal("retry did not retain old and newly observed restrictions")
	}
}

func TestBlockedDomainsRejectInvalidValuesAndMissingBrands(t *testing.T) {
	for _, row := range []string{"(1,1,NULL)", "(1,1,'bad domain')", "(1,99,'example.com')"} {
		t.Run(row, func(t *testing.T) {
			dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'Brand','sender@example.com');` + fmt.Sprintf("INSERT INTO blocked_domains(id,app,domain) VALUES%s;", row)
			_, err := Import(context.Background(), testDB(t), DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "invalid-domain"})
			if err == nil {
				t.Fatal("invalid domain source was accepted")
			}
		})
	}
}
