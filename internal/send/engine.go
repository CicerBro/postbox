package send

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
	"postbox/internal/render"
	"postbox/internal/store"
	"postbox/internal/subscribers"
)

type Config struct {
	BrandSenderProvider                 BrandSenderProvider
	EventHook                           func(context.Context, *sql.Conn, int64, string, any) error
	MaxWorkers, ClaimBatch, ResultBatch int
	FlushInterval, QuotaRefresh         time.Duration
	RecoveryPolicy, BaseURL             string
	TokenSecret                         []byte
	SoftBounceLimit                     int
}
type Estimate struct {
	Recipients      int64   `json:"recipients"`
	MessageBytes    int64   `json:"message_bytes"`
	UploadBytes     int64   `json:"upload_bytes"`
	DurationSeconds float64 `json:"duration_seconds"`
	Quota           Quota   `json:"quota"`
	FitsDailyQuota  bool    `json:"fits_daily_quota"`
}
type Metrics struct {
	AchievedRate           float64          `json:"achieved_rate"`
	RequestDurationSeconds float64          `json:"request_duration_seconds"`
	ConfiguredRate         float64          `json:"configured_rate"`
	EffectiveRate          float64          `json:"effective_rate"`
	Sent                   int64            `json:"sent"`
	Failed                 int64            `json:"failed"`
	Unknown                int64            `json:"unknown"`
	InFlight               int64            `json:"in_flight"`
	Backlog                int64            `json:"backlog"`
	Requests               int64            `json:"requests"`
	P50Milliseconds        float64          `json:"p50_ms"`
	P99Milliseconds        float64          `json:"p99_ms"`
	WriterP99Milliseconds  float64          `json:"writer_p99_ms"`
	WriterBatches          int64            `json:"writer_batches"`
	WriterRows             int64            `json:"writer_rows"`
	Errors                 map[string]int64 `json:"errors"`
}
type Engine struct {
	latencyBuckets, writerBuckets                                                 [30001]uint64
	latencyCount, writerCount                                                     uint64
	firstRequestAt, lastRequestAt                                                 atomic.Int64
	metricsOrigin                                                                 time.Time
	db                                                                            *store.DB
	scopes                                                                        map[string]*accountPacer
	sender                                                                        Sender
	cfg                                                                           Config
	mu                                                                            sync.Mutex
	running                                                                       map[int64]context.CancelFunc
	limiter                                                                       *rate.Limiter
	configured, effective                                                         float64
	latencies, writerLatencies                                                    []float64
	latencyPos, writerPos                                                         int
	errorCounts                                                                   map[string]int64
	sent, failed, unknown, inflight, backlog, requests, writerBatches, writerRows atomic.Int64
	enabled                                                                       atomic.Bool
	target                                                                        atomic.Int64
	runMu                                                                         sync.Mutex
	paceMu                                                                        sync.Mutex
}

func New(db *store.DB, sender Sender, cfg Config) *Engine {
	if cfg.MaxWorkers < 1 {
		cfg.MaxWorkers = 1024
	}
	if cfg.ClaimBatch < 1 {
		cfg.ClaimBatch = 200
	}
	if cfg.ResultBatch < 1 {
		cfg.ResultBatch = 500
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 250 * time.Millisecond
	}
	if cfg.QuotaRefresh <= 0 {
		cfg.QuotaRefresh = 3 * time.Minute
	}
	if cfg.RecoveryPolicy == "" {
		cfg.RecoveryPolicy = "unknown"
	}
	e := &Engine{scopes: map[string]*accountPacer{}, db: db, sender: sender, cfg: cfg, running: map[int64]context.CancelFunc{}, limiter: rate.NewLimiter(1, 1), errorCounts: map[string]int64{}, metricsOrigin: time.Now()}
	e.enabled.Store(true)
	return e
}
func (e *Engine) Metrics() Metrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	errorsCopy := map[string]int64{}
	for k, v := range e.errorCounts {
		errorsCopy[k] = v
	}
	duration := float64(e.lastRequestAt.Load()-e.firstRequestAt.Load()) / 1e9
	if duration < 0 || e.firstRequestAt.Load() == 0 {
		duration = 0
	}
	if e.configured > 0 {
		duration += 1 / e.configured
	}
	achieved := float64(0)
	if duration > 0 {
		achieved = float64(e.requests.Load()) / duration
	}
	return Metrics{achieved, duration, e.configured, e.effective, e.sent.Load(), e.failed.Load(), e.unknown.Load(), e.inflight.Load(), e.backlog.Load(), e.requests.Load(), histPercentile(&e.latencyBuckets, e.latencyCount, .5), histPercentile(&e.latencyBuckets, e.latencyCount, .99), histPercentile(&e.writerBuckets, e.writerCount, .99), e.writerBatches.Load(), e.writerRows.Load(), errorsCopy}
}
func percentile(a []float64, p float64) float64 {
	if len(a) == 0 {
		return 0
	}
	v := append([]float64(nil), a...)
	slicesSort(v)
	return v[int(float64(len(v)-1)*p)]
}
func slicesSort(v []float64) { slices.Sort(v) }

func (e *Engine) observe(d time.Duration, writer bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := float64(d) / float64(time.Millisecond)
	bucket := min(30000, max(0, int(math.Ceil(v))))
	if writer {
		e.writerBuckets[bucket]++
		e.writerCount++
	} else {
		e.latencyBuckets[bucket]++
		e.latencyCount++
	}
	if writer {
		if len(e.writerLatencies) < 2048 {
			e.writerLatencies = append(e.writerLatencies, v)
		} else {
			e.writerLatencies[e.writerPos%2048] = v
			e.writerPos++
		}
	} else {
		if len(e.latencies) < 2048 {
			e.latencies = append(e.latencies, v)
			if len(e.latencies)%32 == 1 {
				e.updateTargetLocked()
			}
		} else {
			e.latencies[e.latencyPos%2048] = v
			e.latencyPos++
			if e.latencyPos%32 == 0 {
				e.updateTargetLocked()
			}
		}
	}
}
func (e *Engine) setQuota(q Quota) error {
	if q.MaxSendRate <= 0 || math.IsNaN(q.MaxSendRate) || math.IsInf(q.MaxSendRate, 0) {
		return errors.New("SES send rate must be positive and finite")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	oldConfigured := e.configured
	e.configured = q.MaxSendRate
	if e.effective == 0 || e.effective == oldConfigured || e.effective > q.MaxSendRate {
		e.effective = q.MaxSendRate
	}
	e.limiter.SetBurst(quotaBurst(e.effective))
	e.limiter.SetLimit(rate.Limit(e.effective))
	e.updateTargetLocked()
	return nil
}
func (e *Engine) adjust(throttle bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if throttle {
		e.effective = math.Max(.1, e.effective*.7)
	} else {
		e.effective = math.Min(e.configured, e.effective+math.Max(.1, e.configured*.01))
	}
	e.limiter.SetBurst(quotaBurst(e.effective))
	e.limiter.SetLimit(rate.Limit(e.effective))
	e.updateTargetLocked()
}
func (e *Engine) workerTarget() int {
	n := int(e.target.Load())
	if n < 1 {
		return 1
	}
	return n
}

// quotaBurst retains a small allowance for scheduler delays without changing the
// account's token refill rate. A 20ms allowance retains the existing five-token
// minimum and is capped at one second of quota.
func quotaBurst(sendRate float64) int {
	return min(max(1, int(math.Floor(sendRate))), max(5, int(math.Ceil(sendRate*.020))))
}
func (e *Engine) updateTargetLocked() {
	p99 := percentile(e.latencies, .99) / 1000
	if p99 < .15 {
		p99 = .15
	}
	e.target.Store(int64(min(e.cfg.MaxWorkers, max(1, int(math.Ceil(e.effective*p99*1.25))))))
}

func (e *Engine) Recover(ctx context.Context) error {
	if err := e.ensureDispatchLedger(ctx); err != nil {
		return err
	}
	if e.cfg.RecoveryPolicy != "unknown" {
		return fmt.Errorf("invalid recovery policy %q", e.cfg.RecoveryPolicy)
	}
	return e.db.Immediate(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, `UPDATE campaign_recipients SET status=?,error='process stopped with an uncertain outcome' WHERE status='claimed'`, "unknown"); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `UPDATE send_attempts SET outcome='unknown',finished_at=?,error='process stopped with an uncertain outcome' WHERE outcome='started'`, time.Now().UnixMilli()); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `UPDATE campaign_stats SET unknown=(SELECT count(*) FROM campaign_recipients r WHERE r.campaign_id=campaign_stats.campaign_id AND r.status='unknown')`)
		return err
	})
}
func (e *Engine) Start(ctx context.Context) error {
	if err := e.Recover(ctx); err != nil {
		return err
	}
	var enabled string
	err := e.db.Reader.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='sending_enabled'`).Scan(&enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	e.enabled.Store(enabled != "false")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if e.enabled.Load() {
			var id int64
			err := e.db.Reader.QueryRowContext(ctx, `SELECT id FROM campaigns WHERE status IN ('sending','preparing') OR (status='scheduled' AND scheduled_at<=?) ORDER BY id LIMIT 1`, time.Now().UnixMilli()).Scan(&id)
			if err == nil {
				if err = e.RunCampaign(ctx, id); err != nil && ctx.Err() == nil {
					if pErr := e.Pause(context.Background(), id); pErr != nil {
						return errors.Join(err, pErr)
					}
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
func (e *Engine) control(ctx context.Context, id int64, state string) error {
	_, err := e.db.Writer.ExecContext(ctx, `UPDATE campaigns SET status=?,updated_at=? WHERE id=? AND status NOT IN ('sent','cancelled')`, state, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if stop := e.running[id]; stop != nil {
		stop()
	}
	e.mu.Unlock()
	return nil
}
func (e *Engine) Pause(ctx context.Context, id int64) error { return e.control(ctx, id, "paused") }
func (e *Engine) Cancel(ctx context.Context, id int64) error {
	return e.cancelCampaign(ctx, id)
}
func (e *Engine) Resume(ctx context.Context, id int64) error {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	if err := e.db.Immediate(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `UPDATE campaign_recipients SET status=?,error='writer interrupted with uncertain outcome' WHERE campaign_id=? AND status='claimed'`, "unknown", id); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `UPDATE send_attempts SET outcome='unknown',finished_at=? WHERE campaign_id=? AND outcome='started'`, time.Now().UnixMilli(), id); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, `UPDATE campaign_stats SET unknown=(SELECT count(*) FROM campaign_recipients WHERE campaign_id=? AND status='unknown') WHERE campaign_id=?`, id, id)
		return err
	}); err != nil {
		return err
	}

	if !e.enabled.Load() {
		return errors.New("global sending is disabled")
	}
	r, err := e.db.Writer.ExecContext(ctx, `UPDATE campaigns SET status='sending',updated_at=? WHERE id=? AND status='paused'`, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("campaign is not paused")
	}
	return nil
}
func (e *Engine) SetEnabled(ctx context.Context, on bool) error {
	if !on {
		e.enabled.Store(false)
		e.mu.Lock()
		for _, cancel := range e.running {
			cancel()
		}
		e.mu.Unlock()
	}
	value := "false"
	if on {
		value = "true"
	}
	if _, err := e.db.Writer.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('sending_enabled',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value); err != nil {
		return err
	}
	e.enabled.Store(on)
	if !on {
		e.mu.Lock()
		for _, cancel := range e.running {
			cancel()
		}
		e.mu.Unlock()
		_, err := e.db.Writer.ExecContext(ctx, `UPDATE campaigns SET status='paused',updated_at=? WHERE status IN ('sending','preparing')`, time.Now().UnixMilli())
		return err
	}
	return nil
}

type campaign struct {
	delivery                                                                          *brandDelivery
	audience                                                                          string
	audienceArgs                                                                      []any
	id, brand                                                                         int64
	subject, html, text, fromName, fromEmail, replyTo, configSet, trackingURL, status string
	inline                                                                            bool
	fields                                                                            []string
	prepared                                                                          *render.Prepared
	date                                                                              time.Time
}

func (e *Engine) prepare(ctx context.Context, id int64, persist bool) (campaign, error) {
	c := campaign{id: id}
	var inline int
	err := e.db.Reader.QueryRowContext(ctx, `SELECT c.brand_id,c.subject,c.html,coalesce(c.text,''),c.inline_css,c.status,b.from_name,b.from_email,coalesce(b.reply_to,''),coalesce(b.configuration_set,''),coalesce(b.tracking_domain,'') FROM campaigns c JOIN brands b ON b.id=c.brand_id WHERE c.id=?`, id).Scan(&c.brand, &c.subject, &c.html, &c.text, &inline, &c.status, &c.fromName, &c.fromEmail, &c.replyTo, &c.configSet, &c.trackingURL)
	if err != nil {
		return c, err
	}
	c.inline = inline != 0
	var fieldsJSON string
	var at int64
	err = e.db.Reader.QueryRowContext(ctx, `SELECT subject,html,text,from_name,from_email,reply_to,configuration_set,tracking_url,fields,prepared_at FROM campaign_preparations WHERE campaign_id=?`, id).Scan(&c.subject, &c.html, &c.text, &c.fromName, &c.fromEmail, &c.replyTo, &c.configSet, &c.trackingURL, &fieldsJSON, &at)
	if err == nil {
		if err = json.Unmarshal([]byte(fieldsJSON), &c.fields); err != nil {
			return c, err
		}
		rows, err := e.db.Reader.QueryContext(ctx, `SELECT id,url FROM links WHERE campaign_id=? ORDER BY id`, id)
		if err != nil {
			return c, err
		}
		var links []render.Link
		for rows.Next() {
			var l render.Link
			if err = rows.Scan(&l.ID, &l.URL); err != nil {
				rows.Close()
				return c, err
			}
			links = append(links, l)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return c, err
		}
		c.date = time.UnixMilli(at)
		c.prepared, err = render.Restore(c.subject, c.html, c.text, links, c.fields)
		return c, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	rows, err := e.db.Reader.QueryContext(ctx, `SELECT key FROM custom_fields WHERE brand_id=?`, c.brand)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return c, err
		}
		c.fields = append(c.fields, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	c.date = time.Now()
	if c.trackingURL == "" {
		c.trackingURL = e.cfg.BaseURL
	}
	if !strings.Contains(c.trackingURL, "://") {
		c.trackingURL = "https://" + c.trackingURL
	}
	c.trackingURL = strings.TrimRight(c.trackingURL, "/")
	if !persist {
		c.prepared, err = render.Prepare(render.Options{CampaignID: id, Subject: c.subject, HTML: c.html, Text: c.text, TrackingURL: c.trackingURL, InlineCSS: c.inline, Fields: c.fields, Now: c.date})
		return c, err
	}
	err = e.db.Immediate(ctx, func(conn *sql.Conn) error {
		p, err := render.Prepare(render.Options{CampaignID: id, Subject: c.subject, HTML: c.html, Text: c.text, TrackingURL: c.trackingURL, InlineCSS: c.inline, Fields: c.fields, Now: c.date, LinkID: func(url string) (int64, error) {
			var lid int64
			err := conn.QueryRowContext(ctx, `INSERT INTO links(campaign_id,url) VALUES(?,?) ON CONFLICT(campaign_id,url) DO UPDATE SET url=excluded.url RETURNING id`, id, url).Scan(&lid)
			return lid, err
		}})
		if err != nil {
			return err
		}
		c.prepared = p
		f, _ := json.Marshal(c.fields)
		_, err = conn.ExecContext(ctx, `INSERT INTO campaign_preparations(campaign_id,subject,html,text,from_name,from_email,reply_to,configuration_set,tracking_url,fields,prepared_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, p.Subject, p.HTML, p.Text, c.fromName, c.fromEmail, c.replyTo, c.configSet, c.trackingURL, string(f), c.date.UnixMilli())
		return err
	})
	return c, err
}
func (e *Engine) audienceSQL(ctx context.Context, c campaign) (string, []any, error) {
	rows, err := e.db.Reader.QueryContext(ctx, `SELECT target_type,target_id,mode FROM campaign_targets WHERE campaign_id=?`, c.id)
	if err != nil {
		return "", nil, err
	}
	var targets []AudienceTarget
	for rows.Next() {
		var t AudienceTarget
		if err = rows.Scan(&t.Type, &t.ID, &t.Mode); err != nil {
			rows.Close()
			return "", nil, err
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	return e.audienceTargetsSQL(ctx, c.brand, targets)
}

func (e *Engine) audienceTargetsSQL(ctx context.Context, brandID int64, targets []AudienceTarget) (string, []any, error) {
	var err error
	var includes, excludes []string
	var incArgs, excArgs []any
	for _, t := range targets {
		if t.ID <= 0 || (t.Type != "list" && t.Type != "segment") || (t.Mode != "include" && t.Mode != "exclude") {
			return "", nil, errors.New("invalid audience target")
		}
		predicate := ""
		var args []any
		if t.Type == "list" {
			var brand int64
			if err = e.db.Reader.QueryRowContext(ctx, `SELECT brand_id FROM lists WHERE id=?`, t.ID).Scan(&brand); err != nil {
				return "", nil, err
			}
			if brand != brandID {
				return "", nil, fmt.Errorf("list %d belongs to another brand", t.ID)
			}
			predicate = `EXISTS(SELECT 1 FROM list_subscribers ls WHERE ls.list_id=? AND ls.subscriber_id=s.id AND ls.status='subscribed')`
			args = []any{t.ID}
		} else {
			var rules string
			if err = e.db.Reader.QueryRowContext(ctx, `SELECT rules FROM segments WHERE id=? AND brand_id=?`, t.ID, brandID).Scan(&rules); err != nil {
				return "", nil, err
			}
			var rule subscribers.Rule
			if err = json.Unmarshal([]byte(rules), &rule); err != nil {
				return "", nil, err
			}
			if err = subscribers.New(e.db).ValidateSegment(ctx, brandID, rule); err != nil {
				return "", nil, err
			}
			predicate, args, err = subscribers.CompileSegment(rule)
			if err != nil {
				return "", nil, err
			}
		}
		if t.Mode == "include" {
			includes = append(includes, predicate)
			incArgs = append(incArgs, args...)
		} else {
			excludes = append(excludes, predicate)
			excArgs = append(excArgs, args...)
		}
	}
	if len(includes) == 0 {
		return "", nil, errors.New("campaign has no included lists or segments")
	}
	query := `FROM subscribers s WHERE s.brand_id=? AND s.status='active' AND (` + strings.Join(includes, " OR ") + `)`
	args := []any{brandID}
	args = append(args, incArgs...)
	if len(excludes) > 0 {
		query += ` AND NOT (` + strings.Join(excludes, " OR ") + `)`
		args = append(args, excArgs...)
	}
	query += ` AND NOT EXISTS(SELECT 1 FROM suppressions x WHERE x.kind='email' AND x.value=s.email AND (x.brand_id=s.brand_id OR x.brand_id IS NULL)) AND NOT EXISTS(SELECT 1 FROM suppressions x WHERE x.kind='domain' AND x.value=s.domain AND (x.brand_id=s.brand_id OR x.brand_id IS NULL))`
	return query, args, nil
}
func (e *Engine) snapshot(ctx context.Context, c campaign) error {
	var complete int
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT recipients_snapshotted FROM campaign_preparations WHERE campaign_id=?`, c.id).Scan(&complete); err != nil {
		return err
	}
	if complete != 0 {
		return nil
	}
	q, args, err := e.audienceSQL(ctx, c)
	if err != nil {
		return err
	}
	args = append([]any{c.id}, args...)
	return e.db.Immediate(ctx, func(conn *sql.Conn) error {
		// Another engine may have completed the immutable snapshot while we waited.
		if err := conn.QueryRowContext(ctx, `SELECT recipients_snapshotted FROM campaign_preparations WHERE campaign_id=?`, c.id).Scan(&complete); err != nil {
			return err
		}
		if complete != 0 {
			return nil
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO campaign_recipients(campaign_id,subscriber_id,snapshot_email,snapshot_name,snapshot_fields) SELECT ?,s.id,s.email,s.name,s.fields `+q+` ON CONFLICT(campaign_id,subscriber_id) DO NOTHING`, args...); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO campaign_stats(campaign_id,recipients) SELECT ?,count(*) FROM campaign_recipients WHERE campaign_id=? ON CONFLICT(campaign_id) DO UPDATE SET recipients=excluded.recipients`, c.id, c.id); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, `UPDATE campaign_preparations SET recipients_snapshotted=1 WHERE campaign_id=?`, c.id)
		return err
	})
}

func (e *Engine) Estimate(ctx context.Context, id int64) (Estimate, error) {
	c, err := e.prepare(ctx, id, false)
	if err != nil {
		return Estimate{}, err
	}
	qsql, args, err := e.audienceSQL(ctx, c)
	if err != nil {
		return Estimate{}, err
	}
	var n int64
	if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) `+qsql, args...).Scan(&n); err != nil {
		return Estimate{}, err
	}
	delivery, err := e.resolveBrand(ctx, c.brand)
	if err != nil {
		return Estimate{}, err
	}
	q, err := delivery.sender.GetAccount(ctx)
	if err != nil {
		return Estimate{}, err
	}
	if q.MaxSendRate <= 0 || math.IsNaN(q.MaxSendRate) || math.IsInf(q.MaxSendRate, 0) {
		return Estimate{}, errors.New("invalid SES sending quota")
	}
	raw, err := e.render(c, recipient{ID: 1, Email: "recipient@example.org", Name: "Recipient", Fields: "{}"})
	if err != nil {
		return Estimate{}, err
	}
	return Estimate{n, int64(len(raw)), int64(len(raw)) * n, float64(n) / q.MaxSendRate, q, float64(n) <= q.Max24HourSend-q.SentLast24Hours}, nil
}

// waitToken serializes reservations so stalled goroutines cannot accumulate overdue tokens.
func (e *Engine) waitToken(ctx context.Context) error {
	e.paceMu.Lock()
	defer e.paceMu.Unlock()
	return e.limiter.Wait(ctx)
}

func (e *Engine) markRequest() {
	now := time.Since(e.metricsOrigin).Nanoseconds() + 1
	e.firstRequestAt.CompareAndSwap(0, now)
	for {
		last := e.lastRequestAt.Load()
		if now <= last || e.lastRequestAt.CompareAndSwap(last, now) {
			break
		}
	}
}

func histPercentile(buckets *[30001]uint64, count uint64, p float64) float64 {
	if count == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(count) * p))
	var sum uint64
	for i, n := range buckets {
		sum += n
		if sum >= target {
			return float64(i)
		}
	}
	return 30000
}
