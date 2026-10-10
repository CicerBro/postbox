package subscribers

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSubscriberSortIndexes(t *testing.T) {
	s, brand, _ := fixture(t)
	if _, err := s.db.Writer.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10000)
INSERT INTO subscribers(brand_id,email,name,status,gdpr_consent_at,created_at,updated_at)
SELECT ?,printf('person%05d@example.org',i),printf('Name%03d',i%100),CASE WHEN i%2=0 THEN 'bounced' ELSE 'active' END,
CASE WHEN i%3=0 THEN NULL ELSE i%100 END,i%100,0 FROM n`, brand.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Writer.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	for column, index := range map[string]string{
		"id": "subscribers_brand_id", "email": "subscribers_brand_email", "name": "subscribers_brand_name_id",
		"status": "subscribers_brand_status_id", "gdpr_consent_at": "subscribers_brand_consent_id", "created_at": "subscribers_brand_created",
	} {
		for _, direction := range []string{"asc", "desc"} {
			for _, after := range []int64{0, 5000} {
				q, args, err := subscriberSearchQuery(brand.ID, after, 100, SearchFilter{Sort: column, Direction: direction})
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
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if !strings.Contains(plan.String(), index) || strings.Contains(plan.String(), "TEMP B-TREE") || strings.Contains(plan.String(), "SCAN s") {
					t.Fatalf("%s/%s after=%d does not stream through its ordering index:\n%s", column, direction, after, plan.String())
				}
				if column == "gdpr_consent_at" && after > 0 && !strings.Contains(plan.String(), "<expr>") {
					t.Fatalf("consent cursor must seek the expression index range:\n%s", plan.String())
				}
			}
		}
	}
}

func TestSubscriberConsentDuplicatesAndMutableAnchor(t *testing.T) {
	s, brand, _ := fixture(t)
	if _, err := s.db.Writer.Exec(`INSERT INTO subscribers(brand_id,email,name,gdpr_consent_at,created_at,updated_at) VALUES
(?,'one@example.org','A',NULL,0,0),(?,'two@example.org','B',NULL,0,0),
(?,'three@example.org','C',0,0,0),(?,'four@example.org','D',10,0,0)`, brand.ID, brand.ID, brand.ID, brand.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, direction := range []string{"asc", "desc"} {
		var after int64
		for i := 0; i < 4; i++ {
			rows, err := s.SearchFiltered(ctx, brand.ID, after, 1, SearchFilter{Sort: "gdpr_consent_at", Direction: direction})
			want := int64(i + 1)
			if direction == "desc" {
				want = int64(4 - i)
			}
			if err != nil || len(rows) != 1 || rows[0].ID != want {
				t.Fatalf("%s page %d: %+v %v", direction, i, rows, err)
			}
			after = rows[0].ID
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		if _, err := s.db.Writer.Exec("UPDATE subscribers SET name='B' WHERE id=2"); err != nil {
			t.Fatal(err)
		}
		rows, err := s.SearchFiltered(ctx, brand.ID, 2, 10, SearchFilter{Sort: "name", Direction: direction})
		want := 2
		if direction == "desc" {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Fatalf("original anchor %s: %+v %v", direction, rows, err)
		}
		if _, err := s.db.Writer.Exec("UPDATE subscribers SET name='Z' WHERE id=2"); err != nil {
			t.Fatal(err)
		}
		rows, err = s.SearchFiltered(ctx, brand.ID, 2, 10, SearchFilter{Sort: "name", Direction: direction})
		want = 0
		if direction == "desc" {
			want = 3
		}
		if err != nil || len(rows) != want {
			t.Fatalf("edited anchor %s: %+v %v", direction, rows, err)
		}
	}
	if _, err := s.db.Writer.Exec("DELETE FROM subscribers WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"asc", "desc"} {
		rows, err := s.SearchFiltered(ctx, brand.ID, 2, 10, SearchFilter{Sort: "name", Direction: direction})
		if err != nil || len(rows) != 0 {
			t.Fatalf("deleted anchor %s: %+v %v", direction, rows, err)
		}
	}
	other, err := s.CreateBrand(ctx, Brand{Name: "Other", FromEmail: "other@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("INSERT INTO subscribers(brand_id,email,name,created_at,updated_at) VALUES(?,'other@example.org','ZZ',0,0)", other.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SearchFiltered(ctx, other.ID, 1, 10, SearchFilter{Sort: "name"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("other brand anchor: %+v %v", rows, err)
	}
}
