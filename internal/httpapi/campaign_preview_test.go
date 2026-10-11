package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"postbox/internal/htmlpolicy"
	"postbox/internal/send"
	"postbox/internal/subscribers"
	"regexp"
	"strings"
	"testing"
	"time"
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
	assertHTMLPolicy(t, w.Header(), htmlpolicy.Email)
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing preview privacy header")
	}
	w = request(mux, "GET", unsubscribe, "", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "selected@example.org") || !strings.Contains(w.Body.String(), "Readers") {
		t.Fatal(w.Code, w.Body)
	}
	assertHTMLPolicy(t, w.Header(), htmlpolicy.UnsubscribePreview)
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
		assertHTMLPolicy(t, w.Header(), htmlpolicy.UnsubscribePreview)
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

// Exercise actual test-email MIME and the public routes it links to.
func TestTestEmailPreviewLinks(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("selected=%t", selected), func(t *testing.T) {
			a, mux, db, cookie, csrf := setup(t)
			sender := &previewMailSender{}
			a.engine = send.New(db, sender, send.Config{BaseURL: "https://example.org", TokenSecret: []byte("long-testing-token-secret-value")})
			ctx := context.Background()
			s := subscribers.New(db)
			brand, err := s.CreateBrand(ctx, subscribers.Brand{Name: "Preview", FromEmail: "sender@example.org", TrackingDomain: "https://tracking.example.org"})
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
			res, err := db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,'mail-preview','draft','Hello {{ email }}',?,0,0)`, brand.ID, `<p>Hello {{ name }} {{ email }}</p><a href="{{ webversion }}">Browser</a><a href="{{ unsubscribe }}">Unsubscribe</a><a href="https://news.example.org">News</a>`)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := res.LastInsertId()
			subscriberID := int64(0)
			if selected {
				subscriberID = sub.ID
			}
			before := map[string]int{}
			for _, table := range []string{"links", "campaign_recipients", "campaign_preparations", "subscriber_activity"} {
				var count int
				if err = db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				before[table] = count
			}

			payload, _ := json.Marshal(map[string]any{"emails": []string{"inbox@example.org"}})
			var w *httptest.ResponseRecorder
			if selected {
				if err := a.engine.TestCampaign(ctx, id, []string{"inbox@example.org"}, subscriberID); err != nil {
					t.Fatal(err)
				}
			} else {
				w = request(mux, "POST", fmt.Sprintf("/api/v1/campaigns/%d/test-send?brand_id=%d", id, brand.ID), string(payload), &cookie, csrf)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			}
			if len(sender.messages) != 1 {
				t.Fatal(len(sender.messages))
			}
			msg, err := mail.ReadMessage(bytes.NewReader(sender.messages[0].Raw))
			if err != nil {
				t.Fatal(err)
			}
			_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			parts := multipart.NewReader(msg.Body, params["boundary"])
			var body string
			for {
				part, err := parts.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
					body = string(data)
				}
			}
			links := regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(body, -1)
			if len(links) != 3 || links[2][1] != "https://news.example.org" || strings.Contains(body, "/o/") || !strings.Contains(body, "inbox@example.org") {
				t.Fatal(body)
			}
			browser, unsubscribe := links[0][1], links[1][1]
			if !strings.HasPrefix(browser, "https://example.org/preview/campaign/") || !strings.HasPrefix(unsubscribe, "https://example.org/preview/unsubscribe/") || msg.Header.Get("List-Unsubscribe") != "<"+unsubscribe+">" {
				t.Fatal(body, msg.Header)
			}
			claims, err := a.engine.PreviewIdentity(ctx, strings.TrimPrefix(browser, "https://example.org/preview/campaign/"))
			if err != nil || claims.Subscriber != subscriberID || claims.Expires < time.Now().Add(6*24*time.Hour).Unix() {
				t.Fatal(claims, err)
			}
			w = request(mux, "GET", browser, "", nil, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Hello") {
				t.Fatal(w.Code, w.Body)
			}
			w = request(mux, "GET", unsubscribe, "", nil, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Preview only") {
				t.Fatal(w.Code, w.Body)
			}
			selections := regexp.MustCompile(`name="selection" value="([^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
			if len(selections) == 0 {
				t.Fatal(w.Body)
			}
			for _, selection := range selections {
				r := httptest.NewRequest("POST", unsubscribe, strings.NewReader(url.Values{"selection": {selection[1]}}.Encode()))
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
			for table, expected := range before {
				var count int
				if err = db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != expected {
					t.Fatal(table, count, err)
				}
			}
		})
	}
}

type previewMailSender struct{ messages []send.Request }

func (s *previewMailSender) GetAccount(context.Context) (send.Quota, error) {
	return send.Quota{MaxSendRate: 10000, Max24HourSend: 100000}, nil
}
func (s *previewMailSender) Send(_ context.Context, r send.Request) (string, error) {
	s.messages = append(s.messages, r)
	return "test-message", nil
}
