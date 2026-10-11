package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"postbox/internal/htmlpolicy"
	"postbox/internal/render"
	"postbox/internal/store"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
	"time"
)

func trackDB(t *testing.T) *store.DB {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "tracking.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{`INSERT INTO brands(id,name,from_name,from_email,tracking_domain,created_at) VALUES(1,'Brand','','sender@example.com','track.example.com',1)`, `INSERT INTO subscribers(id,brand_id,email,name,fields,created_at,updated_at) VALUES(1,1,'changed@example.com','Changed','{}',1,1)`, `INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'one','One','single',1)`, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',1,1)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','sending','Hi','Hi',1,1),(2,1,'other','sending','Hi','Hi',1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status,snapshot_email,snapshot_name,snapshot_fields) VALUES(1,1,'sent','original@example.com','Original','{}'),(2,1,'pending','original@example.com','Original','{}')`, `INSERT INTO campaign_stats(campaign_id) VALUES(1),(2)`} {
		if _, e = db.Writer.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return db
}
func TestOneClickImmediateAndHost(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	s := New(db, &Config{TokenSecret: secret, BaseURL: "https://postbox.example.com"})
	mux := http.NewServeMux()
	s.Register(mux)
	url := "https://track.example.com/u/" + token.Encode(secret, 1, 1)
	r := httptest.NewRequest("POST", url, strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var status string
	db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=1`).Scan(&status)
	if status != "unsubscribed" {
		t.Fatal(status)
	}
	db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=2`).Scan(&status)
	if status != "skipped" {
		t.Fatal(status)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "https://attacker.example/u/"+token.Encode(secret, 1, 1), nil)
	mux.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("accepted wrong brand host")
	}
}
func TestWebIdenticalToSentSnapshot(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	base := "https://track.example.com"
	p, e := render.Prepare(render.Options{CampaignID: 1, Subject: "Hello [Name]", HTML: `<html><body>Dear [Name], [Email] <a href="[unsubscribe]">Leave</a><a href="https://example.com" target="_blank">link</a></body></html>`, TrackingURL: base, Now: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Writer.Exec(`INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,tracking_url,prepared_at) VALUES(1,?,?,?,?,?,?,?)`, p.Subject, p.HTML, p.Text, "", "sender@example.com", base, time.Now().UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	tok := token.Encode(secret, 1, 1)
	_, expected, _, e := p.Render(render.Recipient{ID: 1, Email: "original@example.com", Name: "Original", Token: tok, UnsubscribeURL: base + "/u/" + tok, WebVersionURL: base + "/w/1/" + tok})
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, &Config{TokenSecret: secret, BaseURL: base})
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", base+"/w/1/"+tok, nil))
	if w.Code != 200 || w.Body.String() != expected {
		t.Fatalf("web does not match sent: status=%d\n%s\n%s", w.Code, w.Body.String(), expected)
	}
	assertTrackingHTMLPolicy(t, w.Header(), htmlpolicy.Email)
	for _, path := range []string{"/w/1/" + tok, "/w/1"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", base+path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `target="_blank"`) {
			t.Fatal("browser-version link changed", w.Code, w.Body)
		}
		assertTrackingHTMLPolicy(t, w.Header(), htmlpolicy.Email)
	}
}

func assertTrackingHTMLPolicy(t *testing.T, h http.Header, policy htmlpolicy.Policy) {
	t.Helper()
	if h.Get("Cache-Control") != "no-store, no-transform" || h.Get("Content-Security-Policy") != string(policy) {
		t.Fatal("incorrect tracking HTML policy or proxy protection", h)
	}
}
func TestTrackingCountersSurviveRawDrop(t *testing.T) {
	db := trackDB(t)
	s := New(db, &Config{})
	records := []record{{campaign: 1, subscriber: 1, at: 100, raw: false, machine: true}, {campaign: 1, subscriber: 1, at: 200, raw: true, machine: false}, {campaign: 1, subscriber: 1, at: 300, raw: true, machine: false}}
	if e := s.writeBatch(context.Background(), records); e != nil {
		t.Fatal(e)
	}
	var total, unique, human int
	db.Reader.QueryRow(`SELECT opens_total,opens_unique,opens_human_unique FROM campaign_stats WHERE campaign_id=1`).Scan(&total, &unique, &human)
	if total != 3 || unique != 1 || human != 1 {
		t.Fatalf("counters %d %d %d", total, unique, human)
	}
	db.Reader.QueryRow(`SELECT count(*) FROM opens`).Scan(&total)
	if total != 2 {
		t.Fatal("raw drop lost counters")
	}
}

func TestPersonalizedClickAndDoubleResubscribe(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	base := "https://track.example.com"
	p, e := render.Prepare(render.Options{CampaignID: 1, Subject: "Hi", HTML: `<a href="https://destination.example/path/[Name]?email=[Email]&number=[age]">Click</a>`, TrackingURL: base, Fields: []string{"age"}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Writer.Exec(`INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,tracking_url,fields,prepared_at) VALUES(1,?,?,?,?,?,?,?,?)`, p.Subject, p.HTML, p.Text, "", "sender@example.com", base, `["age"]`, time.Now().UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range p.Links {
		if _, e = db.Writer.Exec(`INSERT INTO links(id,campaign_id,url) VALUES(?,1,?)`, l.ID, l.URL); e != nil {
			t.Fatal(e)
		}
	}
	_, e = db.Writer.Exec(`UPDATE campaign_recipients SET snapshot_fields='{"age":42}' WHERE campaign_id=1`)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, &Config{TokenSecret: secret, BaseURL: base})
	mux := http.NewServeMux()
	s.Register(mux)
	tok := token.Encode(secret, 1, 1)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", base+"/c/1/"+tok, nil))
	if w.Code != 302 || w.Header().Get("Location") != "https://destination.example/path/Original?email=original%40example.com&number=42" {
		t.Fatalf("redirect %d %s", w.Code, w.Header().Get("Location"))
	}
	if _, e = db.Writer.Exec(`UPDATE lists SET opt_in='double' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Writer.Exec(`UPDATE list_subscribers SET status='unsubscribed' WHERE list_id=1`); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", base+"/u/"+tok+"/resubscribe", strings.NewReader("list=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("resubscribe %d %s", w.Code, w.Body.String())
	}
	var status string
	db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE list_id=1`).Scan(&status)
	if status != "unconfirmed" {
		t.Fatal(status)
	}
	var n int
	db.Reader.QueryRow(`SELECT count(*) FROM subscription_confirmations WHERE list_id=1`).Scan(&n)
	if n != 1 {
		t.Fatal("confirmation not durable")
	}
	db.Reader.QueryRow(`SELECT count(*) FROM jobs WHERE queue='transactional_email' AND json_extract(payload,'$.html') LIKE '%https://track.example.com/confirm/%'`).Scan(&n)
	if n != 1 {
		t.Fatal("confirmation email not durable")
	}
}
func TestPerListUnsubscribeKeepsOtherEligibleCampaign(t *testing.T) {
	db := trackDB(t)
	for _, q := range []string{`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(2,1,'two','Two','single',1)`, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(2,1,'subscribed',1,1)`, `INSERT INTO campaign_targets VALUES(2,'list',2,'include')`} {
		if _, e := db.Writer.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	secret := []byte(strings.Repeat("x", 32))
	s := New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"})
	mux := http.NewServeMux()
	s.Register(mux)
	r := httptest.NewRequest("POST", "https://track.example.com/u/"+token.Encode(secret, 1, 1), strings.NewReader("list=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var status string
	db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=2`).Scan(&status)
	if status != "pending" {
		t.Fatal("unrelated audience incorrectly skipped", status)
	}
}
