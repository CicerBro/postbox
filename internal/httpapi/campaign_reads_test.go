package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestCampaignOptionsArePaginatedAndExcludeContent(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	service := subscribers.New(db)
	brand, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Choices", FromEmail: "choices@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Private", FromEmail: "private@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 4; id++ {
		scope := brand.ID
		if id == 3 {
			scope = other.ID
		}
		execStreamSQL(t, db, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,text,created_at,updated_at) VALUES(?,?,?,'draft',?,'<p>Private HTML</p>','Private text',1,1)`, id, scope, fmt.Sprint(id), fmt.Sprintf("Campaign %d", id))
	}
	path := fmt.Sprintf("/api/v1/campaigns/options?brand_id=%d&limit=2", brand.ID)
	w := request(mux, "GET", path, "", &cookie, "")
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if len(rows) != 2 || rows[0]["id"] != float64(1) || rows[1]["id"] != float64(2) || len(rows[0]) != 2 || len(rows[1]) != 2 {
		t.Fatal(rows)
	}
	w = request(mux, "GET", path+"&after=2", "", &cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0]["id"] != float64(4) {
		t.Fatal(w.Body.String(), err)
	}
	// The dedicated path still applies API-key brand isolation.
	key := request(mux, "POST", fmt.Sprintf("/api/v1/api-keys?brand_id=%d", brand.ID), `{"name":"choices","scopes":["read"]}`, &cookie, csrf)
	var created struct{ Secret string }
	if err := json.Unmarshal(key.Body.Bytes(), &created); err != nil || key.Code != 200 {
		t.Fatal(key.Code, key.Body.String(), err)
	}
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/campaigns/options?brand_id=%d", other.ID), nil)
	r.Header.Set("Authorization", "Bearer "+created.Secret)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCampaignSummariesBoundLargeMessagesAndKeepLegacyContent(t *testing.T) {
	_, mux, db, cookie, _ := setup(t)
	brand := streamCampaignFixture(t, db)
	html := "<!DOCTYPE html><html><head><style>" + strings.Repeat(".x{color:red}", 100000) + "</style><title>Hidden title</title></head><body><script>Hidden script</script><p>Hello &amp; welcome</p><p>" + strings.Repeat("More content ", 100000) + "</p></body></html>"
	execStreamSQL(t, db, "UPDATE campaigns SET html=?,text=? WHERE id=1", html, strings.Repeat("Plaintext ", 100000))
	path := fmt.Sprintf("/api/v1/campaigns?brand_id=%d", brand)
	w := request(mux, "GET", path+"&summary=true&offset=0&sort=subject", "", &cookie, "")
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || w.Code != 200 || len(rows) != 1 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if w.Body.Len() > 1000 || rows[0]["html"] != nil || rows[0]["text"] != nil || !strings.HasPrefix(rows[0]["excerpt"].(string), "Hello & welcome More content") || len([]rune(rows[0]["excerpt"].(string))) > 110 {
		t.Fatal("unbounded or invalid excerpt", rows[0])
	}
	w = request(mux, "GET", path, "", &cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || rows[0]["html"] != html {
		t.Fatal("legacy list lost full content", err)
	}
	w = request(mux, "GET", fmt.Sprintf("/api/v1/campaigns/1?brand_id=%d&summary=true", brand), "", &cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || rows[0]["html"] != html {
		t.Fatal("single campaign lost full content", err)
	}
}

func TestCampaignExcerptVisibleText(t *testing.T) {
	for _, test := range []struct{ html, want string }{
		{"<head><title>Hidden</title><style>Hidden</style></head><p>Hello<br>world</p>", "Hello world"},
		{"<p> A &amp; B </p><script>hidden</script><style>hidden</style><p> C </p>", "A & B C"},
		{"<p>" + strings.Repeat("é", 200) + "</p>", strings.Repeat("é", 110)},
	} {
		if got := campaignExcerpt(test.html); got != test.want {
			t.Fatalf("excerpt: %q, want %q", got, test.want)
		}
	}
}
