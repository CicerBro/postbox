package subscribers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCSVExportSpreadsheetSafetyAndRawData(t *testing.T) {
	s, brand, _ := fixture(t)
	ctx := context.Background()
	email, name := "=formula@example.org", "\t\u200b=HYPERLINK(\"https://example.org\")"
	fields := map[string]any{"note": "雪, café \"quoted\"\nsecond line", "formula": "=1+1"}
	// Insert directly to cover legacy data as well as normal application writes.
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Writer.Exec("INSERT INTO subscribers(brand_id,email,name,fields,created_at,updated_at) VALUES(?,?,?,?,1,1)", brand.ID, email, name, string(encoded)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = s.ExportCSV(ctx, &out, brand.ID, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "'"+email || rows[1][1] != "'"+name || rows[1][3] != string(encoded) {
		t.Fatalf("export rows = %#v", rows)
	}
	var rawEmail, rawName string
	if err = s.db.Reader.QueryRow("SELECT email,name FROM subscribers WHERE brand_id=?", brand.ID).Scan(&rawEmail, &rawName); err != nil || rawEmail != email || rawName != name {
		t.Fatalf("stored values changed: %q %q %v", rawEmail, rawName, err)
	}
	// A literal apostrophe is data. Reimporting a spreadsheet download must not
	// guess whether it was an escape or strip it to reactivate a formula.
	dest, err := s.CreateBrand(ctx, Brand{Name: "Destination", FromEmail: "dest@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ImportCSV(ctx, strings.NewReader(out.String()), ImportOptions{BrandID: dest.ID})
	if err != nil || result.Imported != 1 {
		t.Fatal(fmt.Sprintf("import: %+v %v", result, err))
	}
	if err = s.db.Reader.QueryRow("SELECT name FROM subscribers WHERE brand_id=?", dest.ID).Scan(&rawName); err != nil || rawName != "'"+name {
		t.Fatalf("spreadsheet escape not retained: %q %v", rawName, err)
	}
}
