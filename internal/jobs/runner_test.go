package jobs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"postbox/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, Config{Lease: 300 * time.Millisecond, RetryBase: time.Millisecond, RetryMax: 5 * time.Millisecond, MaxAttempts: 3})
}
func TestAtomicClaims(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	r.Register("test", func(context.Context, Job) error { return nil })
	for i := 0; i < 20; i++ {
		if _, err := r.Enqueue(ctx, "test", map[string]int{"i": i}, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	seen := sync.Map{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, _, err := r.claim(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				if _, loaded := seen.LoadOrStore(j.ID, true); loaded {
					t.Errorf("duplicate claim %d", j.ID)
				}
			}
		}()
	}
	wg.Wait()
	n := 0
	seen.Range(func(any, any) bool { n++; return true })
	if n != 20 {
		t.Fatalf("claimed %d", n)
	}
}
func TestRetryRecoveryAndHeartbeat(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	r.Register("test", func(context.Context, Job) error { return errors.New("temporary") })
	id, err := r.Enqueue(ctx, "test", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		j, h, err := r.claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.execute(ctx, j, h)
		var status string
		var attempts int
		err = r.db.Reader.QueryRowContext(ctx, "SELECT status,attempts FROM jobs WHERE id=?", id).Scan(&status, &attempts)
		if err != nil {
			t.Fatal(err)
		}
		want := "pending"
		if attempt == 3 {
			want = "failed"
		}
		if status != want || attempts != attempt {
			t.Fatalf("status %s attempt %d", status, attempts)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err = r.db.Writer.ExecContext(ctx, "UPDATE jobs SET status='running',locked_until=? WHERE id=?", time.Now().Add(-time.Second).UnixMilli(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	j, _, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.execute(ctx, j, func(ctx context.Context, j Job) error {
		time.Sleep(650 * time.Millisecond)
		if err := r.Recover(ctx); err != nil {
			return err
		}
		var status string
		err := r.db.Reader.QueryRowContext(ctx, "SELECT status FROM jobs WHERE id=?", id).Scan(&status)
		if err != nil {
			return err
		}
		if status != "running" {
			return errors.New("active heartbeat was recovered")
		}
		return nil
	})
	if r.Metrics().Completed != 1 {
		t.Fatalf("metrics %+v", r.Metrics())
	}
}
func TestWebhookSafetyAndHMAC(t *testing.T) {
	for _, u := range []string{"http://localhost/x", "http://127.0.0.1", "http://[::1]", "http://169.254.169.254/latest/meta-data", "http://user:secret@example.com", "file:///etc/passwd", "http://100.100.100.200"} {
		if ValidateWebhookURL(u) == nil {
			t.Errorf("allowed %s", u)
		}
	}
	if err := ValidateWebhookURL("https://example.com/webhook"); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("123.{}"))
	if Signature("secret", "123", []byte("{}")) != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("signature mismatch")
	}
	if _, err := publicDial(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("private dial allowed")
	}
}
func TestRetentionPreservesAggregates(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	err := r.db.Immediate(ctx, func(c *sql.Conn) error {
		for _, q := range []string{`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'b','n','e@example.com',0)`, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(1,1,'campaign','draft','s','h',0,0)`, `INSERT INTO campaign_stats(campaign_id,opens_total) VALUES(1,42)`, `INSERT INTO opens(campaign_id,subscriber_id,at) VALUES(1,1,1),(1,1,2),(1,1,9999999999999)`, `INSERT INTO clicks(campaign_id,link_id,subscriber_id,at) VALUES(1,1,1,1)`, `INSERT INTO ses_events(sns_message_id,ses_message_id,type,recipient,at,payload) VALUES('a','b','Delivery','e@example.com',1,'{}')`} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.PurgeRaw(ctx, time.UnixMilli(3), 1); err != nil {
		t.Fatal(err)
	}
	var count, total int
	if err = r.db.Reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM opens").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = r.db.Reader.QueryRowContext(ctx, "SELECT opens_total FROM campaign_stats WHERE campaign_id=1").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if count != 1 || total != 42 {
		t.Fatalf("count %d aggregate %d", count, total)
	}
	path := filepath.Join(t.TempDir(), "backup.sqlite")
	if err = r.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	backup, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err = backup.Reader.QueryRowContext(ctx, "SELECT opens_total FROM campaign_stats WHERE campaign_id=1").Scan(&total); err != nil || total != 42 {
		t.Fatalf("backup total=%d err=%v", total, err)
	}
}

func TestEmitAndDailyRollup(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if _, err := r.db.Writer.ExecContext(ctx, `INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'b','n','e@example.com',0); INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list','l','single',0); INSERT INTO list_counters(list_id,active,subscribed) VALUES(1,10,10); INSERT INTO webhooks(brand_id,url,events,secret) VALUES(1,'https://example.com','["subscribe","campaign_started"]','secret')`); err != nil {
		t.Fatal(err)
	}
	if err := r.Emit(ctx, 1, "subscribe", map[string]string{"email": "e@example.com"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := r.db.Reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE queue='outbound_webhook'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("enqueued %d err %v", n, err)
	}
	day := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := r.Rollup(ctx, day); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Writer.ExecContext(ctx, "UPDATE list_counters SET active=12,subscribed=13,unsubscribed=1 WHERE list_id=1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Rollup(ctx, day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.Rollup(ctx, day.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var active, subscribed, unsubscribed int
	if err := r.db.Reader.QueryRowContext(ctx, "SELECT active,subscribed,unsubscribed FROM list_daily_stats WHERE list_id=1 AND day=?", time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).UnixMilli()).Scan(&active, &subscribed, &unsubscribed); err != nil {
		t.Fatal(err)
	}
	if active != 12 || subscribed != 3 || unsubscribed != 1 {
		t.Fatalf("rollup %d %d %d", active, subscribed, unsubscribed)
	}
}

func TestDueAndRetentionIndexes(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	queries := []string{"SELECT id FROM jobs WHERE queue='test' AND status='pending' AND run_at<=1 ORDER BY run_at,id LIMIT 1", "SELECT id FROM opens WHERE at<1 ORDER BY at,id LIMIT 1000", "SELECT id FROM clicks WHERE at<1 ORDER BY at,id LIMIT 1000", "SELECT id FROM ses_events WHERE at<1 ORDER BY at,id LIMIT 1000"}
	for _, q := range queries {
		rows, err := r.db.Reader.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "USING") {
				indexed = true
			}
			if strings.Contains(detail, "SCAN ") {
				t.Errorf("unindexed hot query %s: %s", q, detail)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !indexed {
			t.Errorf("no index: %s", q)
		}
	}
}
