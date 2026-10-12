package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestTimezoneMigrationPreservesUsersAndSavedPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(`CREATE TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT, WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "006_user_timezone.sql" {
			break
		}
		source, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(string(source)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(`INSERT INTO schema_migrations VALUES(?,?,0)`, entry.Name(), fmt.Sprintf("%x", sha256.Sum256(source))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO users(email,password_hash,role,created_at) VALUES('user@example.org','existing-hash','brand_user',123)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var zone, hash string
	if err := db.Reader.QueryRow(`SELECT timezone,password_hash FROM users WHERE email='user@example.org'`).Scan(&zone, &hash); err != nil || zone != "" || hash != "existing-hash" {
		t.Fatalf("upgrade changed user: zone=%q hash=%q error=%v", zone, hash, err)
	}
	if _, err := db.Writer.Exec(`UPDATE users SET timezone='Asia/Kathmandu'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reader.QueryRow(`SELECT timezone FROM users WHERE email='user@example.org'`).Scan(&zone); err != nil || zone != "Asia/Kathmandu" {
		t.Fatalf("reopen lost timezone: %q %v", zone, err)
	}
}
