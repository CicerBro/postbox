package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestLoginRejectsCredentialsChangedBeforeSessionInsertion(t *testing.T) {
	newHash, err := HashPassword("replacement-test-password")
	if err != nil {
		t.Fatal(err)
	}
	const originalOTP = "JBSWY3DPEHPK3PXP"
	for _, tc := range []struct {
		name        string
		initialOTP  any
		mutation    string
		value       any
		wantSuccess bool
	}{
		{name: "password reset", mutation: "UPDATE users SET password_hash=? WHERE id=1", value: newHash},
		{name: "MFA enabled", mutation: "UPDATE users SET totp_secret=? WHERE id=1", value: originalOTP},
		{name: "MFA replaced", initialOTP: originalOTP, mutation: "UPDATE users SET totp_secret=? WHERE id=1", value: "KRUGS4ZANFZSAYJA"},
		{name: "MFA disabled", initialOTP: originalOTP, mutation: "UPDATE users SET totp_secret=? WHERE id=1", value: nil},
		{name: "email changed", mutation: "UPDATE users SET email=? WHERE id=1", value: "new-admin@example.org"},
		{name: "account deleted", mutation: "DELETE FROM users WHERE id=1"},
		{name: "unchanged credentials", wantSuccess: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mux, db, _, _ := setup(t)
			if tc.initialOTP != nil {
				if _, err := db.Writer.Exec("UPDATE users SET totp_secret=? WHERE id=1", tc.initialOTP); err != nil {
					t.Fatal(err)
				}
			}
			in := map[string]string{"email": "admin@example.org", "password": "long-test-password"}
			if tc.initialOTP != nil {
				code, err := totp.GenerateCode(originalOTP, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				in["totp"] = code
			}
			body, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// Holding the only writer connection lets password/MFA validation finish
			// while keeping session insertion queued until the account change commits.
			writer, err := db.Writer.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			waitCount := db.Writer.Stats().WaitCount
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- request(mux, "POST", "/api/v1/auth/login", string(body), nil, "")
			}()
			for db.Writer.Stats().WaitCount == waitCount {
				select {
				case response := <-done:
					t.Fatalf("login returned before reaching the writer: %d %s", response.Code, response.Body.String())
				case <-ctx.Done():
					t.Fatal("login did not reach session insertion:", ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			defer writer.ExecContext(context.Background(), "ROLLBACK")
			if tc.mutation != "" {
				args := []any{tc.value}
				if tc.name == "account deleted" {
					args = nil
				}
				if _, err := writer.ExecContext(ctx, tc.mutation, args...); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := writer.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ExecContext(ctx, "COMMIT"); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
			case <-ctx.Done():
				t.Fatal("login did not finish:", ctx.Err())
			}
			var sessions int
			if err := db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if tc.wantSuccess {
				if response.Code != http.StatusOK || sessions != 1 || len(response.Result().Cookies()) != 1 {
					t.Fatalf("unchanged credentials: status=%d sessions=%d body=%s", response.Code, sessions, response.Body.String())
				}
			} else if response.Code != http.StatusUnauthorized || sessions != 0 || len(response.Result().Cookies()) != 0 {
				t.Fatalf("obsolete credentials created a session: status=%d sessions=%d body=%s", response.Code, sessions, response.Body.String())
			}
		})
	}
}
