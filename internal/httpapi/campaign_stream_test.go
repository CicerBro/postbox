package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"postbox/internal/store"
	"postbox/internal/subscribers"
	"strings"
	"testing"
	"time"
)

func TestCampaignStreamRevalidatesAccess(t *testing.T) {
	for _, test := range []struct {
		name   string
		bearer bool
		user   bool
		change string
	}{
		{name: "logout", change: "DELETE FROM sessions"},
		{name: "expiry", change: "UPDATE sessions SET expires_at=0"},
		{name: "admin demotion", change: "UPDATE users SET role='brand_user'"},
		{name: "brand access removal", user: true, change: "DELETE FROM user_brands"},
		{name: "read permission removal", user: true, change: `UPDATE user_brands SET permissions='["write"]'`},
		{name: "key revocation", bearer: true, change: "UPDATE api_keys SET revoked_at=1"},
		{name: "key read scope removal", bearer: true, change: `UPDATE api_keys SET scopes='["write"]'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, mux, db, cookie, _ := setup(t)
			brand := streamCampaignFixture(t, db)
			if test.user {
				execStreamSQL(t, db, "UPDATE users SET role='brand_user'")
				execStreamSQL(t, db, `INSERT INTO user_brands(user_id,brand_id,permissions) VALUES(1,?,'["read"]')`, brand)
			}
			key := "stream-test.secret"
			if test.bearer {
				execStreamSQL(t, db, `INSERT INTO api_keys(brand_id,name,prefix,secret_hash,scopes,created_at) VALUES(?,'Stream','stream-test',?,'["read"]',0)`, brand, digest(key))
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			r, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/campaigns/1/events?brand_id=%d", server.URL, brand), nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.bearer {
				r.Header.Set("Authorization", "Bearer "+key)
			} else {
				r.AddCookie(&cookie)
			}
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal(response.StatusCode)
			}
			reader := bufio.NewReader(response.Body)
			readStatsEvent(t, reader)
			execStreamSQL(t, db, test.change)
			execStreamSQL(t, db, "UPDATE campaign_stats SET delivered=99 WHERE campaign_id=1")
			rest, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("stream did not close after access change: %v", err)
			}
			if len(rest) != 0 {
				t.Fatalf("stream disclosed statistics after access was removed: %s", rest)
			}
		})
	}
}

func TestCampaignStreamUpdatesAndDoesNotConsumeRequestQuota(t *testing.T) {
	a, mux, db, _, _ := setup(t)
	brand := streamCampaignFixture(t, db)
	key := "updates.secret"
	execStreamSQL(t, db, `INSERT INTO api_keys(brand_id,name,prefix,secret_hash,scopes,created_at) VALUES(?,'Stream','updates',?,'["read"]',0)`, brand, digest(key))
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v1/campaigns/1/events?brand_id=%d", server.URL, brand), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	readStatsEvent(t, reader)
	execStreamSQL(t, db, "UPDATE campaign_stats SET delivered=99 WHERE campaign_id=1")
	if event := readStatsEvent(t, reader); !strings.Contains(event, `"delivered":99`) {
		t.Fatalf("updated counters missing: %s", event)
	}
	a.mu.Lock()
	requests := a.limits["api:updates"].n
	a.mu.Unlock()
	if requests != 1 {
		t.Fatalf("stream updates consumed %d requests", requests)
	}
	cancel()
}

func streamCampaignFixture(t *testing.T, db *store.DB) int64 {
	t.Helper()
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Stream", FromEmail: "stream@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	execStreamSQL(t, db, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,?,'stream','sent','Stream','<p>Stream</p>',1,1)`, brand.ID)
	execStreamSQL(t, db, "INSERT INTO campaign_stats(campaign_id) VALUES(1)")
	return brand.ID
}

func execStreamSQL(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func readStatsEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var event strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		event.WriteString(line)
		if line == "\n" {
			break
		}
	}
	if !strings.HasPrefix(event.String(), "event: stats\ndata: ") {
		t.Fatalf("unexpected stream event: %s", event.String())
	}
	return event.String()
}
