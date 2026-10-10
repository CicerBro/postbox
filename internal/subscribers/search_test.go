package subscribers

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
		t.Fatalf("prefix and membership across 10k subscribers: %+v, %v", rows, err)
	}
	rows, err = s.SearchFiltered(context.Background(), brand.ID, 9999, 1, SearchFilter{EmailPrefix: "person099", ListID: list.ID})
	if err != nil || len(rows) != 0 {
		t.Fatalf("filtered cursor: %+v, %v", rows, err)
	}
}

func TestSubscriberSortedPagination(t *testing.T) {
	s, brand, list := fixture(t)
	_, err := s.db.Writer.Exec(`INSERT INTO subscribers(brand_id,email,name,status,gdpr_consent_at,created_at,updated_at) VALUES
(?, 'z@example.org', 'Same', 'active', NULL, 30, 0),
(?, 'a@example.org', 'Same', 'active', 10, 10, 0),
(?, 'm@example.org', 'Alice', 'bounced', 20, 20, 0),
(?, 'outside@example.org', 'Excluded', 'active', 40, 40, 0)`, brand.ID, brand.ID, brand.ID, brand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) SELECT ?,id,'subscribed',0,0 FROM subscribers WHERE email<>'outside@example.org'", list.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		column string
		asc    []string
	}{
		{"email", []string{"a@example.org", "m@example.org", "z@example.org"}},
		{"name", []string{"m@example.org", "z@example.org", "a@example.org"}},
		{"status", []string{"z@example.org", "a@example.org", "m@example.org"}},
		{"created_at", []string{"a@example.org", "m@example.org", "z@example.org"}},
		{"gdpr_consent_at", []string{"z@example.org", "a@example.org", "m@example.org"}},
	} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(tc.column+"/"+direction, func(t *testing.T) {
				var after int64
				for i := 0; i < len(tc.asc); i++ {
					rows, err := s.SearchFiltered(context.Background(), brand.ID, after, 1, SearchFilter{ListID: list.ID, Sort: tc.column, Direction: direction})
					position := i
					if direction == "desc" {
						position = len(tc.asc) - 1 - i
					}
					if err != nil || len(rows) != 1 || rows[0].Email != tc.asc[position] {
						t.Fatalf("page %d: %+v, %v; want %s", i, rows, err, tc.asc[position])
					}
					after = rows[0].ID
				}
				rows, err := s.SearchFiltered(context.Background(), brand.ID, after, 1, SearchFilter{ListID: list.ID, Sort: tc.column, Direction: direction})
				if err != nil || len(rows) != 0 {
					t.Fatalf("end of audience: %+v, %v", rows, err)
				}
			})
		}
	}
	rows, err := s.SearchFiltered(context.Background(), brand.ID, 0, 10, SearchFilter{EmailPrefix: "a@", Status: "active", ListID: list.ID, MembershipStatus: "subscribed", Sort: "name", Direction: "desc"})
	if err != nil || len(rows) != 1 || rows[0].Email != "a@example.org" {
		t.Fatalf("sorted filters: %+v, %v", rows, err)
	}
	for _, filter := range []SearchFilter{{Sort: "email; DROP TABLE subscribers"}, {Direction: "sideways"}} {
		if _, err := s.SearchFiltered(context.Background(), brand.ID, 0, 10, filter); err == nil {
			t.Fatalf("expected invalid sort to fail: %+v", filter)
		}
	}
}
