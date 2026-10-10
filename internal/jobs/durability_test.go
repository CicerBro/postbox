package jobs

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"postbox/internal/store"
)

// This simulates a process interruption after external acceptance, before the
// completion transaction. It does not simulate loss of the host's write cache.
func TestMailReservationCrashChild(t *testing.T) {
	path := os.Getenv("POSTBOX_MAIL_CRASH_DB")
	if path == "" {
		return
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := New(db, Config{})
	if err = r.Register("transactional_email", func(ctx context.Context, j Job) error {
		var state string
		var attempts, mode int
		if err := db.Reader.QueryRow(`SELECT status,attempts FROM jobs WHERE id=?`, j.ID).Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if err := db.Writer.QueryRow(`PRAGMA synchronous`).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if state != "running" || attempts != 1 || mode != 1 {
			t.Fatalf("pre-dispatch reservation=%s attempts=%d mode=%d", state, attempts, mode)
		}
		// Record fake external acceptance and exit without DB.Close or completion.
		if err := os.WriteFile(path+".accepted", []byte("accepted"), 0600); err != nil {
			t.Fatal(err)
		}
		os.Exit(31)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	j, h, err := r.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.execute(context.Background(), j, h)
	t.Fatal("child failed to interrupt")
}
func TestMailReservationProcessRecoveryDoesNotResend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := New(db, Config{})
	if _, err = r.Enqueue(context.Background(), "transactional_email", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMailReservationCrashChild$")
	cmd.Env = append(os.Environ(), "POSTBOX_MAIL_CRASH_DB="+path)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("child did not interrupt: %s", out)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 31 {
		t.Fatalf("child error=%v output=%s", err, out)
	}
	if _, err = os.Stat(path + ".accepted"); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r = New(db, Config{})
	var state string
	if err = db.Reader.QueryRow(`SELECT status FROM jobs`).Scan(&state); err != nil || state != "running" {
		t.Fatalf("lost reservation state=%s err=%v", state, err)
	}
	if _, err = db.Writer.Exec(`UPDATE jobs SET locked_until=0`); err != nil {
		t.Fatal(err)
	}
	if err = r.Register("transactional_email", func(context.Context, Job) error { t.Fatal("recovery resent accepted mail"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err = r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.claim(context.Background()); err != sql.ErrNoRows {
		t.Fatalf("recovered mail was claimable: %v", err)
	}
	if err = db.Reader.QueryRow(`SELECT status FROM jobs`).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("recovery=%s err=%v", state, err)
	}
}

func TestMailClaimFailureDoesNotInvokeHandler(t *testing.T) {
	r := testRunner(t)
	if err := r.Register("transactional_email", func(context.Context, Job) error { t.Fatal("handler ran without committed claim"); return nil }); err != nil {
		t.Fatal(err)
	}
	id, err := r.Enqueue(context.Background(), "transactional_email", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Writer.Exec(`CREATE TRIGGER fail_mail_claim BEFORE UPDATE OF status ON jobs WHEN NEW.status='running' BEGIN SELECT RAISE(ABORT,'injected claim failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, h, err := r.claim(context.Background()); err == nil || h != nil {
		t.Fatalf("failed claim returned handler: err=%v", err)
	}
	var status string
	if err = r.db.Reader.QueryRow(`SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("failed claim did not rollback: status=%s err=%v", status, err)
	}
}
