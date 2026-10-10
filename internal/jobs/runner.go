// Package jobs implements a SQLite backed leased job runner.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"postbox/internal/store"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	ID          int64
	Queue       string
	Payload     json.RawMessage
	Attempts    int
	LockedUntil int64
}
type Handler func(context.Context, Job) error
type Config struct {
	Workers, MaxAttempts                     int
	PollInterval, Lease, RetryBase, RetryMax time.Duration
	Logger                                   *slog.Logger
}
type Metrics struct{ Claimed, Completed, Retried, Failed, Recovered, InFlight int64 }
type Runner struct {
	db                                                       *store.DB
	cfg                                                      Config
	mu                                                       sync.RWMutex
	handlers                                                 map[string]Handler
	running                                                  atomic.Bool
	claimed, completed, retried, failed, recovered, inflight atomic.Int64
}

func New(db *store.DB, cfg Config) *Runner {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.Lease < 300*time.Millisecond {
		cfg.Lease = time.Minute
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = time.Second
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	r := &Runner{db: db, cfg: cfg, handlers: map[string]Handler{}}
	_ = r.Register("outbound_webhook", r.deliverWebhook)
	return r
}
func (r *Runner) Register(queue string, h Handler) error {
	if queue == "" || h == nil {
		return errors.New("job queue and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handlers[queue]; ok {
		return fmt.Errorf("job handler %q already registered", queue)
	}
	r.handlers[queue] = h
	return nil
}
func (r *Runner) RegisterCSV(importHandler, exportHandler Handler) error {
	if importHandler != nil {
		if err := r.Register("csv_import", importHandler); err != nil {
			return err
		}
	}
	if exportHandler != nil {
		return r.Register("csv_export", exportHandler)
	}
	return nil
}
func (r *Runner) Enqueue(ctx context.Context, queue string, payload any, runAt time.Time) (int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	if runAt.IsZero() {
		runAt = time.Now()
	}
	var id int64
	err = r.db.Immediate(ctx, func(c *sql.Conn) error {
		return c.QueryRowContext(ctx, "INSERT INTO jobs(queue,payload,status,run_at,created_at) VALUES(?,?,'pending',?,?) RETURNING id", queue, string(data), runAt.UnixMilli(), time.Now().UnixMilli()).Scan(&id)
	})
	return id, err
}
func (r *Runner) Metrics() Metrics {
	return Metrics{r.claimed.Load(), r.completed.Load(), r.retried.Load(), r.failed.Load(), r.recovered.Load(), r.inflight.Load()}
}
func (r *Runner) Recover(ctx context.Context) error {
	return r.db.Immediate(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, "UPDATE jobs SET status='pending',locked_until=NULL WHERE status='running' AND (locked_until IS NULL OR locked_until<=?)", time.Now().UnixMilli())
		if err == nil {
			n, _ := res.RowsAffected()
			r.recovered.Add(n)
		}
		return err
	})
}
func (r *Runner) claim(ctx context.Context) (Job, Handler, error) {
	r.mu.RLock()
	qs := make([]string, 0, len(r.handlers))
	for q := range r.handlers {
		qs = append(qs, q)
	}
	sort.Strings(qs)
	r.mu.RUnlock()
	if len(qs) > 1 {
		off := rand.IntN(len(qs))
		qs = append(qs[off:], qs[:off]...)
	}
	for _, q := range qs {
		var j Job
		var payload string
		err := r.db.Immediate(ctx, func(c *sql.Conn) error {
			return c.QueryRowContext(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,locked_until=? WHERE id=(SELECT id FROM jobs WHERE queue=? AND status='pending' AND run_at<=? ORDER BY run_at,id LIMIT 1) AND status='pending' RETURNING id,queue,payload,attempts,locked_until`, time.Now().Add(r.cfg.Lease).UnixMilli(), q, time.Now().UnixMilli()).Scan(&j.ID, &j.Queue, &payload, &j.Attempts, &j.LockedUntil)
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Job{}, nil, err
		}
		r.mu.RLock()
		h := r.handlers[q]
		r.mu.RUnlock()
		j.Payload = json.RawMessage(payload)
		r.claimed.Add(1)
		return j, h, nil
	}
	return Job{}, nil, sql.ErrNoRows
}
func (r *Runner) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return errors.New("job runner already running")
	}
	defer r.running.Store(false)
	if err := r.Recover(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for i := 0; i < r.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timer := time.NewTicker(r.cfg.PollInterval)
			defer timer.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				j, h, err := r.claim(ctx)
				if err == nil {
					r.execute(ctx, j, h)
					continue
				}
				if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
					r.cfg.Logger.Error("claim job failed", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					if err := r.Recover(ctx); err != nil && ctx.Err() == nil {
						r.cfg.Logger.Error("recover jobs failed", "error", err)
					}
				}
			}
		}()
	}
	wg.Wait()
	return nil
}
func (r *Runner) execute(parent context.Context, j Job, h Handler) {
	r.inflight.Add(1)
	defer r.inflight.Add(-1)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	var lease atomic.Int64
	lease.Store(j.LockedUntil)
	go func() {
		defer close(done)
		t := time.NewTicker(r.cfg.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				old := lease.Load()
				next := time.Now().Add(r.cfg.Lease).UnixMilli()
				err := r.db.Immediate(ctx, func(c *sql.Conn) error {
					res, e := c.ExecContext(ctx, "UPDATE jobs SET locked_until=? WHERE id=? AND status='running' AND locked_until=?", next, j.ID, old)
					if e != nil {
						return e
					}
					n, e := res.RowsAffected()
					if n != 1 {
						return errors.New("job lease lost")
					}
					return e
				})
				if err != nil {
					cancel()
					return
				}
				lease.Store(next)
			}
		}
	}()
	err := callHandler(ctx, h, j)
	close(stop)
	<-done
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	status := "done"
	runAt := time.Now().UnixMilli()
	var last any
	if err != nil {
		last = safeError(err)
		status = "pending"
		if j.Attempts >= r.cfg.MaxAttempts {
			status = "failed"
		}
		runAt = time.Now().Add(r.backoff(j.Attempts)).UnixMilli()
	}
	finishErr := r.db.Immediate(finishCtx, func(c *sql.Conn) error {
		res, e := c.ExecContext(finishCtx, "UPDATE jobs SET status=?,run_at=?,locked_until=NULL,last_error=? WHERE id=? AND status='running' AND locked_until=?", status, runAt, last, j.ID, lease.Load())
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if n != 1 {
			return errors.New("job lease lost before completion")
		}
		return e
	})
	if finishErr != nil {
		r.cfg.Logger.Error("finish job failed", "job_id", j.ID, "error", finishErr)
		return
	}
	switch status {
	case "done":
		r.completed.Add(1)
	case "pending":
		r.retried.Add(1)
	case "failed":
		r.failed.Add(1)
	}
}
func callHandler(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("job handler panicked")
		}
	}()
	return h(ctx, j)
}
func safeError(err error) string {
	s := err.Error()
	if len(s) > 1000 {
		s = s[:1000]
	}
	return s
}
func (r *Runner) backoff(attempt int) time.Duration {
	d := r.cfg.RetryBase
	for i := 1; i < attempt && d < r.cfg.RetryMax; i++ {
		if d > r.cfg.RetryMax/2 {
			d = r.cfg.RetryMax
			break
		}
		d *= 2
	}
	if d > r.cfg.RetryMax {
		d = r.cfg.RetryMax
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// RegisterNotifications installs delivery of campaign start/finish email notifications.
// The callback owns recipient configuration and mail delivery; retries are durable.
func (r *Runner) RegisterNotifications(h Handler) error { return r.Register("email_notification", h) }

type Failure struct {
	ID        int64  `json:"id"`
	Queue     string `json:"queue"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
	CreatedAt int64  `json:"created_at"`
}

// Failures uses keyset pagination and never returns payloads or webhook secrets.
func (r *Runner) Failures(ctx context.Context, afterID int64, limit int) ([]Failure, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := r.db.Reader.QueryContext(ctx, "SELECT id,queue,attempts,COALESCE(last_error,''),created_at FROM jobs WHERE status='failed' AND id>? ORDER BY id LIMIT ?", afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Failure{}
	for rows.Next() {
		var f Failure
		if err := rows.Scan(&f.ID, &f.Queue, &f.Attempts, &f.Error, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
