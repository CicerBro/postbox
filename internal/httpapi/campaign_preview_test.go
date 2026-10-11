package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"postbox/internal/subscribers"
	"regexp"
	"strings"
	"testing"
)

func TestSavedCampaignAndUnsubscribePreview(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	s := subscribers.New(db)
	brand, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Preview", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: "selected@example.org", Name: "Selected", ListIDs: []int64{list.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,'preview-flow','draft','Saved','<p>Old</p>',0,0)`, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	tables := []string{"links", "campaign_preparations", "campaign_recipients", "subscriber_activity", "jobs"}
	before := map[string]int{}
	for _, table := range tables {
		var n int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		before[table] = n
	}

	payload, _ := json.Marshal(map[string]any{"campaign_id": id, "subscriber_id": sub.ID, "html": `<a href="{{ webversion }}">Browser</a><a href="{{ unsubscribe }}">Unsubscribe</a>`})
	w := request(mux, "POST", fmt.Sprintf("/api/v1/email-preview?brand_id=%d", brand.ID), string(payload), &cookie, csrf)
	var out struct{ HTML string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Code, w.Body)
	}
	links := regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(out.HTML, -1)
	if len(links) != 2 {
		t.Fatal(out.HTML)
	}
	browser, unsubscribe := links[0][1], links[1][1]
	// Links issued before saving must load the newest saved content, without send snapshots.
	_, err = db.Writer.Exec(`UPDATE campaigns SET html=? WHERE id=?`, `<p>Latest saved {{ name }} {{ email }}</p><a href="{{ unsubscribe }}">Unsubscribe</a>`, id)
	if err != nil {
		t.Fatal(err)
	}
	w = request(mux, "GET", browser, "", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Latest saved Selected selected@example.org") {
		t.Fatal(w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing privacy headers")
	}
	w = request(mux, "GET", unsubscribe, "", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "selected@example.org") || !strings.Contains(w.Body.String(), "Readers") {
		t.Fatal(w.Code, w.Body)
	}
	selections := regexp.MustCompile(`name="selection" value="([^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(selections) != 2 {
		t.Fatal(w.Body)
	}
	for _, v := range selections {
		r := httptest.NewRequest("POST", unsubscribe, strings.NewReader(url.Values{"selection": {v[1]}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "No subscription was changed") {
			t.Fatal(w.Code, w.Body)
		}
	}
	var status string
	if err = db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=? AND list_id=?`, sub.ID, list.ID).Scan(&status); err != nil || status != "subscribed" {
		t.Fatal(status, err)
	}
	for _, table := range tables {
		var n int
		if err = db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != before[table] {
			t.Fatalf("preview wrote %s: %d %v", table, n, err)
		}
	}
	w = request(mux, "GET", strings.Replace(browser, "example.org", "wrong.example", 1), "", nil, "")
	if w.Code != 404 {
		t.Fatal("wrong host accepted", w.Code)
	}
	w = request(mux, "GET", browser[:len(browser)-5]+"aaaaa", "", nil, "")
	if w.Code != 404 {
		t.Fatal("tamper accepted", w.Code)
	}
	_, err = db.Writer.Exec(`UPDATE subscribers SET status='blocklisted' WHERE id=?`, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = request(mux, "GET", browser, "", nil, "")
	if w.Code != 404 {
		t.Fatal("inactive subscriber accepted", w.Code)
	}
}
