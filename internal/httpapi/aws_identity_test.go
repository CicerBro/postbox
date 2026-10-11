package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"postbox/internal/awsbrand"
	"postbox/internal/subscribers"
)

func TestSESIdentitySetupUsesBrandConnection(t *testing.T) {
	created, mailFrom, behavior, reject := false, "", "USE_DEFAULT_VALUE", false
	var mutations int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=brand-domain-key/") || !strings.Contains(r.Header.Get("Authorization"), "/eu-west-1/") {
			t.Error("request did not use selected brand keys and region")
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if reject {
			w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"brand-domain-key secret-value-never-returned"}`)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/v2/email/identities":
			var in struct{ EmailIdentity string }
			json.NewDecoder(r.Body).Decode(&in)
			if in.EmailIdentity != "example.org" {
				t.Error("identity was not normalized", in)
			}
			mutations++
			if created {
				w.Header().Set("X-Amzn-ErrorType", "AlreadyExistsException")
				w.WriteHeader(400)
				fmt.Fprint(w, `{"message":"already exists"}`)
				return
			}
			created = true
			fmt.Fprint(w, `{"IdentityType":"DOMAIN","VerifiedForSendingStatus":false,"DkimAttributes":{"SigningEnabled":true,"Status":"PENDING","SigningAttributesOrigin":"AWS_SES","SigningHostedZone":"dkim.eu-west-1.amazonses.com","Tokens":["one","two","three"]}}`)
		case r.Method == "GET" && r.URL.Path == "/v2/email/identities/example.org":
			if !created {
				w.Header().Set("X-Amzn-ErrorType", "NotFoundException")
				w.WriteHeader(404)
				fmt.Fprint(w, `{"message":"missing"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"IdentityType": "DOMAIN", "VerifiedForSendingStatus": true, "VerificationStatus": "SUCCESS", "DkimAttributes": map[string]any{"SigningEnabled": true, "Status": "SUCCESS", "SigningAttributesOrigin": "AWS_SES", "SigningHostedZone": "dkim.eu-west-1.amazonses.com", "Tokens": []string{"one", "two", "three"}}, "MailFromAttributes": map[string]any{"MailFromDomain": mailFrom, "MailFromDomainStatus": "PENDING", "BehaviorOnMxFailure": behavior}})
		case r.Method == "PUT" && r.URL.Path == "/v2/email/identities/example.org/mail-from":
			var in struct{ MailFromDomain, BehaviorOnMxFailure string }
			json.NewDecoder(r.Body).Decode(&in)
			mailFrom, behavior = in.MailFromDomain, in.BehaviorOnMxFailure
			mutations++
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected AWS request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	a, m, db, c, csrf := setup(t)
	p, err := awsbrand.New(db, []byte(strings.Repeat("x", 32)), "us-east-1", upstream.URL, 1)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.AWSProvider = p
	for _, name := range []string{"one", "two"} {
		if _, err = subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.Save(context.Background(), 1, awsbrand.Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "brand-domain-key", SecretAccessKey: "secret-value-never-returned"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/aws-settings/identity?brand_id=1"
	get := path + "&identity=example.org"
	if w := request(m, "GET", get, "", &c, ""); w.Code != 404 {
		t.Fatal("missing identity", w.Code, w.Body.String())
	}
	if w := request(m, "POST", path, `{"identity":"example.org"}`, &c, ""); w.Code != 403 || mutations != 0 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	w := request(m, "POST", path, `{"identity":" EXAMPLE.org. "}`, &c, csrf)
	var out sesIdentityResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.VerificationStatus != "PENDING" || out.SendingEnabled || !out.CustomEndpoint || out.Region != "eu-west-1" || out.CheckedAt == 0 || len(out.DNSRecords) != 3 {
		t.Fatal("identity create response", w.Code, w.Body.String())
	}
	if out.DNSRecords[0].Name != "one._domainkey.example.org" || out.DNSRecords[0].Value != "one.dkim.eu-west-1.amazonses.com" {
		t.Fatal("incorrect AWS signing zone", out.DNSRecords)
	}
	// Existing identities return their current settings rather than a creation error.
	w = request(m, "POST", path, `{"identity":"example.org"}`, &c, csrf)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.SendingEnabled || out.VerificationStatus != "SUCCESS" {
		t.Fatal("existing identity was not loaded", w.Code, w.Body.String())
	}
	mailPath := "/api/v1/aws-settings/identity/mail-from?brand_id=1"
	w = request(m, "PATCH", mailPath, `{"identity":"example.org","mail_from_domain":"bounce.example.org","mail_from_behavior":"REJECT_MESSAGE"}`, &c, csrf)
	if w.Code != 200 || mailFrom != "bounce.example.org" || behavior != "REJECT_MESSAGE" {
		t.Fatal("MAIL FROM not saved", w.Code, w.Body.String(), mailFrom, behavior)
	}
	w = request(m, "GET", get, "", &c, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.DNSRecords) != 5 || out.MailFromBehavior != "REJECT_MESSAGE" {
		t.Fatal("MAIL FROM records missing", w.Code, w.Body.String())
	}
	mx, spf := out.DNSRecords[3], out.DNSRecords[4]
	if mx.Type != "MX" || mx.Name != mailFrom || mx.Priority != 10 || mx.Value != "feedback-smtp.eu-west-1.amazonses.com" || spf.Value != "v=spf1 include:amazonses.com ~all" {
		t.Fatal(mx, spf)
	}
	before := mutations
	for _, body := range []string{
		`{"identity":"example.org","mail_from_domain":"example.org","mail_from_behavior":"USE_DEFAULT_VALUE"}`,
		`{"identity":"example.org","mail_from_domain":"bounce.example.org.evil.com","mail_from_behavior":"USE_DEFAULT_VALUE"}`,
		`{"identity":"example.org","mail_from_behavior":"USE_DEFAULT_VALUE"}`,
		`{"identity":"example.org","mail_from_domain":"bounce.example.org","mail_from_behavior":"wrong"}`,
		`{"identity":"example.org","mail_from_domain":null,"mail_from_behavior":"USE_DEFAULT_VALUE"}`,
	} {
		if w = request(m, "PATCH", mailPath, body, &c, csrf); w.Code != 400 || mutations != before {
			t.Fatal("invalid MAIL FROM accepted", body, w.Code)
		}
	}
	w = request(m, "PATCH", mailPath, `{"identity":"example.org","mail_from_domain":"","mail_from_behavior":"USE_DEFAULT_VALUE"}`, &c, csrf)
	if w.Code != 200 || mailFrom != "" {
		t.Fatal("MAIL FROM not disabled", w.Code, w.Body.String())
	}
	w = request(m, "GET", get, "", &c, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.DNSRecords) != 3 {
		t.Fatal("disabled MAIL FROM still has DNS records", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"identity":"sender@example.org"}`, `{"identity":"https://example.org"}`, `{"identity":"example.org","unknown":"secret"}`, `{"identity":"example.org"} {}`, `null`} {
		if w = request(m, "POST", path, body, &c, csrf); w.Code != 400 {
			t.Fatal("invalid identity accepted", body, w.Code)
		}
	}
	// Members can read their own brand, but only administrator sessions mutate AWS.
	w = request(m, "POST", "/api/v1/users", `{"email":"reader@example.org","password":"reader-password"}`, &c, csrf)
	var reader struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &reader)
	w = request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", reader.ID), `{"permissions":["read","write"]}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"reader@example.org","password":"reader-password"}`, nil, "")
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &session)
	member := login.Result().Cookies()[0]
	if w = request(m, "GET", get, "", member, ""); w.Code != 200 {
		t.Fatal("member could not read identity", w.Code)
	}
	if w = request(m, "GET", "/api/v1/aws-settings/identity?brand_id=2&identity=example.org", "", member, ""); w.Code != 403 {
		t.Fatal("cross-brand access allowed", w.Code)
	}
	before = mutations
	for _, operation := range []struct{ method, url, body string }{
		{"POST", path, `{"identity":"example.org"}`},
		{"PATCH", mailPath, `{"identity":"example.org","mail_from_domain":"bounce.example.org","mail_from_behavior":"USE_DEFAULT_VALUE"}`},
	} {
		if w = request(m, operation.method, operation.url, operation.body, member, session.CSRF); w.Code != 403 || mutations != before {
			t.Fatal("member mutated AWS", w.Code)
		}
	}
	w = request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"integration","scopes":["read","write"]}`, &c, csrf)
	var key struct{ Secret string }
	json.Unmarshal(w.Body.Bytes(), &key)
	for _, operation := range []struct{ method, url string }{{"GET", get}, {"POST", path}, {"PATCH", mailPath}} {
		r := httptest.NewRequest(operation.method, operation.url, strings.NewReader(`{"identity":"example.org"}`))
		r.Header.Set("Authorization", "Bearer "+key.Secret)
		w = httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 403 || mutations != before {
			t.Fatal("API key elevated", operation.method, w.Code)
		}
	}
	reject = true
	w = request(m, "GET", get, "", &c, "")
	if w.Code != 502 || strings.Contains(w.Body.String(), "secret-value-never-returned") || strings.Contains(w.Body.String(), "brand-domain-key") {
		t.Fatal("upstream credentials leaked", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("identity metadata must not be cached")
	}
	// Three admin reads above plus this failed read leave two permitted refreshes.
	for n := 0; n < 2; n++ {
		if w = request(m, "GET", get, "", &c, ""); w.Code != 502 {
			t.Fatal("refresh denied early", w.Code)
		}
	}
	if w = request(m, "GET", get, "", &c, ""); w.Code != 429 {
		t.Fatal("identity rate limit missing", w.Code)
	}
}

func TestIdentityDNSDoesNotInventDKIMRecords(t *testing.T) {
	for _, identity := range []awsbrand.Identity{
		{Identity: "example.org", IdentityType: "DOMAIN", DKIMOrigin: "EXTERNAL", DKIMHostedZone: "dkim.amazonses.com", DKIMTokens: []string{"selector"}},
		{Identity: "example.org", IdentityType: "DOMAIN", DKIMOrigin: "AWS_SES_US_EAST_1", DKIMHostedZone: "dkim.amazonses.com", DKIMTokens: []string{"one"}},
		{Identity: "example.org", IdentityType: "DOMAIN", DKIMOrigin: "AWS_SES", DKIMTokens: []string{"one"}},
	} {
		if got := identityDNS(identity); len(got) != 0 {
			t.Fatal("fabricated CNAME records", identity, got)
		}
	}
}

func TestSESSetupDomainValidation(t *testing.T) {
	for input, want := range map[string]string{" EXAMPLE.org. ": "example.org", "bücher.example": "xn--bcher-kva.example", "bounce.example.org": "bounce.example.org"} {
		if got, err := sesDomain(input); err != nil || got != want {
			t.Fatal(input, got, err)
		}
	}
	for _, input := range []string{"", "localhost", "a@example.org", "https://example.org", "127.0.0.1", "-bad.example", "bad_.example", "a..example", strings.Repeat("a", 64) + ".example"} {
		if got, err := sesDomain(input); err == nil {
			t.Fatal("invalid domain accepted", input, got)
		}
	}
}
