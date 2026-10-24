package subscribers

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"testing"
)

func TestFilteredExportPagesAllMatchingSubscribers(t *testing.T) {
	s, brand, list := fixture(t)
	// More than one page of matches, interleaved with other delivery statuses.
	tx, err := s.db.Writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1; i <= 2102; i++ {
		status := "active"
		if i%2 == 0 {
			status = "complained"
		}
		if _, err = tx.Exec("INSERT INTO subscribers(id,brand_id,email,status,created_at,updated_at) VALUES(?,?,?,?,1,1)", i, brand.ID, fmt.Sprintf("person%d@example.org", i), status); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(?,?,'subscribed',1,1)", list.ID, i); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, listID := range []int64{0, list.ID} {
		var out bytes.Buffer
		if err = s.ExportCSVFiltered(context.Background(), &out, brand.ID, listID, "complained"); err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(&out).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1052 {
			t.Fatalf("got %d rows including header", len(rows))
		}
		for i, row := range rows[1:] {
			if row[0] != fmt.Sprintf("person%d@example.org", (i+1)*2) || row[2] != "complained" {
				t.Fatalf("row %d: %v", i, row)
			}
		}
	}
}
