package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"postbox/internal/awsbrand"
	"postbox/internal/subscribers"
	"strings"
	"testing"
)

func TestAWSConnectionCheckDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, path, code, operation, reason string
	}{
		{"invalid key", "/", "InvalidClientTokenId", "sts:GetCallerIdentity", "AWS rejected the access key"},
		{"bad signature", "/", "SignatureDoesNotMatch", "sts:GetCallerIdentity", "secret matches the access key"},
		{"account permissions", "/v2/email/account", "AccessDeniedException", "ses:GetAccount", "AWS denied permission"},
		{"identity permissions", "/v2/email/list-identities", "AccessDeniedException", "ses:ListEmailIdentities", "AWS denied permission"},
		{"unknown code", "/v2/email/account", "raw-secret-value", "ses:GetAccount", "AWS could not complete the request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					if tc.path == "/" {
						w.Header().Set("Content-Type", "text/xml")
						w.WriteHeader(403)
						fmt.Fprintf(w, `<ErrorResponse><Error><Type>Sender</Type><Code>%s</Code><Message>raw-secret-value AKIATEST</Message></Error><RequestId>test</RequestId></ErrorResponse>`, tc.code)
					} else {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-Amzn-Errortype", tc.code)
						w.WriteHeader(403)
						fmt.Fprintf(w, `{"__type":%q,"message":"raw-secret-value AKIATEST"}`, tc.code)
					}
					return
				}
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/xml")
					fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
				case "/v2/email/account":
					fmt.Fprint(w, `{"SendQuota":{"MaxSendRate":14,"Max24HourSend":1000,"SentLast24Hours":10}}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer upstream.Close()
			a, m, db, c, csrf := setup(t)
			p, err := awsbrand.New(db, []byte(strings.Repeat("x", 32)), "eu-west-1", upstream.URL, 1)
			if err != nil {
				t.Fatal(err)
			}
			a.cfg.AWSProvider = p
			if _, err = subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "one", FromEmail: "from@example.org"}); err != nil {
				t.Fatal(err)
			}
			if err = p.Save(context.Background(), 1, awsbrand.Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "AKIATEST", SecretAccessKey: "raw-secret-value"}); err != nil {
				t.Fatal(err)
			}
			w := request(m, "POST", "/api/v1/aws-settings/check?brand_id=1", "", &c, csrf)
			body := w.Body.String()
			if w.Code != 502 || !strings.Contains(body, tc.operation) || !strings.Contains(body, tc.reason) || !strings.Contains(body, "eu-west-1") {
				t.Fatalf("missing diagnostics: %d %s", w.Code, body)
			}
			if strings.Contains(body, "raw-secret-value") || strings.Contains(body, "AKIATEST") {
				t.Fatalf("AWS error leaked credentials: %s", body)
			}
		})
	}
}

func TestAWSSettingsSecretsAndAuthorization(t *testing.T) {
	a, m, db, c, csrf := setup(t)
	p, err := awsbrand.New(db, []byte(strings.Repeat("x", 32)), "eu-west-1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.AWSProvider = p
	s := subscribers.New(db)
	for _, name := range []string{"one", "two"} {
		if _, err = s.CreateBrand(context.Background(), subscribers.Brand{Name: name, FromEmail: "from@example.org"}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/aws-settings?brand_id=1"
	body := `{"mode":"static","region":"eu-west-1","access_key_id":"AKIATESTSECRETID","secret_access_key":"secret-value-never-returned","session_token":"token-value-never-returned","sns_topic_arn":"arn:aws:sns:eu-west-1:123456789012:brand-one"}`
	if w := request(m, "PATCH", path, body, &c, ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	w := request(m, "PATCH", path, body, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"AKIATESTSECRETID", "secret-value-never-returned", "token-value-never-returned"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential leaked")
		}
	}
	var saved awsbrand.Settings
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !saved.HasCredentials || !saved.HasSessionToken || saved.TopicARN != "arn:aws:sns:eu-west-1:123456789012:brand-one" {
		t.Fatal(saved, err)
	}
	w = request(m, "PATCH", path, `{"sns_topic_arn":"arn:aws:sns:us-east-1:123456789012:wrong"}`, &c, csrf)
	if w.Code != 400 {
		t.Fatal("cross-region SNS topic accepted", w.Code, w.Body.String())
	}
	w = request(m, "PATCH", path, `{"region":"eu-central-1","secret_access_key":"","session_token":"","sns_topic_arn":""}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &saved)
	if !saved.HasCredentials || !saved.HasSessionToken || saved.Region != "eu-central-1" {
		t.Fatal(saved)
	}
	w = request(m, "PATCH", path, `{"clear_session_token":true}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.HasSessionToken {
		t.Fatal("token clear failed")
	}
	w = request(m, "PATCH", path, `{"raw-secret-value":"bad"}`, &c, csrf)
	if w.Code != 400 || strings.Contains(w.Body.String(), "raw-secret-value") {
		t.Fatal("decoding error leaked input", w.Body.String())
	}
	w = request(m, "POST", "/api/v1/api-keys?brand_id=1", `{"name":"integration","scopes":["read","write"]}`, &c, csrf)
	var key struct{ Secret string }
	json.Unmarshal(w.Body.Bytes(), &key)
	bearer := func(method, url, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key.Secret)
		out := httptest.NewRecorder()
		m.ServeHTTP(out, r)
		return out
	}
	if w = bearer("GET", path, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = bearer("GET", "/api/v1/aws-settings?brand_id=2", ""); w.Code != 403 {
		t.Fatal("cross-brand metadata access", w.Code)
	}
	for _, method := range []string{"PATCH", "DELETE", "POST"} {
		url := path
		if method == "POST" {
			url = "/api/v1/aws-settings/check?brand_id=1"
		}
		if w = bearer(method, url, body); w.Code != 403 {
			t.Fatal("API key elevated", method, w.Code)
		}
	}
	if w = bearer("POST", "/api/v1/aws-settings/setup-events?brand_id=1", ""); w.Code != 403 {
		t.Fatal("API key configured SES event destination", w.Code)
	}
	w = request(m, "POST", "/api/v1/users", `{"email":"member@example.org","password":"member-password"}`, &c, csrf)
	var user struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &user)
	w = request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", user.ID), `{"permissions":["read","write","send"]}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-password"}`, nil, "")
	member := login.Result().Cookies()[0]
	var sess struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &sess)
	if w = request(m, "GET", path, "", member, ""); w.Code != 200 {
		t.Fatal("member read failed", w.Code, w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/aws-settings?brand_id=2", "", member, ""); w.Code != 403 {
		t.Fatal("member cross-brand read", w.Code)
	}
	if w = request(m, "PATCH", path, body, member, sess.CSRF); w.Code != 403 {
		t.Fatal("member credential write", w.Code)
	}
	if w = request(m, "POST", "/api/v1/aws-settings/setup-events?brand_id=1", "", member, sess.CSRF); w.Code != 403 {
		t.Fatal("member configured SES event destination", w.Code)
	}

	w = request(m, "DELETE", path, "", &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Mode != "static" || saved.Configured || saved.HasCredentials {
		t.Fatal("brand keys not removed", saved)
	}
	if w = request(m, "GET", "/api/v1/aws-settings?brand_id=999", "", &c, ""); w.Code != 404 {
		t.Fatal("missing brand", w.Code)
	}
}

func TestAWSConnectionCheckReadOnlyAndRedaction(t *testing.T) {
	calls := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account><Arn>arn:aws:iam::123456789012:user/test</Arn><UserId>test</UserId></GetCallerIdentityResult></GetCallerIdentityResponse>`)
		case "/v2/email/account":
			fmt.Fprint(w, `{"SendingEnabled":true,"ProductionAccessEnabled":true,"SendQuota":{"MaxSendRate":14,"Max24HourSend":1000,"SentLast24Hours":10}}`)
		case "/v2/email/list-identities":
			fmt.Fprint(w, `{"EmailIdentities":[{"IdentityName":"example.org","IdentityType":"DOMAIN","SendingEnabled":true,"VerificationStatus":"SUCCESS"}]}`)
		default:
			t.Errorf("unexpected AWS request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "raw-secret-value", 500)
		}
	}))
	defer upstream.Close()
	a, m, db, c, csrf := setup(t)
	p, e := awsbrand.New(db, []byte(strings.Repeat("x", 32)), "eu-west-1", upstream.URL, 1)
	if e != nil {
		t.Fatal(e)
	}
	a.cfg.AWSProvider = p
	if _, e = subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "one", FromEmail: "from@example.org"}); e != nil {
		t.Fatal(e)
	}
	if e = p.Save(context.Background(), 1, awsbrand.Update{Mode: "static", Region: "eu-west-1", AccessKeyID: "AKIATEST", SecretAccessKey: "raw-secret-value"}); e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/aws-settings/check?brand_id=1"
	w := request(m, "POST", path, "", &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		awsbrand.Status
		CheckedAt int64 `json:"checked_at"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.AccountID != "123456789012" || out.Quota.MaxSendRate != 14 || out.CheckedAt == 0 || len(out.Identities) != 1 {
		t.Fatal(out)
	}
	if calls["/"] != 1 || calls["/v2/email/account"] != 1 || calls["/v2/email/list-identities"] != 1 {
		t.Fatal(calls)
	}
	w = request(m, "POST", "/api/v1/users", `{"email":"reader@example.org","password":"reader-password"}`, &c, csrf)
	var reader struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &reader)
	w = request(m, "PUT", fmt.Sprintf("/api/v1/users/%d/brands/1", reader.ID), `{"permissions":["read"]}`, &c, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"reader@example.org","password":"reader-password"}`, nil, "")
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &session)
	if w = request(m, "POST", path, "", login.Result().Cookies()[0], session.CSRF); w.Code != 200 {
		t.Fatal("reader quota check denied", w.Code, w.Body.String())
	}
	for n := 0; n < 5; n++ {
		if w = request(m, "POST", path, "", &c, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w = request(m, "POST", path, "", &c, csrf); w.Code != 429 {
		t.Fatal("rate limit missing", w.Code)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "raw-secret-value AKIATEST", 403) }))
	defer bad.Close()
	p, e = awsbrand.New(db, []byte(strings.Repeat("x", 32)), "eu-west-1", bad.URL, 1)
	if e != nil {
		t.Fatal(e)
	}
	a.cfg.AWSProvider = p
	a.limits = map[string]*bucket{}
	w = request(m, "POST", path, "", &c, csrf)
	if w.Code != 502 || strings.Contains(w.Body.String(), "raw-secret-value") || strings.Contains(w.Body.String(), "AKIATEST") {
		t.Fatal("AWS error leak", w.Code, w.Body.String())
	}
}
