package send

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestSchedulerClaimsOnlyDueCampaignsAndClosesRescheduling(t *testing.T) {
	db, e, f := fixture(t, 1)
	ctx := context.Background()
	setDate := func(at int64) {
		t.Helper()
		if _, err := db.Writer.Exec(`UPDATE campaigns SET status='scheduled',scheduled_at=? WHERE id=1`, at); err != nil {
			t.Fatal(err)
		}
	}
	setDate(time.Now().Add(-time.Hour).UnixMilli())
	// A date change that wins before the scheduler claims work must be honored.
	setDate(time.Now().Add(time.Hour).UnixMilli())
	if _, err := e.nextCampaign(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("future campaign selected: %v", err)
	}
	setDate(time.Now().Add(-time.Hour).UnixMilli())
	id, err := e.nextCampaign(ctx)
	if err != nil || id != 1 {
		t.Fatalf("due campaign not selected: id=%d err=%v", id, err)
	}
	var status string
	if err := db.Reader.QueryRow(`SELECT status FROM campaigns WHERE id=1`).Scan(&status); err != nil || status != "preparing" {
		t.Fatalf("claim did not fence schedule changes: status=%s err=%v", status, err)
	}
	if f.Calls.Load() != 0 {
		t.Fatal("claim sent mail")
	}
	if err := e.RunCampaign(ctx, id); err != nil {
		t.Fatal(err)
	}
	if f.Accepted.Load() != 1 {
		t.Fatalf("claimed campaign did not send: %d", f.Accepted.Load())
	}
}
