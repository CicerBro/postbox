package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
	"time"
)

func TestEditorPreviewRendersUnsavedMergeTags(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Preview", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO custom_fields(brand_id,key,label,type) VALUES(?,?,?,?)`, brand.ID, "city", "City", "text"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/email-preview?brand_id=%d", brand.ID)
	source := `<style>p{color:red}</style><p>Hello {{ name }} ({{ email }}), {{ city | "Amsterdam" }} {{ currentyear }}</p><a href="{{ unsubscribe }}">Unsubscribe</a><a href="{{ webversion }}">Browser</a><a href="[unsubscribe_url]">Legacy</a><a href="https://example.net/article">Article</a>`
	for _, domain := range []string{"", "mail.example.org/"} {
		if _, err = db.Writer.Exec(`UPDATE brands SET tracking_domain=? WHERE id=?`, domain, brand.ID); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"html": source, "inline_css": true})
		w := request(mux, "POST", path, string(payload), &cookie, csrf)
		if w.Code != 200 {
			t.Fatalf("preview: %d %s", w.Code, w.Body)
		}
		var out struct{ HTML string }
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		base := "https://example.org"
		if domain != "" {
			base = "https://mail.example.org"
		}
		for _, want := range []string{"Hello there (preview@example.org), Amsterdam " + time.Now().Format("2006"), base + "/u/preview", base + "/w/0/preview", `href="https://example.net/article"`, "color:red"} {
			if !strings.Contains(out.HTML, want) {
				t.Fatalf("missing %q in %s", want, out.HTML)
			}
		}
		if strings.Contains(out.HTML, "{{") || strings.Contains(out.HTML, "[unsubscribe_url]") {
			t.Fatalf("unresolved tags: %s", out.HTML)
		}
	}
	for _, table := range []string{"campaigns", "links", "jobs"} {
		var count int
		if err = db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("preview wrote %s: %d, %v", table, count, err)
		}
	}
	for _, source := range []string{`<p>{{ nonexistent }}</p>`, `<p>{{ city | invalid }}</p>`, `<p>{{ name`} {
		body, _ := json.Marshal(map[string]any{"html": source})
		w := request(mux, "POST", path, string(body), &cookie, csrf)
		if w.Code != 400 {
			t.Fatalf("invalid tags accepted: %d %s", w.Code, w.Body)
		}
	}
	w := request(mux, "POST", path, `{"html":"hello"}`, nil, "")
	if w.Code != 401 {
		t.Fatalf("unauthenticated preview: %d", w.Code)
	}
}

func TestEditorPreviewSubscriberValuesAndFallback(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	ctx := context.Background()
	s := subscribers.New(db)
	brand, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Preview", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Other", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Readers"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO custom_fields(brand_id,key,label,type) VALUES(?,?,?,?)`, brand.ID, "username", "Username", "text"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: "first@example.org", Name: "First", Fields: map[string]any{"username": "Dark<Shines>"}, ListIDs: []int64{list.ID}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: brand.ID, Email: "second@example.org", ListIDs: []int64{list.ID}})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: other.ID, Email: "foreign@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   int64
		want string
		code int
	}{
		{first.ID, "Hi Dark&lt;Shines&gt;! First first@example.org", 200},
		{second.ID, "Hi reader! friend second@example.org", 200},
		{foreign.ID, "", 404},
		{-1, "", 400},
	} {
		body, _ := json.Marshal(map[string]any{
			"subscriber_id": tc.id,
			"html":          `<p>Hi {{ username | "reader" }}! {{ name | "friend" }} {{ email }}</p>`,
			"subject":       `Hello {{ name | "friend" }}`,
			"text":          `Hi {{ username | "reader" }}`,
		})
		w := request(mux, "POST", fmt.Sprintf("/api/v1/email-preview?brand_id=%d", brand.ID), string(body), &cookie, csrf)
		if w.Code != tc.code {
			t.Fatalf("subscriber %d: %d %s", tc.id, w.Code, w.Body)
		}
		if tc.code != 200 {
			continue
		}
		var out struct{ HTML, Subject, Text string }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.HTML, tc.want) {
			t.Fatalf("subscriber %d: %s", tc.id, out.HTML)
		}
		if tc.id == second.ID && (out.Subject != "Hello friend" || out.Text != "Hi reader") {
			t.Fatalf("fallbacks: %+v", out)
		}
	}
	// The preview browser's first, next and previous cursors stay within the list.
	for _, tc := range []struct {
		after int64
		ids   []int64
	}{{0, []int64{first.ID, second.ID}}, {first.ID, []int64{second.ID}}, {0, []int64{first.ID, second.ID}}, {second.ID, nil}} {
		path := fmt.Sprintf("/api/v1/subscribers?brand_id=%d&list_id=%d&status=active&membership_status=subscribed&after=%d&limit=2", brand.ID, list.ID, tc.after)
		w := request(mux, "GET", path, "", &cookie, "")
		var out []subscribers.Subscriber
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != len(tc.ids) {
			t.Fatalf("page: %d %s", w.Code, w.Body)
		}
		for i, id := range tc.ids {
			if out[i].ID != id {
				t.Fatalf("wrong subscriber: %+v", out)
			}
		}
	}
	var campaignIDs []int64
	for _, brandID := range []int64{brand.ID, other.ID} {
		res, err := db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,?,'draft','Saved','<p>Saved</p>',0,0)`, brandID, fmt.Sprintf("preview-%d", brandID))
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		campaignIDs = append(campaignIDs, id)
	}
	for i, campaignID := range campaignIDs {
		body, _ := json.Marshal(map[string]any{"subscriber_id": first.ID, "campaign_id": campaignID, "html": `<a href="{{ unsubscribe }}">Unsubscribe</a><a href="{{ webversion }}">Browser</a>`})
		w := request(mux, "POST", fmt.Sprintf("/api/v1/email-preview?brand_id=%d", brand.ID), string(body), &cookie, csrf)
		if i == 1 {
			if w.Code != 404 {
				t.Fatalf("foreign campaign: %d %s", w.Code, w.Body)
			}
			continue
		}
		var out struct{ HTML string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("action URLs: %d %s", w.Code, w.Body)
		}
		tok := token.Encode([]byte("long-testing-token-secret-value"), campaignID, first.ID)
		for _, want := range []string{"https://example.org/u/" + tok, fmt.Sprintf("https://example.org/w/%d/%s", campaignID, tok)} {
			if !strings.Contains(out.HTML, want) {
				t.Fatalf("missing recipient URL %q: %s", want, out.HTML)
			}
		}
	}
}
