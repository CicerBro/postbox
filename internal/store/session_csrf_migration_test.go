package store

import (
	"path/filepath"
	"testing"
)

func TestStableCSRFMigrateLegacySessionsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO users(id,email,password_hash,role,created_at) VALUES(1,'legacy@example.org','hash','admin',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(X'01',1,9999999999999,X'02')`); err != nil {
		t.Fatal(err)
	}
	// Model a pre-upgrade database carrying an unrecoverable random CSRF hash.
	if _, err = db.Writer.Exec(`DELETE FROM schema_migrations WHERE name='015_stable_session_csrf.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy sessions remain: %d %v", count, err)
	}
	if err = db.Reader.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration removed users: %d %v", count, err)
	}
	if _, err = db.Writer.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,csrf_hash) VALUES(X'03',1,9999999999999,X'04')`); err != nil {
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
	if err = db.Reader.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("ordinary restart revoked new sessions: %d %v", count, err)
	}
}
