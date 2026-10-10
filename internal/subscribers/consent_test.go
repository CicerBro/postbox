package subscribers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestBrandOptOutPersistsUntilExplicitConsent(t *testing.T) {
	ctx := context.Background()
	s, b, double := fixture(t)
	single, err := s.CreateList(ctx, List{BrandID: b.ID, Name: "single", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "all@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	for _, in := range []UpsertInput{
		{BrandID: b.ID, Email: v.Email, Status: "active"},
		{BrandID: b.ID, Email: v.Email, ListIDs: []int64{single.ID}},
		{BrandID: b.ID, Email: v.Email, ListIDs: []int64{double.ID}, SubscriptionStatus: "subscribed"},
	} {
		if _, err = s.Upsert(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ImportCSV(ctx, strings.NewReader("email\n"+v.Email+"\n"), ImportOptions{BrandID: b.ID, ListIDs: []int64{single.ID}}); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetByEmail(ctx, b.ID, v.Email)
	if err != nil || current.MarketingUnsubscribedAt == nil {
		t.Fatalf("lost opt-out %+v %v", current, err)
	}
	var active int
	if err = s.db.Reader.QueryRow("SELECT count(*) FROM list_subscribers WHERE subscriber_id=? AND status='subscribed'", v.ID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("revived memberships %d %v", active, err)
	}
	if _, err = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{single.ID}, Resubscribe: true}); err == nil {
		t.Fatal("brand consent renewed without consent evidence")
	}
	renewed, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{double.ID}, Resubscribe: true, Consent: true, SubscriptionStatus: "subscribed"})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.MarketingUnsubscribedAt == nil || renewed.ConfirmationTokens[double.ID] == "" {
		t.Fatal("double opt-in bypassed")
	}
	if err = s.Confirm(ctx, renewed.ConfirmationTokens[double.ID]); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetByEmail(ctx, b.ID, v.Email)
	if current.MarketingUnsubscribedAt != nil {
		t.Fatal("confirmation did not renew consent")
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.AddSuppression(ctx, Suppression{BrandID: &b.ID, Kind: "email", Value: v.Email}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{single.ID}, Resubscribe: true, Consent: true}); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetByEmail(ctx, b.ID, v.Email)
	if current.MarketingUnsubscribedAt == nil {
		t.Fatal("suppressed subscriber renewed")
	}
}

func TestGlobalConsentAcrossAudienceAndCampaignStates(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	l.OptIn = "single"
	if err := s.UpdateList(ctx, l); err != nil {
		t.Fatal(err)
	}
	v, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "audience@example.org", Fields: map[string]any{"country": "NL"}, ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.db.Writer.Exec(`INSERT INTO segments(brand_id,name,rules,created_at) VALUES(?,'NL','{"field":"fields.country","op":"eq","value":"NL"}',1)`, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	segment, _ := r.LastInsertId()
	var campaigns []int64
	for i, state := range []string{"preparing", "sending", "paused"} {
		for _, kind := range []string{"list", "segment", "mixed"} {
			r, err = s.db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,?,?,'s','h',1,1)`, b.ID, fmt.Sprintf("%d%s", i, kind), state)
			if err != nil {
				t.Fatal(err)
			}
			campaign, _ := r.LastInsertId()
			campaigns = append(campaigns, campaign)
			for _, target := range []struct {
				kind string
				id   int64
			}{{"list", l.ID}, {"segment", segment}} {
				if kind != "mixed" && target.kind != kind {
					continue
				}
				if _, err = s.db.Writer.Exec(`INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(?,?,?,'include')`, campaign, target.kind, target.id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.db.Writer.Exec(`INSERT INTO campaign_stats(campaign_id) VALUES(?)`, campaign); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Writer.Exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(?,?,'pending')`, campaign, v.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.db.Immediate(ctx, func(c *sql.Conn) error {
				eligible, e := StillInAudienceTx(ctx, c, campaign, v.ID)
				if !eligible && e == nil {
					return fmt.Errorf("initial audience missing")
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	for _, campaign := range campaigns {
		if err = s.db.Immediate(ctx, func(c *sql.Conn) error {
			eligible, e := StillInAudienceTx(ctx, c, campaign, v.ID)
			if eligible && e == nil {
				return fmt.Errorf("opt-out eligible")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	var pending, skipped int
	if err = s.db.Reader.QueryRow(`SELECT count(*) FROM campaign_recipients WHERE status='pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending %d %v", pending, err)
	}
	s.db.Reader.QueryRow(`SELECT sum(skipped) FROM campaign_stats`).Scan(&skipped)
	if skipped != len(campaigns) {
		t.Fatalf("skipped %d", skipped)
	}
	b2, err := s.CreateBrand(ctx, Brand{Name: "other", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Upsert(ctx, UpsertInput{BrandID: b2.ID, Email: v.Email})
	if err != nil || other.MarketingUnsubscribedAt != nil {
		t.Fatalf("brand leaked %+v %v", other, err)
	}
	if err = s.Unsubscribe(ctx, b.ID, other.ID, 0); err == nil {
		t.Fatal("cross-brand mutation accepted")
	}
}

func TestUnsubscribeInvalidatesPendingRenewal(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	v, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "renew@example.org", ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, v.ConfirmationTokens[l.ID]); err == nil {
		t.Fatal("old confirmation revived opt-out")
	}
	v, err = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{l.ID}, Resubscribe: true, Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, v.ConfirmationTokens[l.ID]); err == nil {
		t.Fatal("renewal survived newer opt-out")
	}
}

func TestSingleOptInRenewalRequiresConsentAndSuppressionStillWins(t *testing.T) {
	ctx := context.Background()
	s, b, l := fixture(t)
	l.OptIn = "single"
	if err := s.UpdateList(ctx, l); err != nil {
		t.Fatal(err)
	}
	v, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "single@example.org", ListIDs: []int64{l.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	v, err = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{l.ID}, Resubscribe: true, Consent: true})
	if err != nil || v.MarketingUnsubscribedAt != nil || v.ConsentAt == nil {
		t.Fatalf("single renewal %+v %v", v, err)
	}
	l.OptIn = "double"
	if err = s.UpdateList(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, b.ID, v.ID, 0); err != nil {
		t.Fatal(err)
	}
	v, err = s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: v.Email, ListIDs: []int64{l.ID}, Resubscribe: true, Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddSuppression(ctx, Suppression{BrandID: &b.ID, Kind: "email", Value: v.Email}); err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, v.ConfirmationTokens[l.ID]); err != nil {
		t.Fatal(err)
	}
	v, err = s.GetByEmail(ctx, b.ID, v.Email)
	if err != nil || v.MarketingUnsubscribedAt == nil {
		t.Fatalf("suppressed confirmation renewed %+v %v", v, err)
	}
}
