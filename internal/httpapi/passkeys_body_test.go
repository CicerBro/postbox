package httpapi

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pausedPasskeyBody struct {
	reader  io.Reader
	first   bool
	waiting chan struct{}
	release chan struct{}
}

func (b *pausedPasskeyBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		return b.reader.Read(p[:1])
	}
	if b.waiting != nil {
		close(b.waiting)
		b.waiting = nil
		<-b.release
	}
	return b.reader.Read(p)
}
func (b *pausedPasskeyBody) Close() error { return nil }

func TestPasskeyFinishReadsBodyBeforeWriter(t *testing.T) {
	for _, kind := range []string{"register", "login"} {
		t.Run(kind, func(t *testing.T) {
			a, mux, db, session, csrf := setup(t)
			a.passkeys, _ = passkeyRP("https://example.org")
			challenge, handle, ceremony := pkOptions(t, pkRequest(mux, "/api/v1/auth/passkeys/register/begin", `{"password":"long-test-password"}`, csrf, &session))
			v := newVirtualPasskey(t, handle)
			body := v.response(t, challenge, "https://example.org", true, 0x45)
			if kind == "login" {
				if w := pkRequest(mux, "/api/v1/auth/passkeys/register/finish", body, csrf, &session, ceremony); w.Code != 200 {
					t.Fatalf("registration: %d %s", w.Code, w.Body.String())
				}
				challenge, _, ceremony = pkOptions(t, pkRequest(mux, "/api/v1/auth/passkeys/login/begin", "", ""))
				v.counter++
				body = v.response(t, challenge, "https://example.org", false, 5)
			}
			waiting, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			r := httptest.NewRequest("POST", "/api/v1/auth/passkeys/"+kind+"/finish", nil)
			r.Body = &pausedPasskeyBody{reader: strings.NewReader(body), waiting: waiting, release: release}
			r.AddCookie(ceremony)
			r.AddCookie(&session)
			r.Header.Set("X-CSRF-Token", csrf)
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); mux.ServeHTTP(w, r) }()
			select {
			case <-waiting:
			case <-time.After(2 * time.Second):
				t.Fatal("finish did not start reading the body")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := db.Immediate(ctx, func(c *sql.Conn) error {
				_, err := c.ExecContext(ctx, `UPDATE users SET email=email WHERE id=1`)
				return err
			})
			if err != nil {
				unblock()
				<-done
				t.Fatalf("slow request held the writer: %v", err)
			}
			var pending int
			if err := db.Reader.QueryRow(`SELECT count(*) FROM passkey_ceremonies`).Scan(&pending); err != nil || pending != 1 {
				unblock()
				<-done
				t.Fatalf("challenge consumed before body completed: count=%d err=%v", pending, err)
			}
			unblock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("finish did not complete")
			}
			if w.Code != 200 {
				t.Fatalf("finish: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPasskeyFinishRejectsOversizedBodyBeforeWriter(t *testing.T) {
	for _, kind := range []string{"register", "login"} {
		t.Run(kind, func(t *testing.T) {
			a, mux, db, session, csrf := setup(t)
			a.passkeys, _ = passkeyRP("https://example.org")
			beginBody := ""
			if kind == "register" {
				beginBody = `{"password":"long-test-password"}`
			}
			_, _, ceremony := pkOptions(t, pkRequest(mux, "/api/v1/auth/passkeys/"+kind+"/begin", beginBody, csrf, &session))
			// Occupy the sole writer connection. An oversized response must finish
			// without needing that connection, including for challenge consumption.
			conn, err := db.Writer.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- pkRequest(mux, "/api/v1/auth/passkeys/"+kind+"/finish", strings.Repeat("x", (1<<20)+1), csrf, &session, ceremony)
			}()
			select {
			case w := <-done:
				if w.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("oversized finish: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(2 * time.Second):
				conn.Close()
				<-done
				t.Fatal("oversized body waited for the writer")
			}
			var pending int
			if err := db.Reader.QueryRow(`SELECT count(*) FROM passkey_ceremonies`).Scan(&pending); err != nil || pending != 1 {
				t.Fatalf("oversized body consumed challenge: count=%d err=%v", pending, err)
			}
		})
	}
}
