package httpapi

import (
	"context"
	"net/url"
	"postbox/internal/subscribers"
	"strconv"
	"strings"
	"testing"
)

func TestCSVImportReportsCommittedRowsAfterLateBatchFailure(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	s := subscribers.New(db)
	brand, err := s.CreateBrand(context.Background(), subscribers.Brand{Name: "CSV", FromEmail: "sender@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Writer.Exec(`CREATE TRIGGER reject_second_csv_row BEFORE INSERT ON subscribers
WHEN NEW.email='second@example.org' BEGIN SELECT RAISE(FAIL,'rejected fixture row'); END`); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/subscribers/import?brand_id=" + strconv.FormatInt(brand.ID, 10) + "&options=" + url.QueryEscape(`{"batch_size":1}`)
	w := request(mux, "POST", path, "email\nfirst@example.org\nsecond@example.org\n", &cookie, csrf)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "after 1 committed rows") || !strings.Contains(w.Body.String(), "pending batch was not committed") {
		t.Fatalf("partial import response %d %s", w.Code, w.Body.String())
	}
	if _, err = s.GetByEmail(context.Background(), brand.ID, "first@example.org"); err != nil {
		t.Fatalf("committed batch lost: %v", err)
	}
	if _, err = s.GetByEmail(context.Background(), brand.ID, "second@example.org"); err == nil {
		t.Fatal("failing batch was committed")
	}
}
