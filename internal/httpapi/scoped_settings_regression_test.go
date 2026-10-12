package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"postbox/internal/subscribers"
	"testing"
)

func TestBrandDeletionCannotExposeSettingsToReplacement(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	service := subscribers.New(db)
	brand, err := service.CreateBrand(context.Background(), subscribers.Brand{Name: "Private brand", FromEmail: "private@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf("/api/v1/settings?brand_id=%d", brand.ID)
	response := request(mux, "PATCH", settings, `{"goodbye_html":"Private brand page"}`, &cookie, csrf)
	if response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	response = request(mux, "DELETE", fmt.Sprintf("/api/v1/brands/%d", brand.ID), "", &cookie, csrf)
	if response.Code != 200 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	// Force ID reuse to verify relational ownership independently of tracking IDs.
	if _, err = db.Writer.Exec(`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(?,'Replacement','Sender','replacement@example.org',0)`, brand.ID); err != nil {
		t.Fatal(err)
	}
	response = request(mux, "GET", settings, "", &cookie, csrf)
	var values map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &values) != nil || len(values) != 0 {
		t.Fatalf("replacement inherited private settings: %d %s", response.Code, response.Body)
	}
}
