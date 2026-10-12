package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedJobRetentionMigrationPreservesLegacyFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT, WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_initial.sql", "002_imported_campaigns_finished.sql", "003_audience_deletion.sql"} {
		source, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(string(source)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(`INSERT INTO schema_migrations VALUES(?,?,0)`, name, fmt.Sprintf("%x", sha256.Sum256(source))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO jobs(id,queue,payload,status,run_at,created_at) VALUES (1,'test','{}','failed',0,0),(2,'test','{}','pending',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second).UnixMilli()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var failedAt int64
	var pendingAt sql.NullInt64
	if err := db.Reader.QueryRow(`SELECT failed_at FROM jobs WHERE id=1`).Scan(&failedAt); err != nil || failedAt < before || failedAt > time.Now().UnixMilli() {
		t.Fatalf("legacy failure timestamp=%d error=%v", failedAt, err)
	}
	if err := db.Reader.QueryRow(`SELECT failed_at FROM jobs WHERE id=2`).Scan(&pendingAt); err != nil || pendingAt.Valid {
		t.Fatalf("pending failure timestamp=%+v error=%v", pendingAt, err)
	}
	var id, parent, unused int
	var plan string
	if err := db.Reader.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM jobs WHERE status='failed' AND failed_at<? ORDER BY failed_at,id LIMIT ?`, time.Now().UnixMilli(), 1000).Scan(&id, &parent, &unused, &plan); err != nil || !strings.Contains(plan, "jobs_failed_retention") {
		t.Fatalf("retention query plan=%s error=%v", plan, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var reopened int64
	if err := db.Reader.QueryRow(`SELECT failed_at FROM jobs WHERE id=1`).Scan(&reopened); err != nil || reopened != failedAt {
		t.Fatalf("upgrade timestamp changed on reopen: %d to %d, error=%v", failedAt, reopened, err)
	}
}
