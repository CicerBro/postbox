package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"postbox/internal/config"
	"postbox/internal/jobs"
	"postbox/internal/send"
	"postbox/internal/store"
)

func mailFixture(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'Brand','Sender','sender@example.org',1); INSERT INTO subscribers(id,brand_id,email,created_at,updated_at) VALUES(1,1,'reader@example.org',1,1); INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'list-1','One','single',1),(2,1,'list-2','Two','single',1); INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,1,'subscribed',1,1),(2,1,'subscribed',1,1)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestThankyouRequiresCurrentOriginatingListConsent(t *testing.T) {
	db := mailFixture(t)
	p := transactionalEmail{Purpose: "thankyou", SubscriberID: 1, ListID: 1, BrandID: 1, To: "reader@example.org"}
	if err := validateTransactional(context.Background(), db, p); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE list_subscribers SET status='unsubscribed' WHERE list_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := validateTransactional(context.Background(), db, p); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unsubscribed welcome allowed: %v", err)
	}
	p.ListID = 2
	if err := validateTransactional(context.Background(), db, p); err != nil {
		t.Fatalf("other subscribed list denied: %v", err)
	}
	p.ListID = 0
	if err := validateTransactional(context.Background(), db, p); err == nil {
		t.Fatal("welcome without originating list accepted")
	}
}

type notificationSender struct{ calls atomic.Int64 }

func (s *notificationSender) GetAccount(context.Context) (send.Quota, error) {
	return send.Quota{MaxSendRate: 10000, Max24HourSend: 10000}, nil
}
func (s *notificationSender) Send(context.Context, send.Request) (string, error) {
	if s.calls.Add(1) == 1 {
		return "accepted", nil
	}
	return "", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rejected"}
}

func TestPartialNotificationDeliveryNeverRetriesAcceptedRecipient(t *testing.T) {
	db := mailFixture(t)
	t.Setenv("POSTBOX_NOTIFICATION_TO", "one@example.org,two@example.org")
	t.Setenv("POSTBOX_NOTIFICATION_FROM", "sender@example.org")
	sender := &notificationSender{}
	engine := send.New(db, sender, send.Config{})
	runner := jobs.New(db, jobs.Config{Workers: 1, PollInterval: time.Millisecond, RetryBase: time.Millisecond})
	if err := registerJobs(db, runner, engine, config.Config{DB: filepath.Join(t.TempDir(), "test.db")}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Emit(context.Background(), 1, "campaign_started", map[string]int{"campaign_id": 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := db.Reader.QueryRow(`SELECT status FROM jobs WHERE queue='email_notification'`).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "failed" {
			if sender.calls.Load() != 2 {
				t.Fatalf("notification calls=%d", sender.calls.Load())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("partially accepted notification did not terminate")
}
