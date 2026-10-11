package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestHandlerFailuresLoggedWithRetryOutcome(t *testing.T) {
	for _, tc := range []struct {
		queue, level, status string
		maxAttempts          int
		panics               bool
	}{
		{"test", "WARN", "pending", 3, false},
		{"test", "ERROR", "failed", 1, false},
		{"transactional_email", "ERROR", "failed", 3, false},
		{"test", "WARN", "pending", 3, true},
	} {
		t.Run(tc.queue+tc.status+tc.level, func(t *testing.T) {
			r := testRunner(t)
			var logs bytes.Buffer
			r.cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			r.cfg.MaxAttempts = tc.maxAttempts
			err := r.Register(tc.queue, func(context.Context, Job) error {
				if tc.panics {
					panic("private panic value")
				}
				return errors.New("upstream unavailable")
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			id, err := r.Enqueue(ctx, tc.queue, map[string]string{"secret": "private payload"}, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			j, h, err := r.claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r.execute(ctx, j, h)
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			wantError := "upstream unavailable"
			if tc.panics {
				wantError = "job handler panicked"
			}
			if entry["job_id"] != float64(id) || entry["queue"] != tc.queue || entry["attempt"] != float64(1) || entry["level"] != tc.level || entry["status"] != tc.status || entry["error"] != wantError {
				t.Fatalf("incorrect failure log: %v", entry)
			}
			if strings.Contains(logs.String(), "private") {
				t.Fatalf("payload or panic value leaked: %s", logs.String())
			}
			var status string
			if err := r.db.Reader.QueryRowContext(ctx, "SELECT status FROM jobs WHERE id=?", id).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.status {
				t.Fatalf("persisted status = %s, logged %s", status, tc.status)
			}
		})
	}
}
