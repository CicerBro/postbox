package subscribers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestLatestActivityUsesCoveringIndexWithoutSortingHistory(t *testing.T) {
	ctx := context.Background()
	s, brand, _ := fixture(t)
	// Populate a realistic history so the planner can choose between the brand,
	// subscriber, and ordered lookup indexes using actual cardinalities.
	if err := s.db.Immediate(ctx, func(c *sql.Conn) error {
		for i := 1; i <= 40; i++ {
			if _, err := c.ExecContext(ctx, `INSERT INTO subscribers(id,brand_id,email,name,fields,status,created_at,updated_at) VALUES(?,?,?,'','{}','active',1,1)`, i, brand.ID, fmt.Sprintf("activity%d@example.org", i)); err != nil {
				return err
			}
			for j := 1; j <= 100; j++ {
				if _, err := c.ExecContext(ctx, `INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,'open',?,'{}')`, brand.ID, i, j); err != nil {
					return err
				}
			}
		}
		_, err := c.ExecContext(ctx, "ANALYZE")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Reader.QueryContext(ctx, "EXPLAIN QUERY PLAN "+latestActivityQuery+"?,?,?)", brand.ID, 1, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "COVERING INDEX subscriber_activity_latest") || strings.Contains(text, "TEMP B-TREE") {
		t.Fatalf("latest activity scans or sorts history:\n%s", text)
	}
}

func TestLatestActivityTimestampTieBreakerAndBrandScope(t *testing.T) {
	ctx := context.Background()
	s, brand, _ := fixture(t)
	v, err := s.Upsert(ctx, UpsertInput{BrandID: brand.ID, Email: "reader@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	at := v.UpdatedAt + 1000
	for _, event := range []struct {
		brand int64
		kind  string
		at    int64
	}{
		{brand.ID, "open", at},
		{brand.ID, "click", at},
		{brand.ID, "delivery", at - 100},
		{brand.ID + 100, "complaint", at + 100},
	} {
		if _, err := s.db.Writer.Exec(`INSERT INTO subscriber_activity(brand_id,subscriber_id,kind,at,detail) VALUES(?,?,?,?,'{}')`, event.brand, v.ID, event.kind, event.at); err != nil {
			t.Fatal(err)
		}
	}
	page := []Subscriber{v}
	if err := s.populateLastActivity(ctx, brand.ID, page); err != nil {
		t.Fatal(err)
	}
	if page[0].LastActivityAt != at || page[0].LastActivityKind != "click" {
		t.Fatalf("latest activity=%s/%d want click/%d", page[0].LastActivityKind, page[0].LastActivityAt, at)
	}
}
