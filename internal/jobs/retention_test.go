package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPurgeFailedUsesTerminalTimeAndPreservesOtherJobs(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if _, err := r.db.Writer.Exec(`INSERT INTO jobs(id,queue,payload,status,run_at,created_at,failed_at) VALUES
(1,'test','{}','failed',0,0,1),(2,'test','{}','failed',0,0,2),
(3,'test','{}','failed',0,0,500),(4,'test','{}','failed',0,0,501),
(5,'test','{}','pending',0,0,1),(6,'test','{}','running',0,0,1),
(7,'test','{}','done',0,0,1)`); err != nil {
		t.Fatal(err)
	}
	if err := r.PurgeFailed(ctx, time.UnixMilli(500), 1); err != nil {
		t.Fatal(err)
	}
	var count, smallest int
	if err := r.db.Reader.QueryRow(`SELECT count(*),min(id) FROM jobs`).Scan(&count, &smallest); err != nil {
		t.Fatal(err)
	}
	if count != 5 || smallest != 3 {
		t.Fatalf("purge removed wrong jobs: count=%d first=%d", count, smallest)
	}
	for _, batch := range []int{0, 10001} {
		if err := r.PurgeFailed(ctx, time.Now(), batch); err == nil {
			t.Fatalf("accepted invalid batch %d", batch)
		}
	}
}

func TestHousekeepingReadsChangedRetentionWithoutRestart(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if err := r.RegisterMaintenance(MaintenanceConfig{BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-15 * 24 * time.Hour).UnixMilli()
	if _, err := r.db.Writer.Exec(`INSERT INTO jobs(queue,payload,status,run_at,created_at,failed_at) VALUES('test','{}','failed',0,0,?)`, at); err != nil {
		t.Fatal(err)
	}
	days, err := FailedJobRetentionDays(ctx, r.db)
	if err != nil || days != 30 {
		t.Fatalf("default=%d error=%v", days, err)
	}
	check := func(want int) {
		t.Helper()
		if err := r.handlers["housekeeping"](ctx, Job{}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := r.db.Reader.QueryRow(`SELECT count(*) FROM jobs WHERE status='failed'`).Scan(&count); err != nil || count != want {
			t.Fatalf("failed count=%d want=%d error=%v", count, want, err)
		}
	}
	check(1)
	if err := r.db.SetSetting(ctx, FailedJobRetentionKey, "7"); err != nil {
		t.Fatal(err)
	}
	check(0)
	if err := r.db.SetSetting(ctx, FailedJobRetentionKey, "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := FailedJobRetentionDays(ctx, r.db); err == nil {
		t.Fatal("accepted corrupted retention policy")
	}
}

func TestTerminalFailureAndInterruptedMailRecordFailureTime(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	r.cfg.MaxAttempts = 1
	if err := r.Register("test", func(context.Context, Job) error { return errors.New("terminal failure") }); err != nil {
		t.Fatal(err)
	}
	id, err := r.Enqueue(ctx, "test", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UnixMilli()
	j, h, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.execute(ctx, j, h)
	var failedAt int64
	if err := r.db.Reader.QueryRow(`SELECT failed_at FROM jobs WHERE id=? AND status='failed'`, id).Scan(&failedAt); err != nil || failedAt < before {
		t.Fatalf("terminal failure timestamp=%d error=%v", failedAt, err)
	}
	if _, err := r.db.Writer.Exec(`INSERT INTO jobs(queue,payload,status,run_at,created_at,locked_until) VALUES('transactional_email','{}','running',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Reader.QueryRow(`SELECT failed_at FROM jobs WHERE queue='transactional_email' AND status='failed'`).Scan(&failedAt); err != nil || failedAt < before {
		t.Fatalf("recovery failure timestamp=%d error=%v", failedAt, err)
	}
}
