package main

import (
	"flag"
	"log"
	"net/http"
	"postbox/internal/send"
	"time"
)

func main() {
	addr := flag.String("listen", ":9001", "HTTP listen address")
	rate := flag.Float64("rate", 175, "account max send rate")
	latency := flag.Duration("latency", 100*time.Millisecond, "base request latency")
	jitter := flag.Duration("jitter", 50*time.Millisecond, "uniform additional latency")
	reject := flag.Int("reject-every", 0, "reject each Nth call")
	throttle := flag.Int("throttle-every", 0, "throttle each Nth call")
	server := flag.Int("server-error-every", 0, "500 each Nth call")
	timeout := flag.Int("timeout-every", 0, "accept then time out each Nth call")
	flag.Parse()
	f := send.NewFake(send.FakeConfig{Rate: *rate, Latency: *latency, Jitter: *jitter, RejectEvery: *reject, ThrottleEvery: *throttle, ServerErrorEvery: *server, TimeoutEvery: *timeout})
	log.Printf("fake SES listening on %s, quota %.0f/s", *addr, *rate)
	log.Fatal(http.ListenAndServe(*addr, f.Handler()))
}
