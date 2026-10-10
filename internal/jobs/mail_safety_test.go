package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMailRecoveryNeverRequeuesUncertainDelivery(t *testing.T) {
	r := testRunner(t)
	for _, queue := range []string{"transactional_email", "email_notification", "csv_import", "outbound_webhook"} {
		id, err := r.Enqueue(context.Background(), queue, nil, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.db.Writer.Exec(`UPDATE jobs SET status='running',locked_until=1 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, queue := range []string{"transactional_email", "email_notification", "csv_import", "outbound_webhook"} {
		var status string
		if err := r.db.Reader.QueryRow(`SELECT status FROM jobs WHERE queue=?`, queue).Scan(&status); err != nil {
			t.Fatal(err)
		}
		want := "pending"
		if emailQueue(queue) {
			want = "failed"
		}
		if status != want {
			t.Fatalf("%s status=%s want=%s", queue, status, want)
		}
	}
}

func TestMailRetriesRequireDefinitiveNonDelivery(t *testing.T) {
	for _, queue := range []string{"transactional_email", "email_notification"} {
		for _, safe := range []bool{false, true} {
			t.Run(queue+map[bool]string{false: "/unknown", true: "/rejected"}[safe], func(t *testing.T) {
				r := testRunner(t)
				if err := r.Register(queue, func(context.Context, Job) error {
					err := errors.New("transport failure")
					if safe {
						return RetrySafe(err)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				id, err := r.Enqueue(context.Background(), queue, nil, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				j, h, err := r.claim(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				r.execute(context.Background(), j, h)
				var status string
				if err := r.db.Reader.QueryRow(`SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil {
					t.Fatal(err)
				}
				want := "failed"
				if safe {
					want = "pending"
				}
				if status != want {
					t.Fatalf("status=%s want=%s", status, want)
				}
			})
		}
	}
}
