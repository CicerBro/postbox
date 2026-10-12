package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"postbox/internal/store"
	"postbox/internal/subscribers"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
	"time"
)

func boundToken(t *testing.T, db *store.DB, secret []byte, campaign, subscriber int64) string {
	t.Helper()
	var uuid, identity string
	if err := db.Reader.QueryRow(`SELECT c.tracking_identity,s.tracking_identity FROM campaigns c,subscribers s WHERE c.id=? AND s.id=?`, campaign, subscriber).Scan(&uuid, &identity); err != nil {
		t.Fatal(err)
	}
	return token.EncodeBound(secret, campaign, subscriber, uuid, identity)
}

func TestTrackingCapabilitiesRejectReusedSubscriberIdentity(t *testing.T) {
	for _, version := range []string{"legacy", "bound"} {
		t.Run(version, func(t *testing.T) {
			db := trackDB(t)
			secret := []byte(strings.Repeat("x", 32))
			value := token.Encode(secret, 1, 1)
			if version == "bound" {
				value = boundToken(t, db, secret, 1, 1)
			}
			service := New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"})
			mux := http.NewServeMux()
			service.Register(mux)
			// A valid migrated or bound capability remains usable for the original row.
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", "https://track.example.com/u/"+value, nil))
			if response.Code != 200 {
				t.Fatalf("original token rejected: %d", response.Code)
			}
			if err := subscribers.New(db).Erase(context.Background(), 1, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Writer.Exec(`INSERT INTO subscribers(brand_id,email,fields,created_at,updated_at) VALUES(1,'replacement@example.com','{}',1,1)`); err != nil {
				t.Fatal(err)
			}
			var replacement int64
			if err := db.Reader.QueryRow(`SELECT id FROM subscribers WHERE email='replacement@example.com'`).Scan(&replacement); err != nil {
				t.Fatal(err)
			}
			if replacement != 1 {
				t.Fatalf("fixture did not reproduce SQLite ID reuse: %d", replacement)
			}
			// Even a new snapshot membership for the reused numeric ID cannot revive it.
			for _, query := range []string{`INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'unsubscribed',1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'sent')`} {
				if _, err := db.Writer.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			for _, action := range []struct{ method, path, body string }{{"GET", "/o/", ""}, {"GET", "/u/", ""}, {"POST", "/u/", "list=all"}, {"POST", "/u/", "list=1"}} {
				path := action.path + value
				if action.method == "POST" && action.body == "list=1" {
					path += "/resubscribe"
				}
				request := httptest.NewRequest(action.method, "https://track.example.com"+path, strings.NewReader(action.body))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if response.Code != 404 {
					t.Fatalf("stale %s %s accepted: %d %s", action.method, path, response.Code, response.Body.String())
				}
			}
			var status string
			if err := db.Reader.QueryRow(`SELECT status FROM list_subscribers WHERE subscriber_id=1`).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != "unsubscribed" {
				t.Fatalf("replacement preference changed: %s", status)
			}
		})
	}
}

func TestTrackingCapabilitiesRejectReusedCampaignIdentity(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	values := []string{token.Encode(secret, 2, 1), boundToken(t, db, secret, 2, 1)}
	if _, err := db.Writer.Exec(`DELETE FROM campaigns WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,'other','sending','New','New',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(2,1,'sent')`); err != nil {
		t.Fatal(err)
	}
	s := New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"})
	mux := http.NewServeMux()
	s.Register(mux)
	for _, value := range values {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "https://track.example.com/u/"+value, nil))
		if w.Code != 404 {
			t.Fatalf("replacement campaign accepted old capability: %d", w.Code)
		}
	}
	// A new bound token for the replacement is valid.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "https://track.example.com/u/"+boundToken(t, db, secret, 2, 1), nil))
	if w.Code != 200 {
		t.Fatalf("new campaign bound capability rejected: %d", w.Code)
	}
}

func TestTrackingRequiresSnapshotMembership(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	value := boundToken(t, db, secret, 1, 1)
	if _, err := db.Writer.Exec(`DELETE FROM campaign_recipients WHERE campaign_id=1`); err != nil {
		t.Fatal(err)
	}
	s := New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"})
	mux := http.NewServeMux()
	s.Register(mux)
	for _, path := range []string{"/o/", "/u/", "/c/1/", "/w/1/"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "https://track.example.com"+path+value, nil))
		if w.Code != 404 {
			t.Fatalf("missing snapshot accepted at %s: %d", path, w.Code)
		}
	}
}

func TestQueuedTrackingCannotAttachToReplacement(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	s := New(db, &Config{TokenSecret: secret})
	ev := record{campaign: 1, subscriber: 1, at: 100, raw: true, token: boundToken(t, db, secret, 1, 1)}
	for _, query := range []string{`DELETE FROM subscribers WHERE id=1`, `INSERT INTO subscribers(brand_id,email,fields,created_at,updated_at) VALUES(1,'replacement@example.com','{}',1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'sent')`} {
		if _, err := db.Writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.writeBatch(context.Background(), []record{ev}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM opens`, `SELECT count(*) FROM subscriber_activity`, `SELECT opens_total FROM campaign_stats WHERE campaign_id=1`} {
		var n int
		if err := db.Reader.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("queued stale capability polluted history: %s = %d", query, n)
		}
	}
}

func TestPreferenceMutationRechecksIdentityAfterWriterWait(t *testing.T) {
	for _, suffix := range []string{"", "/resubscribe"} {
		t.Run(suffix, func(t *testing.T) {
			db := trackDB(t)
			secret := []byte(strings.Repeat("x", 32))
			value := boundToken(t, db, secret, 1, 1)
			s := New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"})
			mux := http.NewServeMux()
			s.Register(mux)
			conn, err := db.Writer.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before := db.Writer.Stats().WaitCount
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				request := httptest.NewRequest("POST", "https://track.example.com/u/"+value+suffix, strings.NewReader("list=1"))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, request)
				done <- w
			}()
			deadline := time.Now().Add(5 * time.Second)
			for db.Writer.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if db.Writer.Stats().WaitCount == before {
				t.Fatal("request did not reach mutation transaction")
			}
			for _, query := range []string{`DELETE FROM subscribers WHERE id=1`, `INSERT INTO subscribers(brand_id,email,fields,created_at,updated_at) VALUES(1,'changed@example.com','{}',1,1)`, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'unsubscribed',1,1)`, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(1,1,'sent')`} {
				if _, err := conn.ExecContext(context.Background(), query); err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-done:
				if response.Code == 200 {
					t.Fatal("stale capability survived waiting for mutation transaction")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not complete")
			}
			var status string
			var consent *int64
			if err := db.Reader.QueryRow(`SELECT ls.status,s.gdpr_consent_at FROM subscribers s JOIN list_subscribers ls ON ls.subscriber_id=s.id WHERE s.id=1`).Scan(&status, &consent); err != nil {
				t.Fatal(err)
			}
			if status != "unsubscribed" || consent != nil {
				t.Fatalf("replacement preference changed: %s %v", status, consent)
			}
			var n int
			if err := db.Reader.QueryRow(`SELECT count(*) FROM subscriber_activity WHERE subscriber_id=1`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("stale capability produced replacement activity")
			}
		})
	}
}

func TestUnsubscribeResponseKeepsAuthorizedBrandSnapshot(t *testing.T) {
	db := trackDB(t)
	secret := []byte(strings.Repeat("x", 32))
	value := boundToken(t, db, secret, 1, 1)
	for _, query := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(2,'Private replacement brand','','private@example.org',1)`,
		`INSERT INTO brand_settings(brand_id,name,value) VALUES(1,'goodbye_html','Original goodbye'),(2,'goodbye_html','Private replacement content')`,
		// The mutation completes its authorized transition before identity changes.
		// A trigger provides a deterministic replacement boundary without timing.
		`CREATE TRIGGER replace_unsubscribed_identity AFTER UPDATE OF unsubscribed_at ON campaign_recipients WHEN NEW.campaign_id=1 BEGIN DELETE FROM subscribers WHERE id=1;INSERT INTO subscribers(id,brand_id,email,fields,created_at,updated_at) VALUES(1,2,'replacement@example.org','{}',1,1); END;`,
	} {
		if _, err := db.Writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	New(db, &Config{TokenSecret: secret, BaseURL: "https://track.example.com"}).Register(mux)
	r := httptest.NewRequest("POST", "https://track.example.com/u/"+value, strings.NewReader("list=all"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "Original goodbye" {
		t.Fatalf("unsubscribe response transferred to replacement: %d %s", w.Code, w.Body.String())
	}
}
