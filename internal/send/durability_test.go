package send

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
)

type reservationCheckingSender struct {
	Sender
	db    *sql.DB
	t     *testing.T
	calls atomic.Int64
}

func (s *reservationCheckingSender) Send(ctx context.Context, r Request) (string, error) {
	s.calls.Add(1)
	var reservations int
	// The independent read pool cannot observe an uncommitted reservation.
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM send_attempts a JOIN campaign_dispatch_ledger d ON d.campaign_id=a.campaign_id AND d.owner_subscriber_id=a.subscriber_id AND d.owner_attempt=a.attempt AND d.generation=a.dispatch_generation JOIN subscribers sub ON sub.id=a.subscriber_id WHERE a.campaign_id=? AND sub.email=? AND a.outcome='started' AND d.outcome='started'`, r.CampaignID, r.To).Scan(&reservations)
	if err != nil || reservations != 1 {
		s.t.Errorf("dispatch before committed reservation: count=%d err=%v", reservations, err)
	}
	return s.Sender.Send(ctx, r)
}
func TestDispatchFollowsCommittedReservation(t *testing.T) {
	db, e, f := fixture(t, 4)
	checking := &reservationCheckingSender{Sender: f, db: db.Reader, t: t}
	e.sender = checking
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if checking.calls.Load() != 4 {
		t.Fatalf("calls=%d", checking.calls.Load())
	}
}
func TestReservationFailureDoesNotDispatch(t *testing.T) {
	db, e, f := fixture(t, 1)
	if err := e.ensureDispatchLedger(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := db.Writer.Exec(`CREATE TRIGGER fail_dispatch BEFORE INSERT ON campaign_dispatch_ledger BEGIN SELECT RAISE(ABORT,'injected reservation failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.RunCampaign(context.Background(), 1); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.Calls.Load() != 0 {
		t.Fatal("dispatched after reservation failed")
	}
	var attempts int
	if err = db.Reader.QueryRow(`SELECT count(*) FROM send_attempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("reservation rollback attempts=%d err=%v", attempts, err)
	}
}
