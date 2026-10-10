package subscribers

import (
	"context"
	"testing"
)

func TestSuppressionEditSkipsNewTargetOnce(t *testing.T) {
	ctx := context.Background()
	s, b, _ := fixture(t)
	old, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "old@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Upsert(ctx, UpsertInput{BrandID: b.ID, Email: "next@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Writer.Exec(`INSERT INTO campaigns(brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(?,'edit','sending','subject','html',1,1)`, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	campaign, _ := res.LastInsertId()
	if _, err = s.db.Writer.Exec(`INSERT INTO campaign_stats(campaign_id) VALUES(?)`, campaign); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{old.ID, next.ID} {
		if _, err = s.db.Writer.Exec(`INSERT INTO campaign_recipients(campaign_id,subscriber_id,status) VALUES(?,?,'pending')`, campaign, id); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.AddSuppression(ctx, Suppression{BrandID: &b.ID, Kind: "email", Value: old.Email}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = s.db.Reader.QueryRow(`SELECT id FROM suppressions WHERE value=?`, old.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.UpdateSuppression(ctx, id, &b.ID, SuppressionPatch{Value: &next.Email}); err != nil {
			t.Fatal(err)
		}
	}
	for _, subscriber := range []int64{old.ID, next.ID} {
		var status string
		if err = s.db.Reader.QueryRow(`SELECT status FROM campaign_recipients WHERE campaign_id=? AND subscriber_id=?`, campaign, subscriber).Scan(&status); err != nil || status != "skipped" {
			t.Fatal(status, err)
		}
	}
	var skipped int
	if err = s.db.Reader.QueryRow(`SELECT skipped FROM campaign_stats WHERE campaign_id=?`, campaign).Scan(&skipped); err != nil || skipped != 2 {
		t.Fatal(skipped, err)
	}
}
