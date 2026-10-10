package main

import (
	"context"
	"database/sql"
	"errors"
	"postbox/internal/subscribers"
	"testing"
)

func TestBrandOptOutBlocksWelcomeButAllowsFreshConfirmation(t *testing.T) {
	ctx := context.Background()
	db := mailFixture(t)
	s := subscribers.New(db)
	if err := s.Unsubscribe(ctx, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	// Even a stale membership reset cannot allow marketing mail.
	if _, err := db.Writer.Exec(`UPDATE list_subscribers SET status='subscribed' WHERE list_id=1`); err != nil {
		t.Fatal(err)
	}
	p := transactionalEmail{Purpose: "thankyou", SubscriberID: 1, ListID: 1, BrandID: 1, To: "reader@example.org"}
	if err := validateTransactional(ctx, db, p); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("welcome accepted %v", err)
	}
	if _, err := db.Writer.Exec(`UPDATE lists SET opt_in='double' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	v, err := s.Upsert(ctx, subscribers.UpsertInput{BrandID: 1, Email: p.To, ListIDs: []int64{2}, Resubscribe: true, Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	p.Purpose = ""
	p.ListID = 2
	p.ConfirmationToken = v.ConfirmationTokens[2]
	if p.ConfirmationToken == "" {
		t.Fatal("missing confirmation")
	}
	if err = validateTransactional(ctx, db, p); err != nil {
		t.Fatalf("confirmation blocked by marketing opt-out %v", err)
	}
	if err = s.Unsubscribe(ctx, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err = validateTransactional(ctx, db, p); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale queued confirmation accepted %v", err)
	}
}
