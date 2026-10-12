package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"postbox/internal/jobs"
)

func TestJobSettingsPersistGloballyAndValidate(t *testing.T) {
	_, mux, db, admin, csrf := setup(t)
	const path = "/api/v1/job-settings"
	w := request(mux, "GET", path, "", &admin, csrf)
	if w.Code != 200 || w.Body.String() != "{\"failed_job_retention_days\":30}\n" {
		t.Fatalf("default: %d %s", w.Code, w.Body.String())
	}
	w = request(mux, "PATCH", path+"?brand_id=123", `{"failed_job_retention_days":14}`, &admin, csrf)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"failed_job_retention_days":0}`, `{"failed_job_retention_days":-1}`, `{"failed_job_retention_days":3651}`, `{"failed_job_retention_days":1.5}`, `{"failed_job_retention_days":"7"}`, `{"failed_job_retention_days":null}`, `{"failed_job_retention_days":7,"other":true}`} {
		w = request(mux, "PATCH", path, body, &admin, csrf)
		if w.Code != 400 {
			t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	days, err := jobs.FailedJobRetentionDays(context.Background(), db)
	if err != nil || days != 14 {
		t.Fatalf("persisted days=%d error=%v", days, err)
	}
	w = request(mux, "PATCH", path, `{"failed_job_retention_days":1}`, &admin, "")
	if w.Code != 403 {
		t.Fatalf("missing CSRF: %d %s", w.Code, w.Body.String())
	}
	w = request(mux, "GET", path, "", nil, "")
	if w.Code != 401 {
		t.Fatalf("unauthenticated: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Writer.Exec(`UPDATE users SET role='brand_user' WHERE email='admin@example.org'`); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PATCH"} {
		w = request(mux, method, path, `{"failed_job_retention_days":1}`, &admin, csrf)
		if w.Code != 403 {
			t.Fatalf("member %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}

func TestJobSettingsOpenAPIDocumentsPolicy(t *testing.T) {
	_, mux, _, admin, csrf := setup(t)
	w := request(mux, "GET", "/api/v1/openapi.json", "", &admin, csrf)
	var spec struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	patch := spec.Paths["/api/v1/job-settings"]["patch"]
	if patch["requestBody"] == nil || patch["description"] == nil {
		t.Fatal("job retention API documentation missing")
	}
}
