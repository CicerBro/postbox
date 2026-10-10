package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
)

func TestListGoodbyeOverridesAndAllListsDefault(t *testing.T) {
	for _, list := range []string{"1", "all"} {
		t.Run(list, func(t *testing.T) {
			db := trackDB(t)
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
			var payload string
			if e := db.Reader.QueryRow("SELECT payload FROM jobs WHERE queue='transactional_email'").Scan(&payload); e != nil || !strings.Contains(payload, expected) {
				t.Fatalf("goodbye email: %v %s", e, payload)
			}
		})
	}
}
