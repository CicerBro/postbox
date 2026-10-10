package contacts

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"postbox/internal/store"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, Brand, List) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	b, e := s.CreateBrand(context.Background(), Brand{Name: "Test", FromEmail: "sender@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.CreateList(context.Background(), List{BrandID: b.ID, Name: "Readers", OptIn: "double"})
	if e != nil {
		t.Fatal(e)
	}
	return s, b, l
}
func TestConsentConfirmationAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	v, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "  A@EXAMPLE.ORG ", Fields: map[string]any{"country": "NL"}, ListIDs: []int64{l.ID}, Consent: true, ConsentIP: "127.0.0.1"})
	if e != nil {
		t.Fatal(e)
	}
	token := v.ConfirmationTokens[l.ID]
	if token == "" || v.ConsentAt == nil {
		t.Fatal("missing confirmation or consent")
	}
	if e = s.Confirm(ctx, token); e != nil {
		t.Fatal(e)
	}
	if e = s.Confirm(ctx, token); e == nil {
		t.Fatal("reused token")
	}
	v2, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, Fields: map[string]any{"age": 32}, ListIDs: []int64{l.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if v2.ID != v.ID || v2.Fields["country"] != "NL" {
		t.Fatal("upsert was not idempotent")
	}
	var subscribed string
	if e = s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", l.ID, v.ID).Scan(&subscribed); e != nil || subscribed != "subscribed" {
		t.Fatalf("profile upsert downgraded confirmed member %q %v", subscribed, e)
	}
	if e = s.Unsubscribe(ctx, b.ID, v.ID, l.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{l.ID}}); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", l.ID, v.ID).Scan(&status); e != nil || status != "unsubscribed" {
		t.Fatalf("revived optout %q %v", status, e)
	}
	var active, unsub int
	if e = s.db.Reader.QueryRow("SELECT active,unsubscribed FROM list_counters WHERE list_id=?", l.ID).Scan(&active, &unsub); e != nil || active != 0 || unsub != 1 {
		t.Fatalf("bad counters %d %d %v", active, unsub, e)
	}
}
func TestSegmentsAndIndexes(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	field, e := s.SaveCustomField(ctx, CustomField{BrandID: b.ID, Key: "country", Type: "text", Filterable: true})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "segment@example.org", Fields: map[string]any{"country": "NL"}, ListIDs: []int64{l.ID}})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,'camp','sent','subject','html',1,1)", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	campaign, _ := r.LastInsertId()
	if _, e = s.db.Writer.Exec("INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,first_opened_at,first_clicked_at) VALUES(?,?,'sent',1,1)", campaign, v.ID); e != nil {
		t.Fatal(e)
	}
	for _, rule := range []Rule{{Field: "opened", Op: "in", Value: campaign}, {Field: "clicked", Op: "in", Value: campaign}, {Field: "fields.country", Op: "eq", Value: "NL"}} {
		n, e := s.SegmentCount(ctx, b.ID, rule)
		if e != nil || n != 1 {
			t.Fatalf("rule %+v: %d %v", rule, n, e)
		}
	}
	n, e := s.SegmentCount(ctx, b.ID, Rule{Field: "inactive", Value: 1})
	if e != nil || n != 0 {
		t.Fatalf("inactive %d %v", n, e)
	}
	if _, e = s.SegmentCount(ctx, b.ID+100, Rule{Field: "opened", Value: campaign}); e == nil {
		t.Fatal("cross brand campaign accepted")
	}
	if _, _, e = CompileSegment(Rule{Field: "fields.country' OR 1=1--", Op: "eq", Value: "NL"}); e == nil {
		t.Fatal("unsafe field accepted")
	}
	rows, e := s.db.Reader.Query("EXPLAIN QUERY PLAN SELECT id FROM subscribers INDEXED BY subscribers_custom_"+fmt.Sprint(field.ID)+" WHERE brand_id=? AND fields ->> '$.country'=?", b.ID, "NL")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var a, c, d int
		var detail string
		if e = rows.Scan(&a, &c, &d, &detail); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(detail, "SEARCH") || !strings.Contains(detail, "<expr>") {
			t.Fatalf("expression index unused: %s", detail)
		}
	}
}
func TestCSVAndErasure(t *testing.T) {
	ctx := context.Background()
	s, b, _ := fixture(t)
	csv := "Email,Country\nTest@EXAMPLE.ORG,NL\ninvalid,US\n"
	opt := ImportOptions{BrandID: b.ID, Mapping: map[string]string{"Email": "email", "Country": "fields.country"}, DryRun: true}
	res, e := s.ImportCSV(ctx, strings.NewReader(csv), opt)
	if e != nil || res.Rows != 2 || res.Invalid != 1 || res.Imported != 0 {
		t.Fatalf("preview %+v %v", res, e)
	}
	subs, _ := s.Search(ctx, b.ID, 0, 100)
	if len(subs) != 0 {
		t.Fatal("dryrun wrote")
	}
	opt.DryRun = false
	res, e = s.ImportCSV(ctx, strings.NewReader(csv), opt)
	if e != nil || res.Imported != 1 {
		t.Fatalf("import %+v %v", res, e)
	}
	v, e := s.GetByEmail(ctx, b.ID, "test@example.org")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Writer.Exec("INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(1,?,1)", v.ID); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = s.ExportCSV(ctx, &out, b.ID, 0); e != nil || !strings.Contains(out.String(), "test@example.org") {
		t.Fatalf("export %s %v", out.String(), e)
	}
	if e = s.Erase(ctx, b.ID, v.ID); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.db.Reader.QueryRow("SELECT count(*) FROM opens WHERE subscriber_id=?", v.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("event not erased")
	}
}

func BenchmarkImportCSV(b *testing.B)  { benchmarkImport(b, 10000) }
func BenchmarkImport350K(b *testing.B) { benchmarkImport(b, 350000) }
func benchmarkImport(b *testing.B, count int) {
	ctx := context.Background()
	var input strings.Builder
	input.WriteString("email,name\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&input, "reader%d@example.org,Reader %d\n", i, i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db, e := store.Open(filepath.Join(b.TempDir(), "bench.db"))
		if e != nil {
			b.Fatal(e)
		}
		s := New(db)
		brand, e := s.CreateBrand(ctx, Brand{Name: "Benchmark", FromEmail: "sender@example.org"})
		if e != nil {
			b.Fatal(e)
		}
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		peak := base.HeapAlloc
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					var m runtime.MemStats
					runtime.ReadMemStats(&m)
					if m.HeapAlloc > peak {
						peak = m.HeapAlloc
					}
				}
			}
		}()
		b.StartTimer()
		r, e := s.ImportCSV(ctx, strings.NewReader(input.String()), ImportOptions{BrandID: brand.ID})
		if e != nil || r.Imported != int64(count) {
			b.Fatalf("%+v %v", r, e)
		}
		b.StopTimer()
		close(stop)
		<-done
		b.ReportMetric(float64(peak)/1048576, "peak_heap_MB")
		b.ReportMetric(float64(peak-base.HeapAlloc)/1048576, "extra_heap_MB")
		db.Close()
		b.StartTimer()
	}
}

func TestCSVTypesAndScopedErase(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	if _, e := s.SaveCustomField(ctx, CustomField{BrandID: b.ID, Key: "age", Type: "number", Filterable: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ImportCSV(ctx, strings.NewReader("email,Age\nyoung@example.org,2\nolder@example.org,20\n"), ImportOptions{BrandID: b.ID, Mapping: map[string]string{"email": "email", "Age": "fields.age"}}); e != nil {
		t.Fatal(e)
	}
	n, e := s.SegmentCount(ctx, b.ID, Rule{Field: "fields.age", Op: "gt", Value: float64(10)})
	if e != nil || n != 1 {
		t.Fatalf("numeric field comparison %d %v", n, e)
	}
	b2, e := s.CreateBrand(ctx, Brand{Name: "Other", FromEmail: "sender@example.org"})
	if e != nil {
		t.Fatal(e)
	}
	s.SetBaseURL("https://example.org")
	v, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "shared@example.org", ListIDs: []int64{l.ID}})
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Upsert(ctx, UpsertInput{BrandID: b2.ID, Email: v.Email})
	if e != nil {
		t.Fatal(e)
	}
	for i, sub := range []Subscriber{v, other} {
		r, e := s.db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,?,'sent','subject','html',1,1)", sub.BrandID, fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		message := fmt.Sprintf("ses%d", i)
		if _, e = s.db.Writer.Exec("INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,message_id) VALUES(?,?,'sent',?)", id, sub.ID, message); e != nil {
			t.Fatal(e)
		}
		if _, e = s.db.Writer.Exec("INSERT INTO ses_events(sns_message_id,ses_message_id,type,recipient,at,payload) VALUES(?,?,'Delivery',?,1,'{}')", message, message, sub.Email); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Erase(ctx, b.ID, v.ID); e != nil {
		t.Fatal(e)
	}
	var raw, jobs int
	if e = s.db.Reader.QueryRow("SELECT count(*) FROM ses_events").Scan(&raw); e != nil || raw != 1 {
		t.Fatalf("other brand events erased %d %v", raw, e)
	}
	if e = s.db.Reader.QueryRow("SELECT count(*) FROM jobs WHERE queue='transactional_email'").Scan(&jobs); e != nil || jobs != 0 {
		t.Fatalf("erased contact email retained %d %v", jobs, e)
	}
}

func TestExportRoundTripPreservesOptOut(t *testing.T) {
	ctx := context.Background()
	s, b, _ := fixture(t)
	list, e := s.CreateList(ctx, List{BrandID: b.ID, Name: "Source", OptIn: "single"})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "optout@example.org", Fields: map[string]any{"country": "NL"}, ListIDs: []int64{list.ID}, Consent: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Unsubscribe(ctx, b.ID, v.ID, list.ID); e != nil {
		t.Fatal(e)
	}
	var csv bytes.Buffer
	if e = s.ExportCSV(ctx, &csv, b.ID, list.ID); e != nil {
		t.Fatal(e)
	}
	dest, e := s.CreateList(ctx, List{BrandID: b.ID, Name: "Replacement", OptIn: "single"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ImportCSV(ctx, &csv, ImportOptions{BrandID: b.ID, ListIDs: []int64{dest.ID}}); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", dest.ID, v.ID).Scan(&status); e != nil || status != "unsubscribed" {
		t.Fatalf("export reactivated optout: %q %v", status, e)
	}
	again, e := s.GetByEmail(ctx, b.ID, v.Email)
	if e != nil || again.Fields["country"] != "NL" || again.ConsentAt == nil {
		t.Fatalf("fields or consent lost: %+v %v", again, e)
	}
}

func TestCustomizedConfirmationAndFieldUpdate(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	s.SetBaseURL("https://example.org")
	if e := s.db.SetSetting(ctx, fmt.Sprintf("brand.%d.confirmation_email", b.ID), `{"subject":"Join us","html":"<a href=\"{{ confirmation_url }}\">Join</a>","text":"Join {{ confirmation_url }}"}`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "custom@example.org", ListIDs: []int64{l.ID}}); e != nil {
		t.Fatal(e)
	}
	var payload string
	if e := s.db.Reader.QueryRow("SELECT payload FROM jobs WHERE queue='transactional_email'").Scan(&payload); e != nil || !strings.Contains(payload, "Join us") || !strings.Contains(payload, "https://example.org/confirm/") {
		t.Fatalf("custom email ignored %s %v", payload, e)
	}
	field, e := s.SaveCustomField(ctx, CustomField{BrandID: b.ID, Key: "age", Label: "Age", Type: "number", Filterable: true})
	if e != nil {
		t.Fatal(e)
	}
	field.Label = "Years"
	field.Filterable = false
	if _, e = s.SaveCustomField(ctx, field); e != nil {
		t.Fatal(e)
	}
	field.Key = "years"
	if _, e = s.SaveCustomField(ctx, field); e == nil {
		t.Fatal("field key rename silently lost stored fields")
	}
}
