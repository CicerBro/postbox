package httpapi

import (
	"encoding/json"
	"testing"
)

func TestSuppressionEditingAndValidation(t *testing.T) {
	_, mux, db, cookie, csrf := setup(t)
	for _, stmt := range []string{
		`INSERT INTO brands(id,name,from_name,from_email,created_at) VALUES(1,'one','One','one@example.org',0),(2,'two','Two','two@example.org',0)`,
		`INSERT INTO suppressions(id,brand_id,kind,value,reason,created_at) VALUES(1,1,'email','first@example.org','hard_bounce',123),(2,1,'email','taken@example.org','manual',124),(3,NULL,'email','global@example.org','complaint',125),(4,2,'email','other@example.org','manual',126)`,
	} {
		if _, err := db.Writer.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/suppressions/1?brand_id=1"
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"value":"example.org"}`, 400},
		{`{"value":"Person <person@example.org>"}`, 400},
		{`{"value":"person@localhost"}`, 400},
		{`{"kind":"domain","value":"person@example.org"}`, 400},
		{`{"kind":"invalid"}`, 400},
		{`{"reason":"unsupported"}`, 400},
		{`{"brand_id":2,"id":4}`, 400},
		{`{"value":" TAKEN@EXAMPLE.ORG "}`, 409},
	} {
		w := request(mux, "PATCH", path, tc.body, &cookie, csrf)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
		var value, reason string
		if err := db.Reader.QueryRow(`SELECT value,reason FROM suppressions WHERE id=1`).Scan(&value, &reason); err != nil || value != "first@example.org" || reason != "hard_bounce" {
			t.Fatalf("failed edit changed row: %s %s %v", value, reason, err)
		}
	}
	w := request(mux, "PATCH", path, `{"value":" Person@EXAMPLE.ORG "}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["value"] != "person@example.org" || row["reason"] != "hard_bounce" {
		t.Fatal(row)
	}
	w = request(mux, "PATCH", path, `{"kind":"domain","value":" BÜCHER.example ","reason":"manual"}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var kind, value, reason string
	var brand, created int64
	if err := db.Reader.QueryRow(`SELECT brand_id,kind,value,reason,created_at FROM suppressions WHERE id=1`).Scan(&brand, &kind, &value, &reason, &created); err != nil || brand != 1 || kind != "domain" || value != "xn--bcher-kva.example" || reason != "manual" || created != 123 {
		t.Fatal(brand, kind, value, reason, created, err)
	}
	for _, path := range []string{"/api/v1/suppressions/3?brand_id=1", "/api/v1/suppressions/4?brand_id=1", "/api/v1/suppressions/1?scope=global", "/api/v1/suppressions/999?scope=global"} {
		if w := request(mux, "PATCH", path, `{"reason":"manual"}`, &cookie, csrf); w.Code != 404 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w = request(mux, "PATCH", "/api/v1/suppressions/3?scope=global", `{"reason":"ses_sync"}`, &cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"kind":"email","value":"example.net"}`, 400},
		{`{"kind":"domain","value":"person@example.net"}`, 400},
		{`{"kind":"domain","value":"example.net"}`, 200},
		{`{"kind":"email","value":"person@example.net"}`, 200},
	} {
		w := request(mux, "POST", "/api/v1/suppressions?brand_id=1", tc.body, &cookie, csrf)
		if w.Code != tc.status {
			t.Fatal(tc.body, w.Code, w.Body.String())
		}
	}
}
