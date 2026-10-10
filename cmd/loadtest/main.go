// loadtest exercises the complete durable pipeline through the SES v2 HTTP adapter.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"postbox/internal/send"
	"postbox/internal/store"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type Report struct {
	Recipients        int          `json:"recipients"`
	Rate              float64      `json:"configured_rate"`
	AchievedRate      float64      `json:"achieved_rate"`
	DurationSeconds   float64      `json:"duration_seconds"`
	TemplateBytes     int          `json:"template_bytes"`
	PeakHeapBytes     uint64       `json:"peak_heap_bytes"`
	PeakRSSBytes      int64        `json:"peak_rss_bytes"`
	CPUSeconds        float64      `json:"cpu_seconds"`
	CPUPercent        float64      `json:"cpu_percent"`
	Accepted          int64        `json:"accepted"`
	Duplicates        int          `json:"duplicates"`
	MaxSecondAccepted int          `json:"max_second_accepted"`
	WithinTwoPercent  bool         `json:"within_two_percent"`
	Database          string       `json:"database"`
	Metrics           send.Metrics `json:"metrics"`
}

func seed(ctx context.Context, db *store.DB, n, size int) error {
	at := time.Now().UnixMilli()
	return db.Immediate(ctx, func(c *sql.Conn) error {
		for _, q := range []string{`INSERT INTO brands(id,name,from_name,from_email,tracking_domain,created_at) VALUES(1,'Load test','Postbox','sender@example.org','tracking.example.org',?)`, `INSERT INTO lists(id,brand_id,uuid,name,opt_in,created_at) VALUES(1,1,'load-list','Load recipients','single',?)`} {
			if _, err := c.ExecContext(ctx, q, at); err != nil {
				return err
			}
		}
		html := `<html><body><p>Hello [Name,fallback=there]</p><a href="https://example.org/news">News</a><p>` + strings.Repeat("Realistic newsletter content with useful information. ", max(1, (size-200)/54)) + `</p><a href="[unsubscribe]">Unsubscribe</a></body></html>`
		if _, err := c.ExecContext(ctx, `INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,inline_css,created_at,updated_at) VALUES(1,1,'load-campaign','draft','Newsletter for [Name]',?,0,?,?)`, html, at, at); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO campaign_targets(campaign_id,target_type,target_id,mode) VALUES(1,'list',1,'include')`); err != nil {
			return err
		}
		sub, err := c.PrepareContext(ctx, `INSERT INTO subscribers(id,brand_id,email,name,fields,created_at,updated_at) VALUES(?,1,?,?,'{"country":"NL","interest":"news"}',?,?)`)
		if err != nil {
			return err
		}
		defer sub.Close()
		list, err := c.PrepareContext(ctx, `INSERT INTO list_subscribers(list_id,subscriber_id,status,created_at,updated_at) VALUES(1,?,'subscribed',?,?)`)
		if err != nil {
			return err
		}
		defer list.Close()
		for i := 1; i <= n; i++ {
			if _, err = sub.ExecContext(ctx, i, fmt.Sprintf("reader%d@example.org", i), fmt.Sprintf("Reader %d", i), at, at); err != nil {
				return err
			}
			if _, err = list.ExecContext(ctx, i, at, at); err != nil {
				return err
			}
		}
		return nil
	})
}
func cpuUsage() (float64, int64) {
	var r syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &r) != nil {
		return 0, 0
	}
	seconds := float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	rss := r.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	return seconds, rss
}
func main() {
	n := flag.Int("recipients", 350000, "recipient count")
	rate := flag.Float64("rate", 175, "SES account quota messages/s")
	size := flag.Int("message-bytes", 100000, "approximate HTML template bytes")
	latency := flag.Duration("latency", 100*time.Millisecond, "base SES latency")
	jitter := flag.Duration("jitter", 50*time.Millisecond, "additional uniform latency")
	path := flag.String("db", "", "SQLite path, new temporary file by default")
	output := flag.String("output", "", "optional JSON report path")
	maxWorkers := flag.Int("workers", 1024, "concurrency cap")
	cpuProfile := flag.String("cpu-profile", "", "optional send-phase CPU profile path")
	heapProfile := flag.String("heap-profile", "", "optional completed live heap profile path")
	flag.Parse()
	if *n < 1 || *rate <= 0 {
		log.Fatal("recipients and rate must be positive")
	}
	if *path == "" {
		f, err := os.CreateTemp("", "postbox-load-*.db")
		if err != nil {
			log.Fatal(err)
		}
		*path = f.Name()
		f.Close()
	}
	db, err := store.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = seed(ctx, db, *n, *size); err != nil {
		log.Fatal(err)
	}
	fake := send.NewFake(send.FakeConfig{Rate: *rate, Latency: *latency, Jitter: *jitter})
	server := httptest.NewServer(fake.Handler())
	defer server.Close()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = *maxWorkers * 2
	tr.MaxIdleConnsPerHost = *maxWorkers
	tr.MaxConnsPerHost = *maxWorkers
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("fake", "fake", ""), HTTPClient: &http.Client{Transport: tr, Timeout: 30 * time.Second}, Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 1; o.RateLimiter = ratelimit.None })
	}}
	sender := &send.AWS{Client: sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = aws.String(server.URL) })}
	engine := send.New(db, sender, send.Config{MaxWorkers: *maxWorkers, TokenSecret: []byte("loadtest-secret-key-at-least-32bytes"), BaseURL: "https://tracking.example.org"})
	var peak atomic.Uint64
	sampleCtx, stopSample := context.WithCancel(context.Background())
	defer stopSample()
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-t.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				m := engine.Metrics()
				log.Printf("progress sent=%d/%d in_flight=%d quota=%.1f achieved=%.1f writer_p99=%.2fms", m.Sent, *n, m.InFlight, m.EffectiveRate, m.AchievedRate, m.WriterP99Milliseconds)
			}
		}
	}()

	before, _ := cpuUsage()
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		if err = pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer f.Close()
	}
	start := time.Now()
	if err = engine.RunCampaign(ctx, 1); err != nil {
		log.Fatal(err)
	}
	duration := time.Since(start).Seconds()
	stopSample()
	after, rss := cpuUsage()
	if *cpuProfile != "" {
		pprof.StopCPUProfile()
	}
	if *heapProfile != "" {
		f, err := os.Create(*heapProfile)
		if err != nil {
			log.Fatal(err)
		}
		runtime.GC()
		if err = pprof.WriteHeapProfile(f); err != nil {
			log.Fatal(err)
		}
		if err = f.Close(); err != nil {
			log.Fatal(err)
		}
	}
	m := engine.Metrics()
	duplicates := 0
	for _, count := range fake.Recipients {
		if count > 1 {
			duplicates += count - 1
		}
	}
	achieved := float64(m.Sent) / duration
	report := Report{*n, *rate, achieved, duration, *size, peak.Load(), rss, after - before, (after - before) / duration * 100, fake.Accepted.Load(), duplicates, fake.MaxWindow, achieved >= *rate*.98 && achieved <= *rate*1.02, *path, m}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	if *output != "" {
		if err = os.WriteFile(*output, append(data, '\n'), 0600); err != nil {
			log.Fatal(err)
		}
	}
	if ctx.Err() != nil || !report.WithinTwoPercent || duplicates > 0 || report.Accepted != int64(*n) || report.Metrics.Sent != int64(*n) {
		os.Exit(1)
	}
}
