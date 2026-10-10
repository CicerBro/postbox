package send

import (
	"fmt"
	"io"
	"sort"
)

// WritePrometheus exports sending metrics without holding the engine lock while writing.
func (e *Engine) WritePrometheus(w io.Writer) {
	m := e.Metrics()
	for _, v := range []struct {
		name  string
		value any
	}{{"postbox_send_configured_rate", m.ConfiguredRate}, {"postbox_send_effective_rate", m.EffectiveRate}, {"postbox_send_achieved_rate", m.AchievedRate}, {"postbox_send_in_flight", m.InFlight}, {"postbox_send_claimer_backlog", m.Backlog}, {"postbox_send_requests_total", m.Requests}, {"postbox_send_sent_total", m.Sent}, {"postbox_send_failed_total", m.Failed}, {"postbox_send_unknown_total", m.Unknown}, {"postbox_send_writer_batches_total", m.WriterBatches}, {"postbox_send_writer_rows_total", m.WriterRows}, {"postbox_send_latency_p50_seconds", m.P50Milliseconds / 1000}, {"postbox_send_latency_p99_seconds", m.P99Milliseconds / 1000}, {"postbox_send_writer_latency_p99_seconds", m.WriterP99Milliseconds / 1000}} {
		fmt.Fprintf(w, "%s %v\n", v.name, v.value)
	}
	keys := make([]string, 0, len(m.Errors))
	for k := range m.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "postbox_send_errors_total{class=%q} %d\n", k, m.Errors[k])
	}
}
