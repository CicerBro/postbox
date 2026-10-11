package store

import (
	"path/filepath"
	"testing"
)

func TestSessionsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO users(id,email,password_hash,role,created_at) VALUES(1,'user@example.org','hash','admin',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(X'01',1,9999999999999,X'02')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("restart revoked session: %d %v", count, err)
	}
	if err = db.Reader.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("restart removed users: %d %v", count, err)
	}
	var tokenHash, csrfHash []byte
	if err = db.Reader.QueryRow("SELECT token_hash,csrf_hash FROM sessions WHERE user_id=1").Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if string(tokenHash) != "\x01" || string(csrfHash) != "\x02" {
		t.Fatalf("restart changed session hashes: %x %x", tokenHash, csrfHash)
	}
}
