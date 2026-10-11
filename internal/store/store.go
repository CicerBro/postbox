// Package store owns SQLite connections and the versioned embedded schema.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// consolidatedInitialMigrationChecksum identifies the applied squashed schema
// before the harmless fresh-install header was added.
const consolidatedInitialMigrationChecksum = "e89e92d4f0f2f762ca560dbfdac7e66fe997dab469e5bfab7ae3f23a953006e3"

type DB struct {
	Writer *sql.DB
	Reader *sql.DB
}

// Open creates a single writer and an independent, read-only connection pool.
// URI pragmas apply on creation of every physical connection, including replacements.
// WAL NORMAL preserves committed reservations across application-process crashes.
// It does not guarantee a committed reservation survives a host crash or power
// loss. That tradeoff is deliberate for campaign throughput; see the performance
// profile. fullfsync is disabled because it is Apple-specific and costly there.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("SQLite path is empty")
	}
	if path == ":memory:" {
		return nil, errors.New("use a temporary SQLite file: WAL requires a file database")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	for _, p := range []string{"journal_mode(WAL)", "synchronous(NORMAL)", "fullfsync(OFF)", "foreign_keys(ON)", "busy_timeout(5000)", "temp_store(MEMORY)", "cache_size(-65536)", "mmap_size(268435456)"} {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	writer, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	db := &DB{Writer: writer}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = writer.PingContext(ctx); err != nil {
		writer.Close()
		return nil, fmt.Errorf("open SQLite writer: %w", err)
	}
	if err = db.migrate(ctx); err != nil {
		writer.Close()
		return nil, err
	}
	q.Add("_pragma", "query_only(ON)")
	u.RawQuery = q.Encode()
	reader, err := sql.Open("sqlite", u.String())
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(8)
	reader.SetMaxIdleConns(8)
	db.Reader = reader
	if err = reader.PingContext(ctx); err != nil {
		reader.Close()
		writer.Close()
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	return db, nil
}

// Immediate serializes writes, acquires the lock before reading state, and rolls
// back even when the callback panics or the caller's context has been cancelled.
func (db *DB) Immediate(ctx context.Context, fn func(*sql.Conn) error) (err error) {
	conn, err := db.Writer.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, rollbackErr := conn.ExecContext(cleanup, "ROLLBACK")
			if rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback SQLite transaction: %w", rollbackErr))
			}
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (db *DB) migrate(ctx context.Context) error {
	return db.Immediate(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT, WITHOUT ROWID"); err != nil {
			return err
		}
		files, err := migrations.ReadDir("migrations")
		if err != nil {
			return err
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".sql") {
				continue
			}
			source, err := migrations.ReadFile("migrations/" + f.Name())
			if err != nil {
				return err
			}
			checksum := fmt.Sprintf("%x", sha256.Sum256(source))
			var existing string
			err = conn.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE name=?", f.Name()).Scan(&existing)
			if err == nil {
				if existing != checksum {
					if f.Name() != "001_initial.sql" || existing != consolidatedInitialMigrationChecksum {
						return fmt.Errorf("migration %s checksum changed", f.Name())
					}
					// The applied schema is the squashed baseline. Keep its recorded checksum.
					continue
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = conn.ExecContext(ctx, string(source)); err != nil {
				return fmt.Errorf("migration %s: %w", f.Name(), err)
			}
			if _, err = conn.ExecContext(ctx, "INSERT INTO schema_migrations(name,checksum,applied_at) VALUES(?,?,?)", f.Name(), checksum, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) Optimize(ctx context.Context) error {
	_, err := db.Writer.ExecContext(ctx, "PRAGMA optimize")
	return err
}
func (db *DB) Analyze(ctx context.Context) error {
	return db.Immediate(ctx, func(c *sql.Conn) error { _, err := c.ExecContext(ctx, "ANALYZE"); return err })
}
func (db *DB) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.Reader.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v, err
}
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	return db.Immediate(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
		return err
	})
}
func (db *DB) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := db.Optimize(ctx)
	if db.Reader != nil {
		err = errors.Join(err, db.Reader.Close())
	}
	return errors.Join(err, db.Writer.Close())
}
