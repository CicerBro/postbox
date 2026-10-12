package send

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"postbox/internal/render"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type preflightTransport func(*http.Request) (*http.Response, error)

func (f preflightTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func preflightTestClient(status int) *http.Client {
	return &http.Client{Transport: preflightTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})}
}
func TestPreflightURLPolicy(t *testing.T) {
	for _, raw := range []string{"http://localhost/a", "http://foo.localhost/", "http://metadata.google.internal/", "http://127.0.0.1/", "http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://10.0.0.1/", "http://169.254.169.254/", "http://100.100.100.200/", "file:///etc/passwd", "https://user:pass@example.org/", "http://example.org:9000/"} {
		if publicPreflightURL(raw) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if err := publicPreflightURL("https://example.org/path"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"192.168.1.1", "169.254.1.1", "fc00::1", "100.64.0.1", "192.0.2.1"} {
		if publicPreflightIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted %s", raw)
		}
	}
	if !publicPreflightIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}
func TestPreflightBoundedHEADAndRedirects(t *testing.T) {
	var active, maximum, requests atomic.Int64
	client := &http.Client{Transport: preflightTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "HEAD" {
			t.Error(r.Method)
		}
		requests.Add(1)
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	links := make([]render.Link, 20)
	for i := range links {
		links[i] = render.Link{URL: "https://example.org/a"}
	}
	got := checkPreflightLinks(context.Background(), links, nil, render.Recipient{}, PreflightOptions{Client: client})
	if requests.Load() != 20 || maximum.Load() > 4 {
		t.Fatalf("requests %d concurrency %d", requests.Load(), maximum.Load())
	}
	for _, l := range got {
		if l.Status != "warning" || l.HTTPStatus != 302 {
			t.Fatalf("%+v", l)
		}
	}
	blocked := checkPreflightLinks(context.Background(), []render.Link{{URL: "http://localhost/"}}, nil, render.Recipient{}, PreflightOptions{Client: client})
	if blocked[0].Status != "blocked" || requests.Load() != 20 {
		t.Fatal(blocked)
	}
}
func TestChecklistCountsClippingAndReadOnly(t *testing.T) {
	db, e, _ := fixture(t, 4)
	for _, q := range []string{
		`UPDATE subscribers SET status='bounced' WHERE id=1`,
		`INSERT INTO suppressions(brand_id,kind,value,reason,created_at) VALUES(1,'email','reader2@example.org','manual',1)`,
		`DELETE FROM list_subscribers WHERE subscriber_id=4`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	html := "<html><body>" + strings.Repeat("a", 102*1024) + `<a href="https://example.org/a">Read</a></body></html>`
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html=? WHERE id=1`, html); err != nil {
		t.Fatal(err)
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Recipients != 1 || got.Checks.Suppressed != 1 || got.Checks.Inactive != 1 || got.Checks.Skipped != 2 || !got.Checks.HTMLClipping || !got.Checks.Ready {
		t.Fatalf("%+v", got)
	}
	for _, table := range []string{"campaign_preparations", "campaign_recipients", "campaign_user_reservations", "links"} {
		var n int
		if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("mutated %s %d %v", table, n, err)
		}
	}
}
func TestChecklistMissingTagsAndAudience(t *testing.T) {
	db, e, _ := fixture(t, 0)
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<p>[MissingOne] {{MissingTwo}}</p>' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil || len(got.Checks.Errors) != 2 || got.Checks.Ready {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<p>hello</p>' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	got, err = e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil || got.Checks.Ready || got.Checks.Errors[0].Code != "empty_audience" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := db.Writer.Exec(`DELETE FROM campaign_targets`); err != nil {
		t.Fatal(err)
	}
	got, err = e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil || got.Checks.Ready || got.Checks.Errors[0].Code != "audience" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestChecklistBrokenAndPersonalizedLinks(t *testing.T) {
	db, e, _ := fixture(t, 1)
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<a href="https://example.org/a?email=[email]">Read</a>' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(404)})
	if err != nil || !got.Checks.Ready || len(got.Checks.Errors) != 0 || len(got.Checks.Links) != 1 || !got.Checks.Links[0].Personalized || len(got.Checks.Warnings) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestChecklistUserQuotaReadOnly(t *testing.T) {
	db, e, _ := fixture(t, 3)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,role,created_at) VALUES(1,'owner@example.org','test','brand_user',1)`,
		`INSERT INTO user_brands(user_id,brand_id,permissions,daily_send_limit) VALUES(1,1,'["send"]',2)`,
		`UPDATE campaigns SET created_by=1 WHERE id=1`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Checks.UserRemainingDailyQuota == nil || *got.Checks.UserRemainingDailyQuota != 2 || got.Checks.Ready {
		t.Fatalf("%+v", got)
	}
	found := false
	for _, issue := range got.Checks.Errors {
		if issue.Code == "user_quota" {
			found = true
		}
	}
	if !found {
		t.Fatal(got.Checks.Errors)
	}
	var n int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM user_brand_daily`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("budget mutated %d %v", n, err)
	}
}

func TestChecklistMissingRecipientFieldWarning(t *testing.T) {
	db, e, _ := fixture(t, 3)
	for _, q := range []string{
		`INSERT INTO custom_fields(brand_id,key,label,type) VALUES(1,'company','Company','text')`,
		`UPDATE campaigns SET html='<p>[company] {{company | "there"}}</p>' WHERE id=1`,
		`UPDATE subscribers SET fields='{"company":"Acme"}' WHERE id=1`,
		`DELETE FROM list_subscribers WHERE subscriber_id=3`,
	} {
		if _, err := db.Writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: preflightTestClient(200)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range got.Checks.Warnings {
		if issue.Code == "missing_merge_value" && issue.Tag == "company" && strings.HasPrefix(issue.Message, "1 eligible") {
			found = true
		}
	}
	if !found || !got.Checks.Ready {
		t.Fatalf("%+v", got)
	}
}

func TestChecklistLinkFailuresAreAdvisory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		url    string
		client *http.Client
		status string
	}{
		{"forbidden", "https://example.org/premium", preflightTestClient(403), "broken"},
		{"not_found", "https://example.org/missing", preflightTestClient(404), "broken"},
		{"server_error", "https://example.org/", preflightTestClient(503), "broken"},
		{"timeout", "https://example.org/", &http.Client{Transport: preflightTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("request timed out")
		})}, "broken"},
		{"restricted_url", "http://localhost/", preflightTestClient(200), "blocked"},
		{"redirect", "https://example.org/", preflightTestClient(302), "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, e, _ := fixture(t, 1)
			if _, err := db.Writer.Exec(`UPDATE campaigns SET html=? WHERE id=1`, `<a href="`+tc.url+`">Read</a>`); err != nil {
				t.Fatal(err)
			}
			got, err := e.Checklist(context.Background(), 1, PreflightOptions{Client: tc.client})
			if err != nil || !got.Checks.Ready || len(got.Checks.Errors) != 0 || len(got.Checks.Links) != 1 || got.Checks.Links[0].Status != tc.status {
				t.Fatalf("%+v %v", got, err)
			}
			if len(got.Checks.Warnings) != 1 || got.Checks.Warnings[0].Code != "link" || !strings.Contains(got.Checks.Warnings[0].Message, tc.url) {
				t.Fatalf("missing link notification: %+v", got.Checks.Warnings)
			}
		})
	}
}
