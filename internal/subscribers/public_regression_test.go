package subscribers

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestPublicSubscribePreservesExistingProfile(t *testing.T) {
	for _, opt := range []string{"single", "double"} {
		t.Run(opt, func(t *testing.T) {
			ctx := context.Background()
			s, b, l := fixture(t)
			l.OptIn = opt
			if err := s.UpdateList(ctx, l); err != nil {
				t.Fatal(err)
			}
			at := int64(42)
			original, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "reader@example.org", Name: "Original", Fields: map[string]any{"country": "UK"}, ListIDs: []int64{l.ID}, ConsentAt: &at, ConsentIP: "original"})
			if err != nil {
				t.Fatal(err)
			}
			if opt == "double" {
				if err = s.Confirm(ctx, original.ConfirmationTokens[l.ID]); err != nil {
					t.Fatal(err)
				}
			}
			anonymous, err := s.PublicSubscribe(ctx, UpsertInput{BrandID: b.ID, Email: "READER@example.org", Name: "Attacker", Fields: map[string]any{"country": "XX", "extra": true}, ListIDs: []int64{l.ID}, Consent: true, ConsentIP: "attacker", Resubscribe: true, SubscriptionStatus: "unsubscribed", Status: "bounced"})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := s.GetByEmail(ctx, b.ID, original.Email)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Name != original.Name || !reflect.DeepEqual(stored.Fields, original.Fields) || stored.Status != original.Status || stored.ConsentAt == nil || *stored.ConsentAt != at || stored.ConsentIP != "original" {
				t.Fatalf("anonymous request changed profile: %+v", stored)
			}
			if len(anonymous.ConfirmationTokens) != 0 {
				t.Fatal("confirmed membership downgraded")
			}
			var status string
			if err = s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE subscriber_id=? AND list_id=?", original.ID, l.ID).Scan(&status); err != nil || status != "subscribed" {
				t.Fatalf("membership=%s err=%v", status, err)
			}
		})
	}
}

func TestPublicPendingSubscribeAndExplicitRenewalPreserveProfile(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	original, err := s.PublicSubscribe(ctx, UpsertInput{BrandID: b.ID, Email: "pending@example.org", Name: "Initial", Fields: map[string]any{"country": "UK"}, ListIDs: []int64{l.ID}, Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Now().UnixMilli()
	renewal, err := s.PublicSubscribe(ctx, UpsertInput{BrandID: b.ID, Email: original.Email, Name: "Other", Fields: map[string]any{"country": "XX"}, ListIDs: []int64{l.ID}, Consent: true, Resubscribe: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, original.ConfirmationTokens[l.ID]); err == nil {
		t.Fatal("superseded confirmation accepted")
	}
	var started int64
	if err = s.db.Reader.QueryRow("SELECT confirmation_started_at FROM list_subscribers WHERE subscriber_id=? AND list_id=?", original.ID, l.ID).Scan(&started); err != nil || started < requestedAt {
		t.Fatalf("stale confirmation timestamp=%d err=%v", started, err)
	}
	if err = s.Confirm(ctx, renewal.ConfirmationTokens[l.ID]); err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, original.ID, 0); err != nil {
		t.Fatal(err)
	}
	renewal, err = s.PublicSubscribe(ctx, UpsertInput{BrandID: b.ID, Email: original.Email, Name: "Changed", ListIDs: []int64{l.ID}, Consent: true, Resubscribe: true})
	if err != nil || renewal.MarketingUnsubscribedAt == nil {
		t.Fatalf("renewal bypassed double opt-in: %+v %v", renewal, err)
	}
	if err = s.Confirm(ctx, renewal.ConfirmationTokens[l.ID]); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetByEmail(ctx, b.ID, original.Email)
	if err != nil || stored.Name != "Initial" || stored.Fields["country"] != "UK" || stored.MarketingUnsubscribedAt != nil {
		t.Fatalf("confirmed profile=%+v err=%v", stored, err)
	}
	var pending sql.NullInt64
	if err = s.db.Reader.QueryRow("SELECT confirmation_started_at FROM list_subscribers WHERE subscriber_id=? AND list_id=?", original.ID, l.ID).Scan(&pending); err != nil || pending.Valid {
		t.Fatalf("confirmed membership retained pending timestamp: %v %v", pending, err)
	}
}

func TestConfirmationRejectsDeletedMembership(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	v, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "gone@example.org", ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("DELETE FROM list_subscribers WHERE subscriber_id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, v.ConfirmationTokens[l.ID]); err == nil {
		t.Fatal("confirmation reported success for missing membership")
	}
	var active int
	if err = s.db.Reader.QueryRow("SELECT coalesce((SELECT active FROM list_counters WHERE list_id=?),0)", l.ID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("false confirmation changed counters: %d %v", active, err)
	}
}

func TestPublicSubscribeRecordsAbsentInitialConsent(t *testing.T) {
	for _, opt := range []string{"single", "double"} {
		t.Run(opt, func(t *testing.T) {
			ctx := context.Background()
			s, b, l := fixture(t)
			l.OptIn = opt
			if err := s.UpdateList(ctx, l); err != nil {
				t.Fatal(err)
			}
			original, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "imported@example.org", Name: "Imported", Fields: map[string]any{"source": "CSV"}})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UnixMilli()
			v, err := s.PublicSubscribe(ctx, UpsertInput{BrandID: b.ID, Email: original.Email, Name: "Replacement", ListIDs: []int64{l.ID}, Consent: true, ConsentIP: "192.0.2.1", Resubscribe: true})
			if err != nil || v.ConsentAt == nil || *v.ConsentAt < start || v.ConsentIP != "192.0.2.1" || v.Name != "Imported" || v.Fields["source"] != "CSV" {
				t.Fatalf("initial consent missing or profile changed: %+v %v", v, err)
			}
			stored, err := s.GetByEmail(ctx, b.ID, original.Email)
			if err != nil || stored.ConsentAt == nil || *stored.ConsentAt != *v.ConsentAt || stored.ConsentIP != "192.0.2.1" {
				t.Fatalf("consent not persisted: %+v %v", stored, err)
			}
		})
	}
}
