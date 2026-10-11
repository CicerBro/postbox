// Package httplog logs failed requests without recording credentials or signed URLs.
package httplog

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	status int
	err    error
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		w.err = err
	}
	return n, err
}

func (w *responseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.err = err
	}
	return err
}

func (w *responseWriter) Flush() { _ = w.FlushError() }

// RecordError attaches a server failure to the request's final log entry.
func RecordError(w http.ResponseWriter, err error) {
	if recorder, ok := w.(*responseWriter); ok {
		recorder.err = err
	}
}

// Middleware logs client failures at WARN, server failures at ERROR, and
// successful requests at DEBUG. Route patterns avoid exposing path tokens,
// email addresses, query parameters, headers, or request/response bodies.
func Middleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &responseWriter{ResponseWriter: w}
		defer func() {
			p := recover()
			if p == http.ErrAbortHandler {
				panic(p)
			}
			status := writer.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelDebug
			if status >= 500 || writer.err != nil || p != nil {
				level = slog.LevelError
			} else if status >= 400 {
				level = slog.LevelWarn
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			attrs := []any{"method", r.Method, "route", route, "status", status, "elapsed_ms", time.Since(start).Milliseconds()}
			if writer.err != nil {
				attrs = append(attrs, "error", writer.err)
			}
			if p != nil {
				// Keep net/http's panic handling and avoid logging arbitrary panic values.
				attrs = append(attrs, "panicked", true, "stack", string(debug.Stack()))
			}
			logger.Log(r.Context(), level, "http request", attrs...)
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(writer, r)
	})
}
