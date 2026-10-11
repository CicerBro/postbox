package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"postbox/internal/htmlpolicy"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
)

func TestListGoodbyeOverridesAndAllListsDefault(t *testing.T) {
	for _, list := range []string{"1", "all"} {
		t.Run(list, func(t *testing.T) {
			db := trackDB(t)
			if _, e := db.Writer.Exec(`UPDATE brands SET name='Sender & Co' WHERE id=1; UPDATE lists SET name='Private audience segment' WHERE id=1`); e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			for key, value := range map[string]string{"brand.1.goodbye_html": "Brand goodbye", "list.1.goodbye_html": "List goodbye", "brand.1.goodbye_email": `{"subject":"Brand goodbye","html":"Bye"}`, "list.1.goodbye_email": `{"subject":"List goodbye","html":"Bye"}`} {
				if e := db.SetSetting(ctx, key, value); e != nil {
					t.Fatal(e)
				}
			}
			secret := []byte(strings.Repeat("x", 32))
			s := New(db, &Config{TokenSecret: secret, BaseURL: "https://postbox.example.com"})
			mux := http.NewServeMux()
			s.Register(mux)
			preferences := httptest.NewRecorder()
			mux.ServeHTTP(preferences, httptest.NewRequest("GET", "https://track.example.com/u/"+token.Encode(secret, 1, 1), nil))
			if preferences.Code != 200 || !strings.Contains(preferences.Body.String(), `<form method="post">`) {
				t.Fatal("unsubscribe preferences changed", preferences.Code, preferences.Body)
			}
			page := preferences.Body.String()
			if !strings.Contains(page, "Sender &amp; Co") || strings.Contains(page, "Private audience segment") || strings.Contains(page, "<select") || strings.Contains(page, "<footer") {
				t.Fatal("public preferences must show the sender without internal lists or a tagline", page)
			}
			assertTrackingHTMLPolicy(t, preferences.Header(), htmlpolicy.Preferences)
			r := httptest.NewRequest("POST", "https://track.example.com/u/"+token.Encode(secret, 1, 1), strings.NewReader("list="+list))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			expected := "List goodbye"
			if list == "all" {
				expected = "Brand goodbye"
			}
			if w.Code != 200 || w.Body.String() != expected || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatalf("goodbye page: %d %s", w.Code, w.Body)
			}
			assertTrackingHTMLPolicy(t, w.Header(), htmlpolicy.Custom)
			var payload string
			if e := db.Reader.QueryRow("SELECT payload FROM jobs WHERE queue='transactional_email'").Scan(&payload); e != nil || !strings.Contains(payload, expected) {
				t.Fatalf("goodbye email: %v %s", e, payload)
			}
		})
	}
}

func TestCurrentMailingUnsubscribePreservesOtherLists(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "multiple"}[multiple], func(t *testing.T) {
			db := trackDB(t)
			for _, q := range []string{
				`UPDATE lists SET name='Private list one' WHERE id=1`,
				`INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(2,1,'two','Private list two','single',1),(3,1,'three','Private list three','single',1)`,
				`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(2,1,'subscribed',1,1),(3,1,'subscribed',1,1)`,
				`INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',1,'include'),(2,'list',3,'include')`,
			} {
				if _, err := db.Writer.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if multiple {
				if _, err := db.Writer.Exec(`INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',2,'include')`); err != nil {
					t.Fatal(err)
				}
			}
			secret := []byte(strings.Repeat("x", 32))
			mux := http.NewServeMux()
			New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"}).Register(mux)
			url := "https://track.example.com/u/" + token.Encode(secret, 1, 1)
			page := httptest.NewRecorder()
			mux.ServeHTTP(page, httptest.NewRequest("GET", url, nil))
			label := "Unsubscribe from this list"
			if multiple {
				label = "Unsubscribe from these lists"
			}
			if page.Code != 200 || !strings.Contains(page.Body.String(), `class="primary" name="list" value="current">`+label) || !strings.Contains(page.Body.String(), `name="list" value="all"`) || strings.Contains(page.Body.String(), "Private list") {
				t.Fatal("incorrect public unsubscribe choices", page.Code, page.Body)
			}
			r := httptest.NewRequest("POST", url, strings.NewReader("list=current"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for id := 1; id <= 3; id++ {
				want := "subscribed"
				if id == 1 || (id == 2 && multiple) {
					want = "unsubscribed"
				}
				var status string
				if err := db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=1 AND list_id=?`, id).Scan(&status); err != nil || status != want {
					t.Fatalf("list %d: got %s, want %s: %v", id, status, want, err)
				}
			}
			var optedOut *int64
			if err := db.Reader.QueryRow(`SELECT marketing_unsubscribed_at FROM subscribers WHERE id=1`).Scan(&optedOut); err != nil || optedOut != nil {
				t.Fatal("current mailing must not set brand-wide opt-out", optedOut, err)
			}
			var status string
			if err := db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=2 AND subscriber_id=1`).Scan(&status); err != nil || status != "pending" {
				t.Fatal("unrelated campaign should remain eligible", status, err)
			}
		})
	}
}
