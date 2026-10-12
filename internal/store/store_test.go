package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "postbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

// synchronous is connection-local. Retiring a connection must retain the
// configured WAL mode, including after rollback or cancellation.
func TestReplacementWriterKeepsConfiguredSync(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := db.Immediate(ctx, func(c *sql.Conn) error {
			var mode, fullfsync int
			if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&mode); err != nil {
				return err
			}
			if mode != 1 {
				return fmt.Errorf("reservation connection synchronous=%d, want NORMAL (1)", mode)
			}
			if err := c.QueryRowContext(ctx, "PRAGMA fullfsync").Scan(&fullfsync); err != nil {
				return err
			}
			if fullfsync != 0 {
				return fmt.Errorf("reservation connection fullfsync=%d, want OFF (0)", fullfsync)
			}
			_, err := c.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('durability','normal') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		c, err := db.Writer.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		c.Close()
	}
}
func TestConnectionsAndTransactions(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if db.Writer.Stats().MaxOpenConnections != 1 {
		t.Fatal("writer must have exactly one connection")
	}
	writer, err := db.Writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"journal_mode": "wal", "synchronous": "1", "fullfsync": "0", "foreign_keys": "1", "busy_timeout": "5000", "temp_store": "2", "cache_size": "-65536", "mmap_size": "268435456", "query_only": "0"} {
		var got string
		if err := writer.QueryRowContext(ctx, "PRAGMA "+p).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("writer %s=%s want %s", p, got, want)
		}
	}
	writer.Close()
	var connections []*sql.Conn
	for i := 0; i < 4; i++ {
		c, err := db.Reader.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, c)
	}
	for _, c := range connections {
		for p, want := range map[string]string{"journal_mode": "wal", "synchronous": "1", "fullfsync": "0", "foreign_keys": "1", "busy_timeout": "5000", "temp_store": "2", "cache_size": "-65536", "mmap_size": "268435456", "query_only": "1"} {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+p).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s=%s want %s", p, got, want)
			}
		}
		if _, err := c.ExecContext(ctx, "INSERT INTO settings VALUES('illegal','1')"); err == nil {
			t.Error("reader accepted a write")
		}
		c.Close()
	}
	sentinel := errors.New("rollback")
	if err := db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO settings VALUES('rollback','value')")
		if err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := db.Setting(ctx, "rollback"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rollback left row: %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing panic")
			}
		}()
		_ = db.Immediate(ctx, func(c *sql.Conn) error {
			_, err := c.ExecContext(ctx, "INSERT INTO settings VALUES('panic','value')")
			if err != nil {
				t.Fatal(err)
			}
			panic("test")
		})
	}()
	if err := db.SetSetting(ctx, "after_panic", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Setting(ctx, "panic"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("panic row was committed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	if err := db.Immediate(cancelled, func(c *sql.Conn) error {
		if _, err := c.ExecContext(cancelled, "INSERT INTO settings VALUES('cancelled','value')"); err != nil {
			return err
		}
		cancel()
		return cancelled.Err()
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transaction: %v", err)
	}
	if _, err := db.Setting(ctx, "cancelled"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cancelled row was committed")
	}
	if err := db.SetSetting(ctx, "after_cancel", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO subscribers(brand_id,email,created_at,updated_at) VALUES(999,'a@b.test',1,1)")
		return err
	}); err == nil {
		t.Error("foreign key accepted orphan")
	}
}
func TestMigrationChecksumMismatchPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	// Earlier development databases recorded a different initial migration.
	if _, err = legacy.Exec(`
		CREATE TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT, WITHOUT ROWID;
		INSERT INTO schema_migrations VALUES('001_initial.sql','previous-development-baseline',1);
		CREATE TABLE preserved_data(value TEXT NOT NULL) STRICT;
		INSERT INTO preserved_data VALUES('keep');
	`); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("opened a database from a different migration baseline")
	}
	if !strings.Contains(err.Error(), "migration 001_initial.sql checksum changed") {
		t.Fatalf("unexpected startup error: %v", err)
	}
	var value string
	if err = legacy.QueryRow("SELECT value FROM preserved_data").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("failed startup changed existing data: %q %v", value, err)
	}
	var tableCount int
	if err = legacy.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='brands'").Scan(&tableCount); err != nil || tableCount != 0 {
		t.Fatalf("failed startup applied the new baseline: %d %v", tableCount, err)
	}
}

func TestConsolidatedInitialMigrationChecksumAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consolidated.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("CREATE TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT, WITHOUT ROWID"); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES('001_initial.sql', ?, 1)", originalInitialMigrationChecksum); err != nil {
		t.Fatal(err)
	}
	// This fixture isolates checksum compatibility. Later migrations have their
	// own real-schema tests and must not need fabricated baseline tables here.
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() == "001_initial.sql" || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		source, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", file.Name(), fmt.Sprintf("%x", sha256.Sum256(source))); err != nil {
			t.Fatal(err)
		}
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open database with consolidated migration checksum: %v", err)
	}
	defer db.Close()
	var recorded string
	if err = db.Reader.QueryRow("SELECT checksum FROM schema_migrations WHERE name='001_initial.sql'").Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != originalInitialMigrationChecksum {
		t.Fatalf("migration checksum rewritten: got %q", recorded)
	}
	var brandTableCount int
	if err = db.Reader.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='brands'").Scan(&brandTableCount); err != nil || brandTableCount != 0 {
		t.Fatalf("compatibility check applied migration SQL: count=%d err=%v", brandTableCount, err)
	}
}

func TestMigrationsReopenAndStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetSetting(context.Background(), "persist", "yes"); err != nil {
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
	var migrationCount int
	var migrationName string
	if err := db.Reader.QueryRow("SELECT count(*),min(name) FROM schema_migrations").Scan(&migrationCount, &migrationName); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(entries) || migrationName != "001_initial.sql" {
		t.Fatalf("fresh database migrations: %d %q", migrationCount, migrationName)
	}
	v, err := db.Setting(context.Background(), "persist")
	if err != nil || v != "yes" {
		t.Fatalf("reopen %q %v", v, err)
	}
	rows, err := db.Reader.Query("PRAGMA table_list")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name, kind string
		var cols, wr, strict int
		if err := rows.Scan(&schema, &name, &kind, &cols, &wr, &strict); err != nil {
			t.Fatal(err)
		}
		if schema == "main" && kind == "table" && !strings.HasPrefix(name, "sqlite_") && strict != 1 {
			t.Errorf("table %s is not STRICT", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
func seed(t *testing.T, db *DB) {
	t.Helper()
	err := db.Immediate(context.Background(), func(c *sql.Conn) error {
		_, err := c.ExecContext(context.Background(), `INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Test','Test','from@example.test',1); INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'l','List','single',1);INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'c','sending','Test','<p>Test</p>',1,1);INSERT INTO campaign_stats(campaign_id) VALUES(1);WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<2000) INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) SELECT x,1,printf('person%d@example.test',x),x,x FROM seq;INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) SELECT 1,id,'subscribed',1,1 FROM subscribers;INSERT INTO campaign_recipients(campaign_id,subscriber_id) SELECT 1,id FROM subscribers;UPDATE campaign_recipients SET message_id='msg',status='sent' WHERE campaign_id=1 AND subscriber_id=1;INSERT INTO links(id,campaign_id,url) VALUES(1,1,'https://example.test');INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES('retention','{}','pending',1,1);`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestHotQueryPlans(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	// This expression is deliberately identical to the segment compiler's expression.
	if _, err := db.Writer.Exec(`CREATE INDEX subscribers_country ON subscribers(brand_id, fields ->> '$.country')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE subscribers SET fields=json_object('country',CASE WHEN id=1 THEN 'NL' ELSE 'US' END); ANALYZE;`); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"claimer":          `SELECT subscriber_id FROM campaign_recipients INDEXED BY campaign_recipients_pending WHERE campaign_id=1 AND status='pending' AND subscriber_id>10 ORDER BY subscriber_id LIMIT 200`,
		"result":           `UPDATE campaign_recipients SET status='sent',message_id='new' WHERE campaign_id=1 AND subscriber_id=2 AND status='claimed'`,
		"snapshot":         `INSERT OR IGNORE INTO campaign_recipients(campaign_id,subscriber_id,snapshot_email,snapshot_name,snapshot_fields) SELECT 1,s.id,s.email,s.name,s.fields FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE ls.list_id=1 AND ls.status='subscribed' AND s.brand_id=1 AND s.status='active' AND NOT EXISTS(SELECT 1 FROM suppressions p WHERE p.kind='email' AND p.value=s.email AND (p.brand_id=1 OR p.brand_id IS NULL)) AND NOT EXISTS(SELECT 1 FROM suppressions p WHERE p.kind='domain' AND p.value=s.domain AND (p.brand_id=1 OR p.brand_id IS NULL))`,
		"message":          `SELECT campaign_id,subscriber_id FROM campaign_recipients WHERE message_id='msg'`,
		"email":            `SELECT id FROM subscribers WHERE brand_id=1 AND email='person1@example.test'`,
		"subscribers_page": `SELECT id,email FROM subscribers WHERE brand_id=1 AND id>100 ORDER BY id LIMIT 100`,
		"list_page":        `SELECT s.id,s.email FROM list_subscribers ls JOIN subscribers s ON s.id=ls.subscriber_id WHERE ls.list_id=1 AND ls.subscriber_id>100 ORDER BY ls.subscriber_id LIMIT 100`,
		"segment_count":    `SELECT count(*) FROM subscribers INDEXED BY subscribers_country WHERE brand_id=1 AND (fields ->> '$.country')='NL'`,
		"report":           `SELECT cs.* FROM campaigns c JOIN campaign_stats cs ON cs.campaign_id=c.id WHERE c.brand_id=1 AND c.id>0 ORDER BY c.id LIMIT 100`,
		"due_jobs":         `SELECT id FROM jobs WHERE queue='retention' AND status='pending' AND run_at<=100 ORDER BY run_at,id LIMIT 10`,
		"due_campaigns":    `SELECT id FROM campaigns WHERE status='scheduled' AND scheduled_at<=100 ORDER BY scheduled_at LIMIT 10`,
		"recipient_report": `SELECT subscriber_id FROM campaign_recipients WHERE campaign_id=1 AND status='unknown' AND subscriber_id>0 ORDER BY subscriber_id LIMIT 100`,
		"open_timeline":    `SELECT id FROM opens WHERE subscriber_id=1 AND at>0 ORDER BY at,id LIMIT 100`,
		"click_report":     `SELECT id FROM clicks WHERE campaign_id=1 AND link_id=1 AND id>0 LIMIT 100`,
		"ses_lookup":       `SELECT id FROM ses_events WHERE ses_message_id='msg'`,
		"skip":             `UPDATE campaign_recipients SET status='skipped' WHERE subscriber_id=1 AND status='pending' AND campaign_id IN (SELECT id FROM campaigns WHERE status IN ('preparing','sending','paused'))`,
	}
	large := []string{"subscribers", "list_subscribers", "campaign_recipients", "opens", "clicks", "ses_events", "s", "ls"}
	for name, q := range tests {
		t.Run(name, func(t *testing.T) {
			rows, err := db.Reader.Query("EXPLAIN QUERY PLAN " + q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			usedIndex := false
			usedExpressionIndex := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(detail)
				if strings.Contains(detail, "subscribers_country") && strings.Contains(detail, "<expr>=?") {
					usedExpressionIndex = true
				}
				if strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "USING INDEX") || strings.Contains(detail, "USING COVERING INDEX") {
					usedIndex = true
				}
				for _, table := range large {
					if strings.HasPrefix(detail, "SCAN "+table+" ") || detail == "SCAN "+table {
						t.Errorf("hot query scans large table: %s", detail)
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !usedIndex {
				t.Error("query did not use an index")
			}
			if name == "segment_count" && !usedExpressionIndex {
				t.Error("segment count must constrain the expression index")
			}
		})
	}
}
func TestForeignKeyIndexes(t *testing.T) {
	db := testDB(t)
	rows, err := db.Reader.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		fkRows, err := db.Reader.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for fkRows.Next() {
			var id, seq int
			var target, from, to, onUpdate, onDelete, match string
			if err := fkRows.Scan(&id, &seq, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, from)
		}
		fkRows.Close()
		for _, col := range cols {
			var count int
			err := db.Reader.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=? AND pk=1 AND type='INTEGER'`, table, col).Scan(&count)
			if err != nil {
				t.Fatal(err)
			}
			if count > 0 {
				continue
			}
			err = db.Reader.QueryRow(`SELECT count(*) FROM pragma_index_list(?) AS il JOIN pragma_index_info(il.name) AS ii WHERE ii.seqno=0 AND ii.name=? AND il.partial=0`, table, col).Scan(&count)
			if err != nil {
				t.Fatal(err)
			}
			if count == 0 {
				t.Errorf("foreign key %s.%s lacks leading full index", table, col)
			}
		}
	}
}
