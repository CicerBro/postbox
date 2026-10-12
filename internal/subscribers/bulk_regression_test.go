package subscribers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestUpsertBulkRollsBackEverySideEffectOnLateFailure(t *testing.T) {
	ctx := context.Background()
	s, brand, list := fixture(t)
	list.OptIn = "single"
	if err := s.UpdateList(ctx, list); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Writer.Exec(`INSERT INTO webhooks(brand_id,url,events,secret) VALUES(?,'https://example.org/webhook','["subscribe"]','secret')`, brand.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Writer.Exec(`CREATE TRIGGER reject_bulk_row BEFORE INSERT ON subscribers WHEN NEW.email='fail@example.org' BEGIN SELECT RAISE(ABORT,'late bulk failure'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := s.UpsertBulk(ctx, brand.ID, []UpsertInput{
		{Email: "first@example.org", ListIDs: []int64{list.ID}},
		{Email: "fail@example.org", ListIDs: []int64{list.ID}},
	})
	if err == nil || !strings.Contains(err.Error(), "late bulk failure") || out != nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	for _, table := range []string{"subscribers", "list_subscribers", "subscriber_activity", "jobs"} {
		var count int
		if err := s.db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d rows: %v", table, count, err)
		}
	}
	var active, subscribed int
	if err := s.db.Reader.QueryRow("SELECT active,subscribed FROM list_counters WHERE list_id=?", list.ID).Scan(&active, &subscribed); err != nil || active != 0 || subscribed != 0 {
		t.Fatalf("counters=%d/%d err=%v", active, subscribed, err)
	}
}

func TestUpsertBulkPreservesConsentAndDuplicateMemberships(t *testing.T) {
	ctx := context.Background()
	s, brand, double := fixture(t)
	s.SetBaseURL("https://mail.example.org")
	single, err := s.CreateList(ctx, List{BrandID: brand.ID, Name: "Single", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	optedOut, err := s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: "out@example.org", ListIDs: []int64{single.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Unsubscribe(ctx, brand.ID, optedOut.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: "bounce@example.org", Status: "bounced"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.UpsertBulk(ctx, brand.ID, []UpsertInput{
		{Email: "  NEW@EXAMPLE.ORG ", ListIDs: []int64{single.ID}},
		{Email: "new@example.org", Name: "latest", ListIDs: []int64{single.ID}},
		{Email: "confirm@example.org", ListIDs: []int64{double.ID}},
		{Email: "out@example.org", ListIDs: []int64{single.ID}},
		{Email: "bounce@example.org", ListIDs: []int64{single.ID}, Resubscribe: true, Consent: true},
	})
	if err != nil || len(out) != 5 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if out[0].ID != out[1].ID || out[1].Name != "latest" || out[3].MarketingUnsubscribedAt == nil || out[4].Status != "bounced" {
		t.Fatalf("duplicate or suppression state changed: %+v", out)
	}
	if out[2].ConfirmationTokens[double.ID] == "" {
		t.Fatal("double opt-in did not issue a confirmation")
	}
	var status string
	if err := s.db.Reader.QueryRow("SELECT status FROM list_subscribers WHERE list_id=? AND subscriber_id=?", single.ID, optedOut.ID).Scan(&status); err != nil || status != "unsubscribed" {
		t.Fatalf("brand opt-out revived: %s %v", status, err)
	}
	var active, subscribed int
	if err := s.db.Reader.QueryRow("SELECT active,subscribed FROM list_counters WHERE list_id=?", single.ID).Scan(&active, &subscribed); err != nil || active != 1 || subscribed != 2 {
		t.Fatalf("duplicate inflated counters: %d/%d %v", active, subscribed, err)
	}
	var confirmations int
	if err := s.db.Reader.QueryRow("SELECT count(*) FROM jobs WHERE queue='transactional_email'").Scan(&confirmations); err != nil || confirmations != 1 {
		t.Fatalf("confirmation jobs=%d err=%v", confirmations, err)
	}
}

func TestUpsertBulkBoundsTransactionsAndMembershipReads(t *testing.T) {
	ctx := context.Background()
	s, brand, list := fixture(t)
	list.OptIn = "single"
	if err := s.UpdateList(ctx, list); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := s.db.Reader.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	counts := &importCountingDriver{}
	driverName := fmt.Sprintf("bulk-read-count-%d", importDriverSequence.Add(1))
	sql.Register(driverName, counts)
	var err error
	s.db.Writer, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Writer.SetMaxOpenConns(1)
	inputs := make([]UpsertInput, 1000)
	for i := range inputs {
		inputs[i] = UpsertInput{Email: fmt.Sprintf("reader%d@example.org", i/2), ListIDs: []int64{list.ID}, SuppressNotifications: true}
	}
	out, err := s.UpsertBulk(ctx, brand.ID, inputs)
	if err != nil || len(out) != 1000 {
		t.Fatalf("results=%d err=%v", len(out), err)
	}
	if got := counts.transactions.Load(); got != 1 {
		t.Fatalf("writer transactions=%d want=1", got)
	}
	if got := counts.listReads.Load(); got != 1 {
		t.Fatalf("list policy reads=%d want=1", got)
	}
	if got := counts.membershipReads.Load(); got != 2 {
		t.Fatalf("membership reads=%d want=2", got)
	}
	var active int
	if err := s.db.Reader.QueryRow("SELECT active FROM list_counters WHERE list_id=?", list.ID).Scan(&active); err != nil || active != 500 {
		t.Fatalf("duplicate membership count=%d err=%v", active, err)
	}
}

func TestUpsertBulkRejectsForeignListsBeforeWrites(t *testing.T) {
	ctx := context.Background()
	s, brand, list := fixture(t)
	other, err := s.CreateBrand(ctx, Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateList(ctx, List{BrandID: other.ID, Name: "Foreign", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertBulk(ctx, brand.ID, []UpsertInput{{Email: "first@example.org", ListIDs: []int64{list.ID}}, {Email: "foreign@example.org", ListIDs: []int64{foreign.ID}}}); err == nil {
		t.Fatal("foreign list accepted")
	}
	var count int
	if err := s.db.Reader.QueryRow("SELECT count(*) FROM subscribers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial writes=%d err=%v", count, err)
	}
}

func TestUpsertBulkSuppressionRefreshesCacheAcrossDifferentListSelections(t *testing.T) {
	ctx := context.Background()
	s, brand, list := fixture(t)
	list.OptIn = "single"
	if err := s.UpdateList(ctx, list); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: "reader@example.org", ListIDs: []int64{list.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertBulk(ctx, brand.ID, []UpsertInput{
		{Email: "reader@example.org", Status: "bounced"},
		{Email: "reader@example.org", ListIDs: []int64{list.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	var active, unsubscribed int
	if err := s.db.Reader.QueryRow("SELECT active,unsubscribed FROM list_counters WHERE list_id=?", list.ID).Scan(&active, &unsubscribed); err != nil || active != 0 || unsubscribed != 1 {
		t.Fatalf("suppression counted twice: active=%d unsubscribed=%d err=%v", active, unsubscribed, err)
	}
}

func TestUpsertBulkBoundsListParameters(t *testing.T) {
	ctx := context.Background()
	s, brand, list := fixture(t)
	lists := []int64{list.ID}
	if err := s.db.Immediate(ctx, func(c *sql.Conn) error {
		for i := 0; i < 500; i++ {
			r, err := c.ExecContext(ctx, `INSERT INTO lists(brand_id,uuid,name,opt_in,created_at) VALUES(?,?,'Many lists','single',1)`, brand.ID, fmt.Sprintf("bulk-list-%d", i))
			if err != nil {
				return err
			}
			id, err := r.LastInsertId()
			if err != nil {
				return err
			}
			lists = append(lists, id)
			if _, err := c.ExecContext(ctx, "INSERT INTO list_counters(list_id) VALUES(?)", id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := s.db.Reader.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	counts := &importCountingDriver{parameterLimit: 502}
	driverName := fmt.Sprintf("bulk-list-count-%d", importDriverSequence.Add(1))
	sql.Register(driverName, counts)
	var err error
	s.db.Writer, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Writer.SetMaxOpenConns(1)
	if _, err := s.UpsertBulk(ctx, brand.ID, []UpsertInput{{Email: "reader@example.org", ListIDs: lists, SuppressNotifications: true}}); err != nil {
		t.Fatal(err)
	}
	if counts.listReads.Load() != 2 || counts.membershipReads.Load() != 2 {
		t.Fatalf("reads list=%d membership=%d want 2/2", counts.listReads.Load(), counts.membershipReads.Load())
	}
}
