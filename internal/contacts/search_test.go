package contacts

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSubscriberSearchUsesSelectiveIndexes(t *testing.T) {
	s, brand, list := fixture(t)
	_, err := s.db.Writer.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10000)
INSERT INTO subscribers(brand_id,email,status,created_at,updated_at)
SELECT ?,printf('person%05d@example.org',i),CASE WHEN i%2=0 THEN 'bounced' ELSE 'active' END,0,0 FROM n`, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) SELECT ?,id,'subscribed',0,0 FROM subscribers WHERE id IN (1,9999,10000)", list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filter SearchFilter
		index  string
	}{
		{SearchFilter{EmailPrefix: "person099", ListID: list.ID}, "subscribers_brand_email"},
		{SearchFilter{ListID: list.ID}, "PRIMARY KEY (list_id=? AND subscriber_id>?)"},
		{SearchFilter{Status: "bounced"}, "subscribers_brand_status_id"},
	} {
		q, args, err := subscriberSearchQuery(brand.ID, 0, 5, tc.filter)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.db.Reader.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&plan, detail)
		}
		rows.Close()
		if strings.Contains(plan.String(), "SCAN ") || !strings.Contains(plan.String(), tc.index) {
			t.Fatalf("filter %+v has unbounded plan:\n%s", tc.filter, plan.String())
		}
		if tc.filter.ListID != 0 && tc.filter.EmailPrefix == "" && strings.Contains(plan.String(), "TEMP B-TREE") {
			t.Fatalf("list membership pagination must stream without sorting: %s", plan.String())
		}
		t.Log(plan.String())
	}
	rows, err := s.SearchFiltered(context.Background(), brand.ID, 1, 1, SearchFilter{EmailPrefix: "  PERSON099  ", ListID: list.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != 9999 {
		t.Fatalf("prefix and membership across 10k contacts: %+v, %v", rows, err)
	}
	rows, err = s.SearchFiltered(context.Background(), brand.ID, 9999, 1, SearchFilter{EmailPrefix: "person099", ListID: list.ID})
	if err != nil || len(rows) != 0 {
		t.Fatalf("filtered cursor: %+v, %v", rows, err)
	}
}
