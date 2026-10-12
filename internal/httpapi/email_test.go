package httpapi

import (
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"testing"
	"time"
)

func TestChangeEmail(t *testing.T) {
	_, mux, db, admin, csrf := setup(t)
	w := request(mux, "POST", "/api/v1/users", `{"email":"member@example.org","password":"member-password"}`, &admin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-password"}`, nil, "")
	member := w.Result().Cookies()[0]
	var session LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	other := request(mux, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-password"}`, nil, "").Result().Cookies()[0]
	body := `{"email":"  NEW@Example.org  ","password":"member-password"}`
	if w = request(mux, "PATCH", "/api/v1/auth/email", body, member, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(mux, "PATCH", "/api/v1/auth/email", body, nil, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, invalid := range []string{`{"email":"invalid","password":"member-password"}`, `{"email":"admin@example.org","password":"member-password"}`, `{"email":"new@example.org","password":"wrong"}`, `{"email":"new@example.org","password":"member-password","user_id":1}`} {
		if w = request(mux, "PATCH", "/api/v1/auth/email", invalid, member, session.CSRFToken); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w = request(mux, "PATCH", "/api/v1/auth/email", body, member, session.CSRFToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(mux, "GET", "/api/v1/auth/session", "", member, "")
	var current SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil || current.Email != "new@example.org" || current.Admin {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if w = request(mux, "GET", "/api/v1/auth/session", "", other, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(mux, "POST", "/api/v1/auth/login", `{"email":"member@example.org","password":"member-password"}`, nil, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(mux, "POST", "/api/v1/auth/login", `{"email":"new@example.org","password":"member-password"}`, nil, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var email string
	if err := db.Reader.QueryRow(`SELECT email FROM users WHERE id=1`).Scan(&email); err != nil || email != "admin@example.org" {
		t.Fatal(email, err)
	}
}

func TestChangeEmailRequiresAuthenticator(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	const secret = "JBSWY3DPEHPK3PXP"
	if _, err := db.Writer.Exec(`UPDATE users SET totp_secret=? WHERE id=1`, secret); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"", "invalid"} {
		body, _ := json.Marshal(map[string]string{"email": "new@example.org", "password": "long-test-password", "code": code})
		if w := request(mux, "PATCH", "/api/v1/auth/email", string(body), &cookie, csrf); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": "new@example.org", "password": "long-test-password", "code": code})
	if w := request(mux, "PATCH", "/api/v1/auth/email", string(body), &cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
