package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientIPTrustBoundary(t *testing.T) {
	a := New(nil, nil, Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("2001:db8:1::/64")}})
	cases := []struct{ peer, header, want string }{
		{"192.0.2.1:1234", "198.51.100.1", "192.0.2.1"},
		{"10.0.0.2:1234", "198.51.100.1", "198.51.100.1"},
		{"10.0.0.2:1234", "192.0.2.99, 198.51.100.1, 10.0.0.3", "198.51.100.1"},
		{"10.0.0.2:1234", "garbage, 198.51.100.1", "198.51.100.1"},
		{"10.0.0.2:1234", "198.51.100.1:1234", "10.0.0.2"},
		{"10.0.0.2:1234", "198.51.100.1,", "10.0.0.2"},
		{"[2001:db8:1::2]:443", "2001:db8:2:0::1", "2001:db8:2::1"},
		{"[::ffff:192.0.2.1]:443", "198.51.100.1", "192.0.2.1"},
		{"192.0.2.1", "", "192.0.2.1"},
		{"2001:db8::1", "", "2001:db8::1"},
		{"bad:1234", "198.51.100.1", "unknown"},
		{"[fe80::1%en0]:80", "", "unknown"},
		{"10.0.0.2:80", "2001:db8::1%en0", "10.0.0.2"},
	}
	for _, tc := range cases {
		t.Run(tc.peer+"/"+tc.header, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.header)
			r.Header.Set("Forwarded", "for=203.0.113.1")
			r.Header.Set("X-Real-IP", "203.0.113.1")
			if got := a.clientIP(r); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:80"
	r.Header.Add("X-Forwarded-For", "192.0.2.5")
	r.Header.Add("X-Forwarded-For", "10.0.0.3")
	if a.clientIP(r) != "192.0.2.5" {
		t.Fatal("multiple header lines not handled")
	}
	r.Header.Set("X-Forwarded-For", strings.Repeat("10.0.0.3,", 32)+"192.0.2.5")
	if a.clientIP(r) != "10.0.0.2" {
		t.Fatal("overlong chain trusted")
	}
}

func TestRateLimitConcurrencyExpirationAndCapacity(t *testing.T) {
	a := New(nil, nil, Config{})
	now := time.Now()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for j := 0; j < 100; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.allowAt("shared", 10, now) {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed %d", allowed.Load())
	}
	if !a.allowAt("shared", 10, now.Add(time.Minute)) {
		t.Fatal("boundary expiry not reset")
	}
	a = New(nil, nil, Config{})
	for j := 0; j < maxRateBuckets; j++ {
		if !a.allowAt(fmt.Sprint(j), 1, now) {
			t.Fatal("early capacity denial")
		}
	}
	if a.allowAt("extra", 1, now) || len(a.limits) != maxRateBuckets {
		t.Fatal("unbounded buckets")
	}
	if a.allowAt("0", 1, now) {
		t.Fatal("full map evicted live throttle")
	}
	if !a.allowAt("extra", 1, now.Add(time.Minute)) || len(a.limits) != 1 {
		t.Fatal("expired buckets not reclaimed")
	}
	// A denied request must also trigger periodic cleanup.
	a.limits["stale"] = &bucket{until: now}
	a.nextLimitCleanup = now
	a.limits["denied"] = &bucket{n: 1, until: now.Add(time.Minute)}
	if a.allowAt("denied", 1, now) || a.limits["stale"] != nil {
		t.Fatal("denied request skipped cleanup")
	}
}

func TestPublicEndpointLimitsAcrossSourcePorts(t *testing.T) {
	cases := []struct {
		path, method, body string
		max                int
	}{
		{"/api/v1/auth/login", "POST", `{"email":"missing@example.org","password":"wrong"}`, 10},
		{"/api/v1/auth/passkeys/login/begin", "POST", `{}`, 20},
		{"/subscribe/missing", "GET", "", 20},
		{"/confirm/missing", "GET", "", 30},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			a, m, _, _, _ := setup(t)
			a.passkeys, _ = passkeyRP("https://example.org")
			for j := 0; j <= tc.max; j++ {
				r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				r.RemoteAddr = fmt.Sprintf("192.0.2.100:%d", 1000+j)
				r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", j+1))
				w := httptest.NewRecorder()
				m.ServeHTTP(w, r)
				if (w.Code == 429) != (j == tc.max) {
					t.Fatalf("attempt %d status %d: %s", j, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestAuthenticatedFactorLimitsFollowAccount(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/api/v1/auth/totp/enroll", `{"password":"wrong"}`},
		{"/api/v1/auth/totp/verify", `{"code":"wrong"}`},
		{"/api/v1/auth/passkeys/register/begin", `{"password":"wrong"}`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			a, m, db, c, csrf := setup(t)
			a.passkeys, _ = passkeyRP("https://example.org")
			secondary := http.Cookie{Name: "postbox_session", Value: secret()}
			secondaryCSRF := secret()
			if _, err := db.Writer.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(?,1,?,?)`, digest(secondary.Value), time.Now().Add(time.Hour).UnixMilli(), digest(secondaryCSRF)); err != nil {
				t.Fatal(err)
			}
			// Avoid expensive password derivation: an invalid stored hash still follows
			// the same authentication failure path and leaves the real session intact.
			if _, err := db.Writer.Exec("UPDATE users SET password_hash='invalid' WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			for j := 0; j <= 10; j++ {
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
				r.RemoteAddr = fmt.Sprintf("192.0.2.%d:%d", j+1, 1000+j)
				if j%2 == 0 {
					r.AddCookie(&c)
					r.Header.Set("X-CSRF-Token", csrf)
				} else {
					r.AddCookie(&secondary)
					r.Header.Set("X-CSRF-Token", secondaryCSRF)
				}
				w := httptest.NewRecorder()
				m.ServeHTTP(w, r)
				if (w.Code == 429) != (j == 10) {
					t.Fatalf("attempt %d status %d: %s", j, w.Code, w.Body.String())
				}
			}
			// A second authenticated account on the same socket is a separate bucket.
			r := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(context.Background(), identityKey{}, &identity{UserID: 2}))
			if !a.allow(a.rateKey(r, "mfa", true), 10) {
				t.Fatal("other account throttled")
			}
		})
	}
}

func TestTrustedProxyEndpointIdentities(t *testing.T) {
	a, m, _, _, _ := setup(t)
	a.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
	for j := 0; j < 11; j++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"missing@example.org","password":"wrong"}`))
		r.RemoteAddr = fmt.Sprintf("10.0.0.2:%d", 1000+j)
		r.Header.Set("X-Forwarded-For", "203.0.113.99, 192.0.2.100, 10.0.0.3")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if (w.Code == 429) != (j == 10) {
			t.Fatalf("attempt %d status %d", j, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"missing@example.org","password":"wrong"}`))
	r.RemoteAddr = "10.0.0.2:5555"
	r.Header.Set("X-Forwarded-For", "192.0.2.2")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code == 429 {
		t.Fatal("different proxy client shares bucket")
	}
}
