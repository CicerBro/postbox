package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"postbox/internal/jobs"
)

func (a *API) registerJobSettings(m *http.ServeMux) {
	for _, method := range []string{"GET", "PATCH"} {
		a.route(m, method+" /api/v1/job-settings", a.jobSettings)
	}
}

func (a *API) jobSettings(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	if r.Method == http.MethodPatch {
		var in map[string]json.RawMessage
		if !decode(w, r, &in) {
			return
		}
		var days int
		raw, ok := in[jobs.FailedJobRetentionKey]
		if !ok || len(in) != 1 || json.Unmarshal(raw, &days) != nil || days < 1 || days > jobs.MaxFailedJobRetentionDays {
			fail(w, http.StatusBadRequest, errors.New("failed_job_retention_days must be an integer between 1 and 3650 and the only setting"))
			return
		}
		if err := a.db.SetSetting(r.Context(), jobs.FailedJobRetentionKey, strconv.Itoa(days)); err != nil {
			result(w, nil, err)
			return
		}
	}
	days, err := jobs.FailedJobRetentionDays(r.Context(), a.db)
	result(w, map[string]int{jobs.FailedJobRetentionKey: days}, err)
}

func documentJobSettings(method, path string, op map[string]any) {
	if path != "/api/v1/job-settings" {
		return
	}
	property := map[string]any{"type": "integer", "minimum": 1, "maximum": jobs.MaxFailedJobRetentionDays, "default": jobs.DefaultFailedJobRetentionDays}
	schema := map[string]any{"type": "object", "required": []string{jobs.FailedJobRetentionKey}, "additionalProperties": false, "properties": map[string]any{jobs.FailedJobRetentionKey: property}}
	op["description"] = "Administrator session required. Read or change the application-wide retention period for failed background jobs. Defaults to 30 days from terminal failure. Hourly housekeeping deletes expired failures in bounded batches; saved changes apply on its next run without a restart. Pending, running and successful jobs are preserved. Existing failures receive their initial failure timestamp on upgrade."
	op["responses"].(map[string]any)["200"] = map[string]any{"description": "Current job settings", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	if method == http.MethodPatch {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
}
