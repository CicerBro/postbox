package send

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"postbox/internal/tracking"
	"postbox/internal/tracking/token"
	"regexp"
	"strings"
	"testing"
)

func TestProductionDispatchUsesImmutableTrackingCapabilities(t *testing.T) {
	db, e, _ := fixture(t, 1)
	ctx := context.Background()
	campaign, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(ctx, campaign); err != nil {
		t.Fatal(err)
	}
	recipients, err := e.claim(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0].TrackingIdentity == "" {
		t.Fatal("claim omitted immutable subscriber identity")
	}
	raw, err := e.render(campaign, recipients[0])
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe := strings.Trim(message.Header.Get("List-Unsubscribe"), "<>")
	value := strings.TrimPrefix(unsubscribe, campaign.trackingURL+"/u/")
	if !token.MatchesIdentity(e.cfg.TokenSecret, value, campaign.id, recipients[0].ID, campaign.trackingIdentity, recipients[0].TrackingIdentity) {
		t.Fatal("production unsubscribe header uses an unbound capability", unsubscribe)
	}
	mux := http.NewServeMux()
	tracking.New(db, &tracking.Config{TokenSecret: e.cfg.TokenSecret, BaseURL: campaign.trackingURL}).Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", unsubscribe, nil))
	if response.Code != 200 {
		t.Fatalf("production tracking capability rejected: %d %s", response.Code, response.Body.String())
	}
}

func TestRecipientPreviewUsesImmutableTrackingCapability(t *testing.T) {
	db, e, _ := fixture(t, 1)
	ctx := context.Background()
	campaign, err := e.prepare(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.snapshot(ctx, campaign); err != nil {
		t.Fatal(err)
	}
	_, body, _, err := e.Preview(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`https://track\.example\.org/u/([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("preview omitted recipient capability: %s", body)
	}
	mux := http.NewServeMux()
	tracking.New(db, &tracking.Config{TokenSecret: e.cfg.TokenSecret, BaseURL: campaign.trackingURL}).Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", match[0], nil))
	if response.Code != 200 {
		t.Fatalf("preview capability rejected: %d %s", response.Code, response.Body.String())
	}
}
