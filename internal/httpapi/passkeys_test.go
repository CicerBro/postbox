package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/pquerna/otp/totp"
	"time"
)

// A minimal ES256 virtual authenticator generates real attestation/assertion bytes.
// Tests exercise the verifier cryptographically instead of replacing WebAuthn with a mock.
type virtualPasskey struct {
	key        *ecdsa.PrivateKey
	id, handle []byte
	counter    uint32
	rp         string
}

func newVirtualPasskey(t *testing.T, handle []byte) *virtualPasskey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &virtualPasskey{key: key, id: []byte("test-credential-random-id-12345678"), handle: handle}
}
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func jsonText(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func (v *virtualPasskey) response(t *testing.T, challenge, origin string, registration bool, flags byte) string {
	t.Helper()
	typ := "webauthn.get"
	if registration {
		typ = "webauthn.create"
	}
	client := []byte(jsonText(t, map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false}))
	rpName := v.rp
	if rpName == "" {
		rpName = "example.org"
	}
	rp := sha256.Sum256([]byte(rpName))
	auth := append([]byte{}, rp[:]...)
	auth = append(auth, flags)
	count := make([]byte, 4)
	binary.BigEndian.PutUint32(count, v.counter)
	auth = append(auth, count...)
	response := map[string]any{"clientDataJSON": b64(client)}
	if registration {
		auth = append(auth, make([]byte, 16)...)
		l := make([]byte, 2)
		binary.BigEndian.PutUint16(l, uint16(len(v.id)))
		auth = append(auth, l...)
		auth = append(auth, v.id...)
		cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: v.key.X.FillBytes(make([]byte, 32)), -3: v.key.Y.FillBytes(make([]byte, 32))})
		if err != nil {
			t.Fatal(err)
		}
		auth = append(auth, cose...)
		att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
		if err != nil {
			t.Fatal(err)
		}
		response["attestationObject"] = b64(att)
		response["transports"] = []string{"internal"}
	} else {
		hash := sha256.Sum256(client)
		signed := append(append([]byte{}, auth...), hash[:]...)
		digest := sha256.Sum256(signed)
		signature, err := ecdsa.SignASN1(rand.Reader, v.key, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		response["authenticatorData"] = b64(auth)
		response["signature"] = b64(signature)
		response["userHandle"] = b64(v.handle)
	}
	return jsonText(t, map[string]any{"id": b64(v.id), "rawId": b64(v.id), "type": "public-key", "response": response, "clientExtensionResults": map[string]any{}})
}
func pkRequest(m http.Handler, path, body, csrf string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func pkOptions(t *testing.T, w *httptest.ResponseRecorder) (string, []byte, *http.Cookie) {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var parsed struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	handle, _ := base64.RawURLEncoding.DecodeString(parsed.PublicKey.User.ID)
	if parsed.PublicKey.Challenge == "" {
		t.Fatal("missing challenge")
	}
	return parsed.PublicKey.Challenge, handle, w.Result().Cookies()[0]
}
func TestPasskeyCryptographicRegistrationAndLogin(t *testing.T) {
	a, m, db, session, csrf := setup(t)
	var err error
	a.passkeys, err = passkeyRP("https://example.org")
	if err != nil {
		t.Fatal(err)
	}
	begin := func() *httptest.ResponseRecorder {
		return pkRequest(m, "/api/v1/auth/passkeys/register/begin", `{"password":"long-test-password","name":"Laptop"}`, csrf, &session)
	}
	challenge, handle, ceremony := pkOptions(t, begin())
	v := newVirtualPasskey(t, handle)
	w := pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, "https://example.org", true, 0x45), csrf, &session, ceremony)
	if w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	// Registration cannot be replayed.
	if w = pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, "https://example.org", true, 0x45), csrf, &session, ceremony); w.Code != 401 {
		t.Fatal("registration replay", w.Code)
	}
	for _, scenario := range []struct {
		name, origin                          string
		flags                                 byte
		badChallenge, badSignature, badHandle bool
	}{
		{name: "wrong origin", origin: "https://evil.example", flags: 5},
		{name: "no UV", origin: "https://example.org", flags: 1},
		{name: "wrong challenge", origin: "https://example.org", flags: 5, badChallenge: true},
		{name: "bad signature", origin: "https://example.org", flags: 5, badSignature: true},
		{name: "wrong user", origin: "https://example.org", flags: 5, badHandle: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			a.mu.Lock()
			a.limits = map[string]*bucket{}
			a.mu.Unlock()
			challenge, _, ceremony := pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
			if scenario.badChallenge {
				challenge = b64(make([]byte, 32))
			}
			v.counter++
			body := v.response(t, challenge, scenario.origin, false, scenario.flags)
			if scenario.badSignature || scenario.badHandle {
				var data map[string]any
				json.Unmarshal([]byte(body), &data)
				field := "signature"
				if scenario.badHandle {
					field = "userHandle"
				}
				data["response"].(map[string]any)[field] = b64([]byte("wrong"))
				body = jsonText(t, data)
			}
			w := pkRequest(m, "/api/v1/auth/passkeys/login/finish", body, "", ceremony)
			if w.Code != 401 {
				t.Fatalf("expected rejection: %d %s", w.Code, w.Body.String())
			}
			// Every failed assertion consumes its browser challenge.
			if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", v.response(t, challenge, "https://example.org", false, 5), "", ceremony); w.Code != 401 {
				t.Fatal("failure challenge reused")
			}
		})
	}
	a.mu.Lock()
	a.limits = map[string]*bucket{}
	a.mu.Unlock()
	challenge, _, ceremony = pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
	v.counter++
	body := v.response(t, challenge, "https://example.org", false, 5)
	if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", body, ""); w.Code != 401 {
		t.Fatal("unbound assertion accepted")
	}
	w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", body, "", ceremony)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var raw string
	if err = db.Reader.QueryRow(`SELECT credential FROM passkeys`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Authenticator struct {
			SignCount uint32 `json:"signCount"`
		} `json:"authenticator"`
	}
	json.Unmarshal([]byte(raw), &stored)
	if stored.Authenticator.SignCount != v.counter {
		t.Fatalf("counter not persisted: %s", raw)
	}
	if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", body, "", ceremony); w.Code != 401 {
		t.Fatal("login replay accepted")
	}
	// A signed counter regression is detected and cannot create another session.
	challenge, _, ceremony = pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
	v.counter = 1
	if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", v.response(t, challenge, "https://example.org", false, 5), "", ceremony); w.Code != 401 {
		t.Fatal("counter regression accepted", w.Code)
	}
	challenge, _, ceremony = pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
	v.rp = "evil.example"
	v.counter = 100
	if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", v.response(t, challenge, "https://example.org", false, 5), "", ceremony); w.Code != 401 {
		t.Fatal("wrong RP accepted", w.Code)
	}
	v.rp = ""
	challenge, _, ceremony = pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
	body = v.response(t, challenge, "https://example.org", false, 5)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- pkRequest(m, "/api/v1/auth/passkeys/login/finish", body, "", ceremony).Code
		}()
	}
	wg.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		} else if status != 401 {
			t.Fatal("unexpected concurrent finish status", status)
		}
	}
	if successes != 1 {
		t.Fatal("single-use race", successes)
	}

	challenge, _, ceremony = pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/login/begin", "", ""))
	db.Writer.Exec(`UPDATE passkey_ceremonies SET expires_at=0`)
	if w = pkRequest(m, "/api/v1/auth/passkeys/login/finish", v.response(t, challenge, "https://example.org", false, 5), "", ceremony); w.Code != 401 {
		t.Fatal("expired challenge accepted")
	}
}
func TestPasskeyOriginAndFactorProtection(t *testing.T) {
	for _, base := range []string{"", "http://example.org", "https://example.org/path", "https://user@example.org", "https://example.org?x=1", "https://127.0.0.2", "https://example.org."} {
		if _, err := passkeyRP(base); err == nil {
			t.Fatalf("insecure RP accepted: %s", base)
		}
	}
	for _, base := range []string{"https://example.org", "http://localhost:8080"} {
		if _, err := passkeyRP(base); err != nil {
			t.Fatalf("valid RP rejected: %s %v", base, err)
		}
	}
	a, m, db, session, csrf := setup(t)
	a.passkeys, _ = passkeyRP("https://example.org")

	malicious := httptest.NewRequest("POST", "/api/v1/auth/passkeys/login/begin", nil)
	malicious.Header.Set("Origin", "https://evil.example.org")
	blocked := httptest.NewRecorder()
	m.ServeHTTP(blocked, malicious)
	if blocked.Code != 403 {
		t.Fatal("sibling origin request accepted", blocked.Code)
	}
	otp := "JBSWY3DPEHPK3PXP"
	db.Writer.Exec(`UPDATE users SET totp_secret=? WHERE id=1`, otp)
	if w := pkRequest(m, "/api/v1/auth/passkeys/register/begin", `{"password":"long-test-password"}`, csrf, &session); w.Code != 401 {
		t.Fatal("TOTP bypass", w.Code)
	}
	if w := pkRequest(m, "/api/v1/auth/passkeys/register/begin", `{"password":"long-test-password"}`, "", &session); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	code, _ := totp.GenerateCode(otp, time.Now())
	challenge, handle, ceremony := pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/register/begin", jsonText(t, map[string]any{"password": "long-test-password", "code": code}), csrf, &session))
	v := newVirtualPasskey(t, handle)
	db.Writer.Exec(`UPDATE users SET totp_secret=NULL WHERE id=1`)
	if w := pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, "https://example.org", true, 0x45), csrf, &session, ceremony); w.Code != 401 {
		t.Fatal("changed factors accepted", w.Code)
	}
}

func TestPasskeyRegistrationBindingAndDeletion(t *testing.T) {
	a, m, db, session, csrf := setup(t)
	a.passkeys, _ = passkeyRP("https://example.org")
	begin := func() (string, []byte, *http.Cookie) {
		t.Helper()
		a.mu.Lock()
		a.limits = map[string]*bucket{}
		a.mu.Unlock()
		return pkOptions(t, pkRequest(m, "/api/v1/auth/passkeys/register/begin", `{"password":"long-test-password"}`, csrf, &session))
	}
	for _, scenario := range []struct {
		name, origin string
		flags        byte
		badChallenge bool
	}{
		{name: "registration origin", origin: "https://evil.example", flags: 0x45},
		{name: "registration verification", origin: "https://example.org", flags: 0x41},
		{name: "registration challenge", origin: "https://example.org", flags: 0x45, badChallenge: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			challenge, handle, cookie := begin()
			if scenario.badChallenge {
				challenge = b64(make([]byte, 32))
			}
			v := newVirtualPasskey(t, handle)
			w := pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, scenario.origin, true, scenario.flags), csrf, &session, cookie)
			if w.Code != 401 {
				t.Fatal("registration bypass", w.Code, w.Body.String())
			}
		})
	}
	challenge, handle, cookie := begin()
	v := newVirtualPasskey(t, handle)
	// Even another authenticated session for the same account cannot finish enrollment.
	login := request(m, "POST", "/api/v1/auth/login", `{"email":"admin@example.org","password":"long-test-password"}`, nil, "")
	var logged map[string]any
	json.Unmarshal(login.Body.Bytes(), &logged)
	second := login.Result().Cookies()[0]
	w := pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, "https://example.org", true, 0x45), logged["csrf_token"].(string), second, cookie)
	if w.Code != 401 {
		t.Fatal("session binding bypass", w.Code)
	}
	challenge, handle, cookie = begin()
	v = newVirtualPasskey(t, handle)
	w = pkRequest(m, "/api/v1/auth/passkeys/register/finish", v.response(t, challenge, "https://example.org", true, 0x45), csrf, &session, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	list := request(m, "GET", "/api/v1/auth/passkeys", "", &session, "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"name":"Passkey"`) || strings.Contains(list.Body.String(), "publicKey") {
		t.Fatal("credential list", list.Code, list.Body.String())
	}
	// A row owned by another user cannot be removed, even with the acting user's correct password.
	if err := CreateAdmin(t.Context(), db, "other@example.org", "other-long-password", ""); err != nil {
		t.Fatal(err)
	}
	res, err := db.Writer.Exec(`INSERT INTO passkeys(user_id,rp_id,credential_id,credential,name,created_at) VALUES(2,'example.org',?,'{}','Other',0)`, []byte("other"))
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := res.LastInsertId()
	if w = request(m, "DELETE", "/api/v1/auth/passkeys/"+jsonText(t, otherID), `{"password":"long-test-password"}`, &session, csrf); w.Code != 404 {
		t.Fatal("wrong-user deletion", w.Code, w.Body.String())
	}
	otp := "JBSWY3DPEHPK3PXP"
	db.Writer.Exec(`UPDATE users SET totp_secret=? WHERE id=1`, otp)
	if w = request(m, "DELETE", "/api/v1/auth/passkeys/1", `{"password":"long-test-password"}`, &session, csrf); w.Code != 401 {
		t.Fatal("delete factor bypass", w.Code)
	}
	code, _ := totp.GenerateCode(otp, time.Now())
	w = request(m, "DELETE", "/api/v1/auth/passkeys/1", jsonText(t, map[string]any{"password": "long-test-password", "code": code}), &session, csrf)
	if w.Code != 200 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	if w = request(m, "GET", "/api/v1/auth/session", "", &session, ""); w.Code != 401 {
		t.Fatal("removed credential failed to revoke sessions", w.Code)
	}
}
