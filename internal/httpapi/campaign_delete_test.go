package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"postbox/internal/subscribers"
	"testing"
)

func TestCampaignDeletionStatesAndIsolation(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, name := range []string{"one", "two"} {
		if _, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"draft", "sent", "cancelled", "scheduled", "preparing", "sending", "paused"} {
		t.Run(state, func(t *testing.T) {
			res, err := db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,?,?,'<p>Hello</p>',1,1)", state, state, state)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := res.LastInsertId()
			for _, table := range []string{"opens", "clicks"} {
				sql := "INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(?,7,1)"
				if table == "clicks" {
					sql = "INSERT INTO clicks(campaign_id,link_id,subscriber_id,at) VALUES(?,8,7,1)"
				}
				if _, err := db.Writer.Exec(sql, id); err != nil {
					t.Fatal(err)
				}
			}
			path := fmt.Sprintf("/api/v1/campaigns/%d?brand_id=", id)
			w := request(mux, "DELETE", path+"2", "", &cookie, csrf)
			if w.Code != http.StatusNotFound {
				t.Fatal("cross-brand deletion", w.Code, w.Body.String())
			}
			w = request(mux, "DELETE", path+"1", "", &cookie, csrf)
			allowed := state == "draft" || state == "sent" || state == "cancelled"
			want := http.StatusConflict
			if allowed {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, table := range []string{"campaigns", "opens", "clicks"} {
				var count int
				column := "campaign_id"
				if table == "campaigns" {
					column = "id"
				}
				if err := db.Reader.QueryRow("SELECT count(*) FROM "+table+" WHERE "+column+"=?", id).Scan(&count); err != nil {
					t.Fatal(err)
				}
				expected := 1
				if allowed {
					expected = 0
				}
				if count != expected {
					t.Fatalf("%s count=%d want=%d", table, count, expected)
				}
			}
		})
	}
}

func TestSentCampaignDeletionRequiresAdmin(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	if _, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "one", FromEmail: "from@example.org"}); err != nil {
		t.Fatal(err)
	}
	w := request(mux, "POST", "/api/v1/users", `{"email":"member@example.org","password":"member-long-password"}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	w = request(mux, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", user.ID), `{"permissions":["read","write"]}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-long-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	member := *w.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"draft", "sent", "cancelled"} {
		res, err := db.Writer.Exec("INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,?,?,'hello',1,1)", state, state, state)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		w = request(mux, "DELETE", fmt.Sprintf("/api/v1/campaigns/%d?brand_id=1", id), "", &member, session.CSRF)
		want := http.StatusForbidden
		if state == "draft" {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatal(state, w.Code, w.Body.String())
		}
	}
}
