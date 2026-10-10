package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSuppressionScopes(t *testing.T) {
	_, mux, db, adminCookie, adminCSRF := setup(t)
	for _, stmt := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'one','One','one@example.org',0),(2,'two','Two','two@example.org',0)`,
		`INSERT INTO suppressions(id,brand_id,kind,value,reason,created_at) VALUES(1,1,'email','brand@example.org','manual',0),(2,NULL,'email','bounce@example.org','hard_bounce',0),(3,2,'email','private@example.org','manual',0),(4,NULL,'email','complaint@example.org','complaint',0),(5,NULL,'domain','blocked.example','ses_sync',0)`,
		`INSERT INTO users(id,email,password_hash,role,created_at) VALUES(2,'user@example.org','unused','brand_user',0)`,
		`INSERT INTO user_brands(user_id,brand_id,permissions) VALUES(2,1,'["read","write"]')`,
		`INSERT INTO subscribers(brand_id,email,status,created_at,updated_at) VALUES(1,'bounce@example.org','bounced',0,0),(1,'complaint@example.org','complained',0,0)`,
	} {
		if _, err := db.Writer.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	userCSRF := "suppression-user-csrf"
	if _, err := db.Writer.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(?,?,?,?)`, digest("suppression-user"), 2, time.Now().Add(time.Hour).UnixMilli(), digest(userCSRF)); err != nil {
		t.Fatal(err)
	}
	userCookie := &http.Cookie{Name: "postbox_session", Value: "suppression-user"}
	w := request(mux, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"suppression-key","scopes":["read","write"]}`, &adminCookie, adminCSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var key map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	do := func(actor, method, path, body string) *httptest.ResponseRecorder {
		if actor == "admin" {
			return request(mux, method, path, body, &adminCookie, adminCSRF)
		}
		if actor == "user" {
			return request(mux, method, path, body, userCookie, userCSRF)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key["secret"].(string))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	for _, actor := range []string{"admin", "user", "key"} {
		t.Run(actor+" effective read", func(t *testing.T) {
			for _, tc := range []struct {
				query string
				ids   []int64
			}{
				{"", []int64{1, 2, 4, 5}}, {"&scope=effective", []int64{1, 2, 4, 5}},
				{"&scope=brand", []int64{1}}, {"&after=2&limit=1", []int64{4}},
			} {
				w := do(actor, "GET", "/api/v1/suppressions?brand_id=1"+tc.query, "")
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var rows []map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
					t.Fatal(err)
				}
				if len(rows) != len(tc.ids) {
					t.Fatalf("%s: unexpected rows %s", tc.query, w.Body.String())
				}
				for j, id := range tc.ids {
					if int64(rows[j]["id"].(float64)) != id {
						t.Fatalf("unexpected rows %s", w.Body.String())
					}
					if id != 1 && rows[j]["brand_id"] != nil {
						t.Fatalf("global row should have null brand_id: %s", w.Body.String())
					}
				}
			}
		})
	}
	for _, actor := range []string{"user", "key"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			path := "/api/v1/suppressions?scope=global"
			if method == "DELETE" {
				path = "/api/v1/suppressions/2?scope=global"
			}
			w := do(actor, method, path, `{"kind":"email","value":"global@example.org"}`)
			if w.Code != 403 {
				t.Fatalf("%s %s global: %d %s", actor, method, w.Code, w.Body.String())
			}
		}
		for _, path := range []string{"/api/v1/suppressions/2?brand_id=1", "/api/v1/suppressions/3?brand_id=1"} {
			if w := do(actor, "DELETE", path, ""); w.Code != 404 {
				t.Fatalf("%s deleting foreign row: %d %s", actor, w.Code, w.Body.String())
			}
		}
		if w := do(actor, "GET", "/api/v1/suppressions?brand_id=2", ""); w.Code != 403 {
			t.Fatalf("%s cross-brand read: %d", actor, w.Code)
		}
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		for _, scope := range []string{"unknown", "all", "effective"} {
			if method == "GET" && scope == "effective" {
				continue
			}
			path := "/api/v1/suppressions?brand_id=1&scope=" + scope
			if method == "DELETE" {
				path = "/api/v1/suppressions/1?brand_id=1&scope=" + scope
			}
			if w := do("admin", method, path, `{"kind":"email","value":"invalid@example.org"}`); w.Code != 400 {
				t.Fatalf("invalid %s scope %s: %d", method, scope, w.Code)
			}
		}
	}
	w = do("admin", "GET", "/api/v1/suppressions?scope=global", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var globals []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &globals); err != nil {
		t.Fatal(err)
	}
	if len(globals) != 3 {
		t.Fatal(w.Body.String())
	}
	if w := do("admin", "POST", "/api/v1/suppressions?scope=global", `{"brand_id":2,"kind":"email","value":"new-global@example.org","reason":"complaint"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var reason string
	var brand any
	if err := db.Reader.QueryRow(`SELECT brand_id,reason FROM suppressions WHERE value='new-global@example.org'`).Scan(&brand, &reason); err != nil || brand != nil || reason != "manual" {
		t.Fatal(brand, reason, err)
	}
	for _, id := range []int64{2, 4} {
		if w := do("admin", "DELETE", fmt.Sprintf("/api/v1/suppressions/%d?scope=global", id), ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ email, status string }{{"bounce@example.org", "bounced"}, {"complaint@example.org", "complained"}} {
		var status string
		if err := db.Reader.QueryRow(`SELECT status FROM subscribers WHERE email=?`, tc.email).Scan(&status); err != nil || status != tc.status {
			t.Fatal(status, err)
		}
	}
	if w := do("admin", "DELETE", "/api/v1/suppressions/1?scope=global", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do("admin", "DELETE", "/api/v1/suppressions/999?scope=global", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSuppressionScopeQueryUsesIndexes(t *testing.T) {
	_, _, db, _, _ := setup(t)
	rows, err := db.Reader.Query(`EXPLAIN QUERY PLAN SELECT * FROM suppressions WHERE brand_id=? AND id>? UNION ALL SELECT * FROM suppressions WHERE brand_id IS NULL AND id>? ORDER BY id LIMIT ?`, 1, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	searches := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN suppressions") {
			t.Fatal(detail)
		}
		if strings.Contains(detail, "SEARCH suppressions USING INDEX suppressions_brand") {
			searches++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if searches != 2 {
		t.Fatalf("expected two indexed scope searches, got %d", searches)
	}
}
