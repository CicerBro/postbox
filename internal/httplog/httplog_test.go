package httplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogging(t *testing.T) {
	for _, status := range []int{200, 400, 401, 404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var logs bytes.Buffer
			mux := http.NewServeMux()
			mux.HandleFunc("GET /test/{token}", func(w http.ResponseWriter, r *http.Request) {
				if status >= 500 {
					RecordError(w, errors.New("database unavailable"))
				}
				w.WriteHeader(status)
				w.WriteHeader(202) // A second final status must not replace the first.
				w.Write([]byte("private response"))
			})
			req := httptest.NewRequest("GET", "/test/private-token?secret=private-query", nil)
			req.Header.Set("Authorization", "Bearer private-header")
			rec := httptest.NewRecorder()
			Middleware(mux, slog.New(slog.NewJSONHandler(&logs, nil))).ServeHTTP(rec, req)
			if rec.Code != status {
				t.Fatalf("response status = %d, want %d", rec.Code, status)
			}
			if status == 200 {
				if logs.Len() != 0 {
					t.Fatal("successful request logged at default level")
				}
				return
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			level := "WARN"
			if status >= 500 {
				level = "ERROR"
				if entry["error"] != "database unavailable" {
					t.Fatalf("missing cause: %v", entry)
				}
			}
			if entry["level"] != level || entry["status"] != float64(status) || entry["route"] != "GET /test/{token}" {
				t.Fatalf("unexpected log: %v", entry)
			}
			if strings.Contains(logs.String(), "private") {
				t.Fatalf("request or response secrets logged: %s", logs.String())
			}
		})
	}
}

func TestPanicLoggingPreservesHTTPRecovery(t *testing.T) {
	for _, abort := range []bool{false, true} {
		var logs bytes.Buffer
		value := any("private panic value")
		if abort {
			value = http.ErrAbortHandler
		}
		h := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(value) }), slog.New(slog.NewJSONHandler(&logs, nil)))
		func() {
			defer func() {
				if got := recover(); got != value {
					t.Errorf("panic = %v, want %v", got, value)
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/private-token", nil))
		}()
		if abort {
			if logs.Len() != 0 {
				t.Fatal("intentional abort logged as panic")
			}
		} else if !strings.Contains(logs.String(), `"panicked":true`) || !strings.Contains(logs.String(), `"stack":`) || strings.Contains(logs.String(), "private") {
			t.Fatalf("unexpected panic log: %s", logs.String())
		}
	}
}

func TestResponseControllerFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	var logs bytes.Buffer
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("streaming response lost http.Flusher")
		}
		w.WriteHeader(103)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	}), slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !rec.Flushed || !strings.Contains(logs.String(), `"status":200`) {
		t.Fatalf("flush lost or incorrect status: %s", logs.String())
	}
}
