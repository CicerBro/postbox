package jobs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecurringScheduleSurvivesTerminalFailureAndRestart(t *testing.T) {
	r := testRunner(t)
	r.cfg.MaxAttempts = 1
	ctx := context.Background()
	if err := r.RegisterRecurring("periodic", time.Hour, func(context.Context, Job) error { return errors.New("offline") }); err != nil {
		t.Fatal(err)
	}
	if err := r.ScheduleRecurring(ctx); err != nil {
		t.Fatal(err)
	}
	j, h, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.execute(ctx, j, h)
	var due int64
	if err := r.db.Reader.QueryRow(`SELECT next_run_at FROM recurring_jobs WHERE queue='periodic'`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	restarted := New(r.db, r.cfg)
	if err := restarted.RegisterRecurring("periodic", time.Hour, func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ScheduleRecurring(ctx); err != nil {
		t.Fatal(err)
	}
	var preserved int64
	if err := r.db.Reader.QueryRow(`SELECT next_run_at FROM recurring_jobs WHERE queue='periodic'`).Scan(&preserved); err != nil || due != preserved {
		t.Fatalf("restart moved due time: %d -> %d, %v", due, preserved, err)
	}
	if _, _, err := restarted.claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("future occurrence claimed: %v", err)
	}
	if _, err := r.db.Writer.Exec(`UPDATE recurring_jobs SET next_run_at=? WHERE queue='periodic'`, time.Now().Add(-48*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	next, h, err := restarted.claim(ctx)
	if err != nil || next.ID == j.ID || next.Attempts != 1 {
		t.Fatalf("new occurrence: %+v, %v", next, err)
	}
	restarted.execute(ctx, next, h)
	var failed, completed, active int
	if err := r.db.Reader.QueryRow(`SELECT count(*) FILTER (WHERE status='failed'),count(*) FILTER (WHERE status='done'),count(*) FILTER (WHERE status IN ('pending','running')) FROM jobs WHERE queue='periodic'`).Scan(&failed, &completed, &active); err != nil {
		t.Fatal(err)
	}
	if failed != 1 || completed != 1 || active != 0 {
		t.Fatalf("occurrences failed=%d done=%d active=%d", failed, completed, active)
	}
	if _, _, err := restarted.claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missed intervals produced a backlog: %v", err)
	}
}

func TestRecurringJobsDoNotOverlapAndRecoverSameOccurrence(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	if err := r.RegisterRecurring("periodic", time.Hour, func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.ScheduleRecurring(ctx); err != nil {
		t.Fatal(err)
	}
	j, _, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Writer.Exec(`UPDATE recurring_jobs SET next_run_at=0 WHERE queue='periodic'`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.ScheduleRecurring(ctx); err != nil {
				t.Error(err)
			}
			if _, _, err := r.claim(ctx); !errors.Is(err, sql.ErrNoRows) {
				t.Errorf("overlapping occurrence: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := r.db.Writer.Exec(`UPDATE jobs SET locked_until=0 WHERE id=?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := r.claim(ctx)
	if err != nil || recovered.ID != j.ID || recovered.Attempts != 2 {
		t.Fatalf("recovered occurrence: %+v, %v", recovered, err)
	}
}

func TestRecurringScheduleAdoptsLegacyPendingJob(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	at := time.Now().Add(time.Hour)
	id, err := r.Enqueue(ctx, "periodic", nil, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterRecurring("periodic", time.Hour, func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.ScheduleRecurring(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var pending, due int64
	if err := r.db.Reader.QueryRow(`SELECT count(*),min(id),min(run_at) FROM jobs WHERE queue='periodic'`).Scan(&count, &pending, &due); err != nil || count != 1 || pending != id || due != at.UnixMilli() {
		t.Fatalf("legacy occurrence duplicated or moved: %d %d %d %v", count, pending, due, err)
	}
	if err := r.db.Reader.QueryRow(`SELECT next_run_at FROM recurring_jobs WHERE queue='periodic'`).Scan(&due); err != nil || due != at.Add(time.Hour).UnixMilli() {
		t.Fatalf("legacy next due=%d, %v", due, err)
	}
}

func TestBackupRecursAfterDestinationRepair(t *testing.T) {
	r := testRunner(t)
	r.cfg.MaxAttempts = 1
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(dir, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterMaintenance(MaintenanceConfig{BackupDirectory: dir}); err != nil {
		t.Fatal(err)
	}
	if err := r.ScheduleMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		j, h, err := r.claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.execute(ctx, j, h)
		if j.Queue == "backup" {
			break
		}
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Writer.Exec(`UPDATE recurring_jobs SET next_run_at=0 WHERE queue='backup'`); err != nil {
		t.Fatal(err)
	}
	j, h, err := r.claim(ctx)
	if err != nil || j.Queue != "backup" {
		t.Fatalf("backup did not recur: %+v %v", j, err)
	}
	r.execute(ctx, j, h)
	files, err := filepath.Glob(filepath.Join(dir, "*.sqlite"))
	if err != nil || len(files) != 1 {
		t.Fatalf("backup files=%v error=%v", files, err)
	}
}
