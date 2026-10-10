package migrate

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"postbox/internal/store"
)

const fixture = `-- Sendy fixture intentionally places subscribers before their dependencies.
CREATE TABLE subscribers (
 id int PRIMARY KEY, list int, name text, email text, confirmed int,
 unsubscribed int, bounced int, complaint int, custom_fields text,
 KEY list_idx (list)
);
INSERT INTO subscribers VALUES
 (1,10,'O\'Brien',' ACTIVE@Example.com ',1,0,0,0,'NL%s%9'),
 (2,10,'Duplicate','active@example.com',1,0,0,0,'NL%s%9'),
 (3,10,'Unsubscribe','off@example.com',1,1,0,0,'NL%s%8'),
 (4,10,'Unconfirmed','pending@example.com',0,0,0,0,'NL%s%7'),
 (5,10,'Bounce','bounce@example.com',1,0,1,0,'NL%s%6'),
 (6,10,'Complaint','complaint@example.com',1,0,0,1,'NL%s%5'),
 (7,11,'Cross brand','bounce@example.com',1,0,0,0,'');
CREATE TABLE apps (id int, app_name text, from_name text, from_email text, unexpected text);
INSERT INTO apps VALUES (1,'Brand','Sender','sender@example.com','retain me'),(2,'Other','Other','other@example.com',NULL);
INSERT INTO lists (id,app,name,opt_in,custom_fields) VALUES (10,1,'Main',1,'Country:Text%s%Score:Number'), (11,2,'Other',0,'');
INSERT INTO templates (id,app,name,html) VALUES (21,1,'Table','<html><style>p { color:red; }</style><!--[if mso]><table><![endif]-->[Name]</html>');
INSERT INTO campaigns (id,app,subject,html_text,plain_text,sent,recipients,opens,unique_opens,clicks,complaints,timestamp) VALUES (31,1,'Past','<p>[Name]</p>','Hello',1,42,10,8,4,1,1700000000);
INSERT INTO campaigns (id,app,subject,html_text,sent) VALUES (32,1,'Scheduled source','<p>draft</p>',0);
INSERT INTO unknown_reports (id,campaign,geo) VALUES (100,31,'NL');
DROP TABLE subscribers;
`

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "dest.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func scalar(t *testing.T, db *store.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if e := db.Reader.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestImportStatusesArchiveAndIdempotency(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	opt := Options{SourceID: "sendy-production", BatchSize: 2}
	r, e := Import(ctx, db, DumpSource{Reader: strings.NewReader(fixture)}, opt)
	if e != nil {
		t.Fatal(e)
	}
	if r.Deduplicated != 1 || r.Suppressed != 2 {
		t.Fatalf("report: %+v", r)
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers") != 6 {
		t.Fatal("normalized dedup failed")
	}
	if scalar(t, db, "SELECT count(*) FROM suppressions WHERE brand_id IS NULL") != 2 {
		t.Fatal("missing global suppressions")
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers WHERE status='bounced'") != 1 {
		t.Fatal("bounce status lost")
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers WHERE status='complained'") != 1 {
		t.Fatal("complaint status lost")
	}
	if scalar(t, db, "SELECT count(*) FROM list_subscribers WHERE status='unconfirmed'") != 1 {
		t.Fatal("unconfirmed status lost")
	}
	if scalar(t, db, `SELECT count(*) FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE ls.status='subscribed' AND s.status='active' AND NOT EXISTS(SELECT 1 FROM suppressions x WHERE x.value=s.email AND x.kind='email' AND (x.brand_id IS NULL OR x.brand_id=s.brand_id))`) != 1 {
		t.Fatal("suppressed contact eligible")
	}
	if scalar(t, db, "SELECT count(*) FROM migration_identities WHERE entity='subscribers'") != 7 {
		t.Fatal("source identities not preserved")
	}
	if scalar(t, db, "SELECT json_extract(payload,'$.unexpected')='retain me' FROM migration_records WHERE entity='apps' AND source_id='1'") != 1 {
		t.Fatal("unknown column discarded")
	}
	if scalar(t, db, "SELECT count(*) FROM migration_records WHERE entity='unknown_reports'") != 1 {
		t.Fatal("unknown report lost")
	}
	if scalar(t, db, "SELECT sent FROM campaign_stats WHERE campaign_id=(SELECT target_id FROM migration_identities WHERE entity='campaigns' AND source_id='31')") != 42 {
		t.Fatal("report lost")
	}
	if scalar(t, db, "SELECT count(*) FROM campaigns WHERE status IN ('sending','scheduled','draft')") != 0 {
		t.Fatal("import queued campaigns")
	}
	var name string
	if e = db.Reader.QueryRow("SELECT name FROM subscribers WHERE email='active@example.com'").Scan(&name); e != nil || name != "O'Brien" {
		t.Fatalf("escaped name %q: %v", name, e)
	}
	if _, e = Import(ctx, db, DumpSource{Reader: strings.NewReader(fixture)}, opt); e != nil {
		t.Fatal(e)
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers") != 6 || scalar(t, db, "SELECT count(*) FROM campaigns") != 2 || scalar(t, db, "SELECT count(*) FROM suppressions") != 2 {
		t.Fatal("rerun duplicated data")
	}
}
func TestDryRunNeverChangesDestination(t *testing.T) {
	db := testDB(t)
	r, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(fixture)}, Options{SourceID: "preview", DryRun: true})
	if e != nil {
		t.Fatal(e)
	}
	if !r.DryRun || r.Imported["subscribers"] != 7 {
		t.Fatalf("%+v", r)
	}
	if scalar(t, db, "SELECT count(*) FROM brands") != 0 {
		t.Fatal("dry run wrote destination")
	}
	enabled, e := db.Setting(context.Background(), "sending_enabled")
	if e != nil || enabled != "true" {
		t.Fatalf("dry run altered kill switch: %q %v", enabled, e)
	}
}
func TestDumpLiteralParser(t *testing.T) {
	input := `/* executable comments are ignored, never executed: /*!50000 DROP TABLE x */
 CREATE TABLE test (id int, value text, score decimal(10,2), blank text, PRIMARY KEY(id));
 INSERT INTO test VALUES (1,'a;,(b)\n\\quoted\' value',12.5,NULL),(2,'it''s',-3,0x6869);
 INSERT INTO db.test (id,value,score,blank) VALUES (3,'日本語',7,'');`
	var rows []Row
	e := (DumpSource{Reader: strings.NewReader(input)}).Scan(context.Background(), func(r Row) error { rows = append(rows, r); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 3 || rows[0].get("score") != "12.5" || rows[0].Values["blank"] != nil || rows[1].get("value") != "it's" || rows[1].get("blank") != "hi" || rows[2].Table != "test" {
		t.Fatalf("%+v", rows)
	}
	if rows[0].get("value") != "a;,(b)\n\\quoted' value" {
		t.Fatalf("decoded %q", rows[0].get("value"))
	}
}
func TestDumpRejectsExpressionsAndMalformedRows(t *testing.T) {
	for _, input := range []string{`INSERT INTO apps VALUES(1);`, `INSERT INTO apps(id,name) VALUES(1,concat('a','b'));`, `INSERT INTO apps(id,name) VALUES(1,'truncated);`, `INSERT INTO apps(id,name) VALUES(1,'ok',2);`, `INSERT INTO apps(id) SELECT id FROM x;`} {
		t.Run(input, func(t *testing.T) {
			if e := (DumpSource{Reader: strings.NewReader(input)}).Scan(context.Background(), func(Row) error { return nil }); e == nil {
				t.Fatal("accepted unsupported SQL")
			}
		})
	}
}
func TestDumpStreamsLargeExtendedInsert(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		fmt.Fprint(pw, "INSERT INTO subscribers(id,email) VALUES ")
		for n := 0; n < 100000; n++ {
			if n != 0 {
				fmt.Fprint(pw, ",")
			}
			fmt.Fprintf(pw, "(%d,'person%d@example.com')", n, n)
		}
		fmt.Fprint(pw, ";")
	}()
	defer pr.Close()
	count := 0
	e := (DumpSource{Reader: pr}).Scan(context.Background(), func(r Row) error {
		if r.get("id") != fmt.Sprint(count) {
			return fmt.Errorf("wrong row %d", count)
		}
		count++
		return nil
	})
	if e != nil || count != 100000 {
		t.Fatalf("stream count %d: %v", count, e)
	}
}
func TestMissingConsentSuppresses(t *testing.T) {
	db := testDB(t)
	dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');INSERT INTO lists(id,app,name) VALUES(1,1,'X');INSERT INTO subscribers(id,list,email) VALUES(1,1,'no-consent@example.com');`
	r, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "missing"})
	if e != nil {
		t.Fatal(e)
	}
	if r.Suppressed != 0 || scalar(t, db, "SELECT count(*) FROM list_subscribers WHERE status='unconfirmed'") != 1 {
		t.Fatal("missing consent became sendable")
	}
}
func TestMalformedCustomFieldsFailActionably(t *testing.T) {
	db := testDB(t)
	dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');INSERT INTO lists(id,app,name,custom_fields) VALUES(1,1,'X','Country:Text%s%Age:Number');INSERT INTO subscribers(id,list,email,confirmed,custom_fields) VALUES(1,1,'a@example.com',1,'NL');`
	_, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "fields"})
	if e == nil || !strings.Contains(e.Error(), "custom field value count") {
		t.Fatalf("%v", e)
	}
}

func TestMembershipScopeAndGlobalBounce(t *testing.T) {
	db := testDB(t)
	dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');
 INSERT INTO lists(id,app,name) VALUES(1,1,'A'),(2,1,'B');
 INSERT INTO subscribers(id,list,email,confirmed,unsubscribed,bounced,complaint) VALUES
 (1,1,'member@example.com',1,0,0,0),(2,2,'MEMBER@example.com',1,1,0,0),
 (3,1,'pending@example.com',1,0,0,0),(4,2,'pending@example.com',0,0,0,0),
 (5,1,'bounced@example.com',1,0,0,0),(6,2,'bounced@example.com',1,0,2,0),
 (7,1,'complaint@example.com',1,0,0,0),(8,2,'complaint@example.com',1,0,0,1);`
	_, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "scope"})
	if e != nil {
		t.Fatal(e)
	}
	if scalar(t, db, "SELECT count(*) FROM suppressions") != 2 {
		t.Fatal("membership-only statuses became global suppression")
	}
	if scalar(t, db, `SELECT count(*) FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE ls.status='subscribed' AND s.status='active' AND NOT EXISTS(SELECT 1 FROM suppressions x WHERE x.value=s.email AND x.kind='email')`) != 2 {
		t.Fatal("subscribed membership did not survive")
	}
	if scalar(t, db, "SELECT count(*) FROM list_subscribers WHERE status='unconfirmed'") != 1 {
		t.Fatal("unconfirmed membership lost")
	}
}
func TestRerunCannotReactivateAndNewBounceIsApplied(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	base := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');INSERT INTO lists(id,app,name) VALUES(1,1,'A');INSERT INTO subscribers(id,list,email,confirmed,bounced) VALUES(1,1,'member@example.com',1,%d);`
	opt := Options{SourceID: "rerun"}
	if _, e := Import(ctx, db, DumpSource{Reader: strings.NewReader(fmt.Sprintf(base, 0))}, opt); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Writer.Exec("UPDATE list_subscribers SET status='unsubscribed'"); e != nil {
		t.Fatal(e)
	}
	if _, e := Import(ctx, db, DumpSource{Reader: strings.NewReader(fmt.Sprintf(base, 0))}, opt); e != nil {
		t.Fatal(e)
	}
	if scalar(t, db, "SELECT count(*) FROM list_subscribers WHERE status='subscribed'") != 0 {
		t.Fatal("rerun reactivated local unsubscribe")
	}
	if _, e := Import(ctx, db, DumpSource{Reader: strings.NewReader(fmt.Sprintf(base, 1))}, opt); e != nil {
		t.Fatal(e)
	}
	if scalar(t, db, "SELECT count(*) FROM subscribers WHERE status='bounced'") != 1 || scalar(t, db, "SELECT count(*) FROM suppressions") != 1 {
		t.Fatal("rerun ignored new bounce")
	}
}
func TestExplicitSuppressionEmailAndIDNDomain(t *testing.T) {
	db := testDB(t)
	dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');INSERT INTO lists(id,app,name) VALUES(1,1,'A');INSERT INTO subscribers(id,list,email,confirmed) VALUES(1,1,'blocked@example.com',1);INSERT INTO suppressions(id,value,kind,reason) VALUES(1,' BLOCKED@EXAMPLE.COM ','email','complaint'),(2,'bücher.de','domain','manual');`
	_, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "suppression"})
	if e != nil {
		t.Fatal(e)
	}
	if scalar(t, db, "SELECT count(*) FROM suppressions WHERE value IN ('blocked@example.com','xn--bcher-kva.de') AND brand_id IS NULL") != 2 {
		t.Fatal("suppression normalization failed")
	}
}
func TestUnknownSuppressionTableFails(t *testing.T) {
	db := testDB(t)
	dump := `INSERT INTO apps(id,app_name,from_email) VALUES(1,'X','from@example.com');INSERT INTO lists(id,app,name) VALUES(1,1,'A');INSERT INTO subscribers(id,list,email,confirmed) VALUES(1,1,'a@example.com',1);INSERT INTO vendor_suppression(id,address) VALUES(1,'a@example.com');`
	_, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader(dump)}, Options{SourceID: "unknown"})
	if e == nil || !strings.Contains(e.Error(), "unsupported suppression table") {
		t.Fatalf("%v", e)
	}
}
func TestCancellationAndInvalidUTF8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := (DumpSource{Reader: strings.NewReader(fixture)}).Scan(ctx, func(Row) error { return nil }); e == nil {
		t.Fatal("cancel ignored")
	}
	db := testDB(t)
	_, e := Import(context.Background(), db, DumpSource{Reader: strings.NewReader("INSERT INTO apps(id,app_name) VALUES(1,'\xff');")}, Options{SourceID: "encoding"})
	if e == nil || !strings.Contains(e.Error(), "non-UTF-8") {
		t.Fatalf("%v", e)
	}
}

func TestUnsupportedDumpModesCannotLoseDataSilently(t *testing.T) {
	for _, dump := range []string{`SET SQL_MODE='NO_BACKSLASH_ESCAPES';INSERT INTO x(id) VALUES(1);`, `/*!40101 SET SQL_MODE='NO_BACKSLASH_ESCAPES' */;`, `/*!50000 INSERT INTO x(id) VALUES(1) */;`, `REPLACE INTO x(id) VALUES(1);`, `LOAD DATA INFILE '/tmp/source' INTO TABLE x;`} {
		if e := (DumpSource{Reader: strings.NewReader(dump)}).Scan(context.Background(), func(Row) error { return nil }); e == nil {
			t.Fatalf("accepted unsupported source %q", dump)
		}
	}
}
