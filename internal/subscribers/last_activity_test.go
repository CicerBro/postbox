package subscribers

import (
	"context"
	"testing"
)

func TestSearchFilteredLastActivity(t *testing.T) {
	s, brand, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: "activity@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.SearchFiltered(ctx, brand.ID, 0, 100, SearchFilter{})
	if err != nil || len(initial) != 1 || initial[0].LastActivityKind != "created" || initial[0].LastActivityAt != v.CreatedAt {
		t.Fatalf("initial creation activity: %+v, %v", initial, err)
	}
	if _, err := s.db.Writer.Exec("UPDATE subscribers SET created_at=100,updated_at=200 WHERE id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Writer.Exec("DELETE FROM subscriber_activity WHERE subscriber_id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	check := func(kind string, at int64) {
		t.Helper()
		rows, err := s.SearchFiltered(ctx, brand.ID, 0, 100, SearchFilter{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("search: %+v, %v", rows, err)
		}
		if rows[0].LastActivityKind != kind || rows[0].LastActivityAt != at {
			t.Fatalf("want %s at %d, got %+v", kind, at, rows[0])
		}
	}
	check("updated", 200)
	for _, event := range []struct {
		brand int64
		kind  string
		at    int64
	}{
		{brand.ID, "complaint", 300},
		{brand.ID, "delivery", 250}, // A delayed event must not replace newer activity.
		{brand.ID + 1, "open", 400}, // Activity must remain inside the selected brand.
	} {
		if _, err := s.db.Writer.Exec("INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,?,?, '{}')", event.brand, v.ID, event.kind, event.at); err != nil {
			t.Fatal(err)
		}
	}
	check("complaint", 300)
	if _, err := s.db.Writer.Exec("UPDATE subscribers SET marketing_unsubscribed_at=500 WHERE id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	check("unsubscribe", 500)
	if _, err := s.db.Writer.Exec("UPDATE subscribers SET updated_at=600 WHERE id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	check("updated", 600)
	if _, err := s.db.Writer.Exec("DELETE FROM subscriber_activity WHERE subscriber_id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Writer.Exec("UPDATE subscribers SET updated_at=100,marketing_unsubscribed_at=NULL WHERE id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	check("created", 100)
}
