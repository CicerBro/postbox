package send

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/mail"
	"strconv"
	"sync"
	"time"

	"postbox/internal/render"
	"postbox/internal/subscribers"
	"postbox/internal/tracking/token"
)

var errNotSending = errors.New("campaign is no longer sending")
var errUnsafeAttempt = errors.New("recipient already has a potentially accepted attempt")
var errStaleIdentity = errors.New("campaign or recipient identity is no longer current")

type recipient struct {
	ID                  int64
	Email, Name, Fields string
	Attempts            int
	TrackingIdentity    string
}
type result struct {
	id                                   int64
	status, messageID, err               string
	attempt                              int
	at                                   int64
	campaignIdentity, subscriberIdentity string
}

func (e *Engine) render(c campaign, r recipient) ([]byte, error) {
	return e.renderWithSubjectPrefix(c, r, "")
}

func (e *Engine) renderWithSubjectPrefix(c campaign, r recipient, prefix string) ([]byte, error) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(r.Fields), &fields); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fields))
	for k, v := range fields {
		values[k] = fmt.Sprint(v)
	}
	tok := token.EncodeBound(e.cfg.TokenSecret, c.id, r.ID, c.trackingIdentity, r.TrackingIdentity)
	unsubscribe := c.trackingURL + "/u/" + tok
	subject, html, text, err := c.prepared.Render(render.Recipient{ID: r.ID, Email: r.Email, Name: r.Name, Fields: values, Token: tok, UnsubscribeURL: unsubscribe, WebVersionURL: c.trackingURL + "/w/" + strconv.FormatInt(c.id, 10) + "/" + tok})
	if err != nil {
		return nil, err
	}
	return render.BuildMIME(render.Message{FromName: c.fromName, FromEmail: c.fromEmail, ReplyTo: c.replyTo, ToName: r.Name, ToEmail: r.Email, Subject: prefix + subject, HTML: html, Text: text, UnsubscribeURL: unsubscribe, Date: c.date})
}
func (e *Engine) claim(ctx context.Context, id, last int64) ([]recipient, error) {
	return e.claimRecipients(ctx, id, last, e.cfg.ClaimBatch, "")
}

func (e *Engine) claimRecipients(ctx context.Context, id, last int64, limit int, providerFilter string) ([]recipient, error) {
	var items []recipient
	err := e.db.Immediate(ctx, func(c *sql.Conn) error {
		rows, err := c.QueryContext(ctx, `SELECT r.subscriber_id,coalesce(r.snapshot_email,s.email),coalesce(r.snapshot_name,s.name),coalesce(r.snapshot_fields,s.fields),r.attempts,s.tracking_identity FROM campaign_recipients r JOIN subscribers s ON s.id=r.subscriber_id WHERE r.campaign_id=? AND r.status='pending' AND r.subscriber_id>?`+providerFilter+` ORDER BY r.subscriber_id LIMIT ?`, id, last, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r recipient
			if err = rows.Scan(&r.ID, &r.Email, &r.Name, &r.Fields, &r.Attempts, &r.TrackingIdentity); err != nil {
				rows.Close()
				return err
			}
			items = append(items, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		stmt, err := c.PrepareContext(ctx, `UPDATE campaign_recipients SET status='claimed' WHERE campaign_id=? AND subscriber_id=? AND status='pending'`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range items {
			if _, err = stmt.ExecContext(ctx, id, r.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

// beginAttempt atomically checks current consent and records the uncertainty boundary.
// Immediate commits the reservation before any external dispatch. WAL NORMAL
// protects against process crashes; host crash durability is described by store.Open.
func (e *Engine) beginAttempt(ctx context.Context, c campaign, r recipient, attempt int) (bool, error) {
	if c.trackingIdentity == "" || r.TrackingIdentity == "" {
		return false, errStaleIdentity
	}
	started := false
	err := e.db.Immediate(ctx, func(conn *sql.Conn) error {
		var status, identity string
		if err := conn.QueryRowContext(ctx, `SELECT status,tracking_identity FROM campaigns WHERE id=? AND brand_id=?`, c.id, c.brand).Scan(&status, &identity); err != nil {
			return err
		}
		if identity != c.trackingIdentity {
			return errStaleIdentity
		}
		if status != "sending" || !e.enabled.Load() {
			return errNotSending
		}
		var valid int
		var err error
		if c.audience != "" {
			args := append([]any{}, c.audienceArgs...)
			args = append(args, r.ID, r.TrackingIdentity)
			err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 `+c.audience+` AND s.id=? AND s.tracking_identity=?)`, args...).Scan(&valid)
		} else {
			err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subscribers s WHERE s.brand_id=? AND s.id=? AND s.tracking_identity=? AND `+subscribers.MarketingEligibilitySQL+`)`, c.brand, r.ID, r.TrackingIdentity).Scan(&valid)
		}
		if err != nil {
			return err
		}

		if valid == 0 {
			return nil
		}
		res, err := conn.ExecContext(ctx, `UPDATE campaign_recipients SET attempts=? WHERE campaign_id=? AND subscriber_id=? AND status='claimed' AND attempts=? AND NOT EXISTS(SELECT 1 FROM send_attempts WHERE campaign_id=? AND subscriber_id=? AND outcome IN ('started','unknown','sent'))`, attempt, c.id, r.ID, attempt-1, c.id, r.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errUnsafeAttempt
		}
		now := time.Now().UnixMilli()
		if isGmailRecipient(r.Email) {
			if err = reserveGmailAttempt(ctx, conn, now); err != nil {
				return err
			}
		}
		if err = reserveBrandAttempt(ctx, conn, c.brand, now); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO send_attempts(campaign_id,subscriber_id,attempt,started_at,outcome) VALUES(?,?,?,?,'started')`, c.id, r.ID, attempt, now); err != nil {
			return err
		}
		started, err = e.beginDispatchTx(ctx, conn, c.id, r.ID, attempt, r.Email)
		if err != nil {
			return err
		}
		if !started {
			return errUnsafeAttempt
		}
		return nil
	})
	return started, err
}
func (e *Engine) finishAttempt(ctx context.Context, c campaign, r recipient, attempt int, outcome, messageID, detail string) error {
	if c.trackingIdentity == "" || r.TrackingIdentity == "" {
		return errStaleIdentity
	}
	cid, sid := c.id, r.ID
	return e.db.Immediate(ctx, func(conn *sql.Conn) error {
		var current int
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns c JOIN subscribers s ON s.brand_id=c.brand_id WHERE c.id=? AND c.tracking_identity=? AND s.id=? AND s.tracking_identity=?)`, cid, c.trackingIdentity, sid, r.TrackingIdentity).Scan(&current); err != nil {
			return err
		}
		if current == 0 {
			return nil
		}
		if _, err := conn.ExecContext(ctx, `UPDATE send_attempts SET finished_at=?,outcome=?,message_id=nullif(?,''),error=nullif(?,'') WHERE campaign_id=? AND subscriber_id=? AND attempt=? AND outcome='started'`, time.Now().UnixMilli(), outcome, messageID, detail, cid, sid, attempt); err != nil {
			return err
		}
		ledgerOutcome := outcome
		if outcome == "retry" || outcome == "failed" {
			ledgerOutcome = "rejected"
		}
		return e.finishDispatchTx(ctx, conn, cid, sid, attempt, ledgerOutcome)
	})
}

func waitBackoff(ctx context.Context, attempt int) error {
	base := time.Duration(1<<min(attempt, 6)) * 100 * time.Millisecond
	d := base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (e *Engine) sendOne(claimCtx context.Context, c campaign, r recipient, workers *campaignWorkers) (out result) {
	out = result{id: r.ID, at: time.Now().UnixMilli(), campaignIdentity: c.trackingIdentity, subscriberIdentity: r.TrackingIdentity}
	if claimCtx.Err() != nil || !e.enabled.Load() {
		out.status = "pending"
		return out
	}
	raw, err := e.render(c, r)
	if err != nil {
		out.status = "failed"
		out.err = err.Error()
		return out
	}
	from := (&mail.Address{Name: c.fromName, Address: c.fromEmail}).String()
	pacedGmail := c.gmailInterval > 0 && isGmailRecipient(r.Email)
	for try := 0; try < 6; {
		attempt := r.Attempts + try + 1
		req := Request{From: from, To: r.Email, Raw: raw, ConfigurationSet: c.configSet, BrandID: c.brand, CampaignID: c.id, CampaignIdentity: c.trackingIdentity, SubscriberIdentity: r.TrackingIdentity, Attempt: attempt}
		sendErr, dispatched := e.dispatchAttempt(claimCtx, c, r, req, workers, pacedGmail, &out)
		if errors.Is(sendErr, errBrandPaced) {
			continue
		}
		if errors.Is(sendErr, errGmailPaced) {
			if !pacedGmail {
				out.status = "pending"
				return out
			}
			continue
		}
		if !dispatched {
			return out
		}
		if sendErr == nil {
			out.status = "sent"
			return out
		}
		class := Classify(sendErr)
		e.mu.Lock()
		e.errorCounts[string(class)]++
		e.mu.Unlock()
		out.err = sendErr.Error()
		switch class {
		case Permanent:
			out.status = "failed"
			if err = e.finishAttempt(context.Background(), c, r, attempt, "failed", "", out.err); err != nil {
				out.status = "unknown"
				out.err = err.Error()
			}
			return out
		case Ambiguous:
			// SES may have accepted the request. Never dispatch this recipient again.
			out.status = "unknown"
			if err = e.finishAttempt(context.Background(), c, r, attempt, "unknown", "", out.err); err != nil {
				out.err = err.Error()
			}
			return out
		case PauseAccount:
			out.status = "pending"
			if err = e.finishAttempt(context.Background(), c, r, attempt, "failed", "", out.err); err != nil {
				out.status = "unknown"
				out.err = err.Error()
			}
			_ = e.Pause(context.Background(), c.id)
			return out
		case Throttled:
			c.delivery.adjust(true)
			e.adjust(true)
			c.delivery.syncCampaignRate()
		}
		if err = e.finishAttempt(context.Background(), c, r, attempt, "retry", "", out.err); err != nil {
			out.status = "unknown"
			out.err = err.Error()
			return out
		}
		if try == 5 {
			out.status = "failed"
			return out
		}
		if err = waitBackoff(claimCtx, try); err != nil {
			out.status = "pending"
			return out
		}
		try++
	}
	return out
}

func (e *Engine) dispatchAttempt(ctx context.Context, c campaign, r recipient, req Request, workers *campaignWorkers, pacedGmail bool, out *result) (error, bool) {
	var releaseGmail func()
	if pacedGmail {
		// Serialize Gmail starts through the persisted reservation. Release the
		// turn before calling SES so paced requests can overlap in flight.
		select {
		case workers.gmailTurn <- struct{}{}:
		case <-ctx.Done():
			out.status = "pending"
			return nil, false
		}
		releaseGmail = sync.OnceFunc(func() { <-workers.gmailTurn })
		defer releaseGmail()
		if e.waitForGmailAttempt(ctx, c.gmailInterval) != nil {
			out.status = "pending"
			return nil, false
		}
	}
	if !workers.acquire(ctx, e, pacedGmail) {
		out.status = "pending"
		return nil, false
	}
	defer workers.release()
	releaseBrand, err := e.acquireBrandPacing(ctx, c.brand)
	if err != nil {
		out.status = "pending"
		return nil, false
	}
	defer releaseBrand()
	if err := c.delivery.wait(ctx); err != nil || ctx.Err() != nil || !e.enabled.Load() {
		out.status = "pending"
		return nil, false
	}
	var currentStatus string
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id=?`, c.id).Scan(&currentStatus); err != nil || currentStatus != "sending" {
		out.status = "pending"
		return nil, false
	}
	args := append([]any{}, c.audienceArgs...)
	args = append(args, r.ID)
	var eligible int
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 `+c.audience+` AND s.id=?)`, args...).Scan(&eligible); err != nil {
		out.status = "pending"
		return nil, false
	}
	if eligible == 0 {
		out.status = "skipped"
		return nil, false
	}
	ok, err := e.beginAttempt(ctx, c, r, req.Attempt)
	if errors.Is(err, errGmailPaced) || errors.Is(err, errBrandPaced) {
		return err, false
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, errNotSending) {
			out.status = "pending"
		} else {
			out.status = "unknown"
			out.err = err.Error()
		}
		return nil, false
	}
	if !ok {
		out.status = "skipped"
		return nil, false
	}
	releaseBrand()
	if releaseGmail != nil {
		releaseGmail()
	}
	out.attempt = req.Attempt
	e.inflight.Add(1)
	start := time.Now()
	requestCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	e.markRequest()
	messageID, sendErr := c.delivery.sender.Send(requestCtx, req)
	cancel()
	e.inflight.Add(-1)
	e.requests.Add(1)
	e.observe(time.Since(start), false)
	out.at = time.Now().UnixMilli()
	if sendErr == nil {
		out.messageID = messageID
	}
	return sendErr, true
}
func (e *Engine) writeResults(ctx context.Context, id int64, batch []result) error {
	start := time.Now()
	var sent, failed, unknown, skipped int
	err := e.db.Immediate(ctx, func(c *sql.Conn) error {
		var campaignStatus, campaignIdentity string
		if err := c.QueryRowContext(ctx, `SELECT status,tracking_identity FROM campaigns WHERE id=?`, id).Scan(&campaignStatus, &campaignIdentity); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		stmt, err := c.PrepareContext(ctx, `UPDATE campaign_recipients SET status=?,message_id=nullif(?,''),error=nullif(?,''),sent_at=CASE WHEN ?='sent' THEN ? ELSE sent_at END WHERE campaign_id=? AND subscriber_id=? AND status='claimed' AND EXISTS(SELECT 1 FROM subscribers s WHERE s.id=campaign_recipients.subscriber_id AND s.tracking_identity=?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range batch {
			if r.campaignIdentity == "" || r.subscriberIdentity == "" || r.campaignIdentity != campaignIdentity {
				continue
			}
			state := r.status
			if state == "pending" {
				if campaignStatus == "cancelled" {
					state = "skipped"
				}
			}
			res, err := stmt.ExecContext(ctx, state, r.messageID, r.err, state, r.at, id, r.id, r.subscriberIdentity)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			switch state {
			case "sent":
				sent++
			case "failed":
				failed++
			case "unknown":
				unknown++
			case "skipped":
				skipped++
			}
			if r.attempt > 0 {
				outcome := state
				if state == "pending" || state == "skipped" {
					outcome = "failed"
				}
				if _, err = c.ExecContext(ctx, `UPDATE send_attempts SET outcome=?,finished_at=?,message_id=nullif(?,''),error=nullif(?,'') WHERE campaign_id=? AND subscriber_id=? AND attempt=? AND outcome='started'`, outcome, r.at, r.messageID, r.err, id, r.id, r.attempt); err != nil {
					return err
				}
				if state == "sent" || state == "unknown" {
					if err = e.finishDispatchTx(ctx, c, id, r.id, r.attempt, state); err != nil {
						return err
					}
				}
			}
		}
		if _, err = c.ExecContext(ctx, `UPDATE campaign_stats SET sent=sent+?,failed=failed+?,unknown=unknown+?,skipped=skipped+? WHERE campaign_id=?`, sent, failed, unknown, skipped, id); err != nil {
			return err
		}
		if sent > 0 {
			if _, err = c.ExecContext(ctx, `INSERT INTO send_hourly(brand_id,hour,sent) SELECT brand_id,?,? FROM campaigns WHERE id=? ON CONFLICT(brand_id,hour) DO UPDATE SET sent=sent+excluded.sent`, time.Now().UTC().Truncate(time.Hour).UnixMilli(), sent, id); err != nil {
				return err
			}
		}
		return nil
	})
	e.observe(time.Since(start), true)
	if err == nil {
		e.sent.Add(int64(sent))
		e.failed.Add(int64(failed))
		e.unknown.Add(int64(unknown))
		e.writerBatches.Add(1)
		e.writerRows.Add(int64(len(batch)))
	}
	return err
}

type campaignWorkers struct {
	mu        sync.Mutex
	busy      int
	waiting   [2]int
	nextLane  int
	changed   [2]chan struct{}
	gmailTurn chan struct{}
}

func (w *campaignWorkers) acquire(ctx context.Context, e *Engine, gmail bool) bool {
	lane := 0
	if gmail {
		lane = 1
	}
	other := 1 - lane
	w.mu.Lock()
	w.waiting[lane]++
	for ctx.Err() == nil {
		// Alternate admission while both lanes have a sender ready. Either
		// lane can use the full worker budget when the other is idle.
		if w.busy < e.workerTarget() && (w.waiting[other] == 0 || w.nextLane == lane) {
			w.busy++
			w.waiting[lane]--
			w.nextLane = other
			if w.busy < e.workerTarget() {
				w.wakeWaiters()
			}
			w.mu.Unlock()
			return true
		}
		w.mu.Unlock()
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
		case <-timer.C:
		case <-w.changed[lane]:
		}
		timer.Stop()
		w.mu.Lock()
	}
	w.waiting[lane]--
	w.wakeWaiters()
	w.mu.Unlock()
	return false
}

func (w *campaignWorkers) release() {
	w.mu.Lock()
	w.busy--
	w.wakeWaiters()
	w.mu.Unlock()
}

func (w *campaignWorkers) wakeWaiters() {
	for lane, changed := range w.changed {
		if w.waiting[lane] > 0 {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

func (e *Engine) sendGmailRecipients(ctx context.Context, c campaign, workers *campaignWorkers, results chan<- result, providerFilter string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan recipient)
	var senders sync.WaitGroup
	const gmailWorkers = 4
	for range min(gmailWorkers, e.cfg.MaxWorkers) {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for r := range jobs {
				results <- e.sendOne(ctx, c, r, workers)
			}
		}()
	}
	defer func() {
		close(jobs)
		senders.Wait()
	}()

	var last int64
	for ctx.Err() == nil {
		items, err := e.claimRecipients(ctx, c.id, last, 1, providerFilter)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			cancel()
			return err
		}
		if len(items) == 0 {
			return nil
		}
		r := items[0]
		last = r.ID
		select {
		case jobs <- r:
		case <-ctx.Done():
			results <- result{id: r.ID, status: "pending", campaignIdentity: c.trackingIdentity, subscriberIdentity: r.TrackingIdentity}
			return nil
		}
	}
	return nil
}

func (e *Engine) RunCampaign(ctx context.Context, id int64) error {
	if err := e.ensureDispatchLedger(ctx); err != nil {
		return err
	}
	e.runMu.Lock()
	defer e.runMu.Unlock()
	claimCtx, cancel := context.WithCancelCause(ctx)
	stop := func() { cancel(nil) }
	defer stop()
	e.mu.Lock()
	e.running[id] = cancel
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.running, id); e.mu.Unlock() }()
	c, err := e.prepareCampaignRun(claimCtx, id)
	if err != nil {
		if errors.Is(context.Cause(claimCtx), errGmailRateChanged) {
			return nil
		}
		return err
	}
	if err := e.activateCampaignRun(claimCtx, id); err != nil {
		if errors.Is(context.Cause(claimCtx), errGmailRateChanged) {
			return nil
		}
		return err
	}

	jobs := make(chan recipient, e.cfg.ClaimBatch)
	results := make(chan result, e.cfg.ResultBatch)
	writerDone := e.startResultWriter(id, results, stop)
	var senders sync.WaitGroup
	workers := &campaignWorkers{
		changed:   [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)},
		gmailTurn: make(chan struct{}, 1),
	}
	for i := 0; i < e.cfg.MaxWorkers; i++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for r := range jobs {
				e.backlog.Add(-1)
				results <- e.sendOne(claimCtx, c, r, workers)
			}
		}()
	}
	claim := e.claim
	var gmailErr error
	if c.gmailInterval > 0 {
		predicate := gmailRecipientSQL("coalesce(r.snapshot_email,s.email)")
		claim = func(ctx context.Context, id, last int64) ([]recipient, error) {
			return e.claimRecipients(ctx, id, last, e.cfg.ClaimBatch, " AND NOT "+predicate)
		}
		senders.Add(1)
		go func() {
			defer senders.Done()
			gmailErr = e.sendGmailRecipients(claimCtx, c, workers, results, " AND "+predicate)
			if gmailErr != nil {
				stop()
			}
		}()
	}
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		refresh := time.NewTicker(e.cfg.QuotaRefresh)
		increase := time.NewTicker(time.Second)
		defer refresh.Stop()
		defer increase.Stop()
		for {
			select {
			case <-claimCtx.Done():
				return
			case <-increase.C:
				c.delivery.adjust(false)
				e.adjust(false)
				c.delivery.syncCampaignRate()
			case <-refresh.C:
				q, err := c.delivery.sender.GetAccount(claimCtx)
				if err != nil {
					if claimCtx.Err() == nil {
						_ = e.Pause(context.Background(), id)
					}
					return
				}
				var remaining int64
				if err = e.db.Reader.QueryRowContext(claimCtx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=? AND status IN ('pending','claimed')`, id).Scan(&remaining); err != nil || float64(remaining) > q.Max24HourSend-q.SentLast24Hours {
					if claimCtx.Err() == nil {
						_ = e.Pause(context.Background(), id)
					}
					return
				}
				if err = c.delivery.setQuota(q); err == nil {
					c.delivery.syncCampaignRate()
				}
				if err != nil {
					_ = e.Pause(context.Background(), id)
					return
				}
			}
		}
	}()
	last := int64(0)
	var claimErr error
claimLoop:
	for claimCtx.Err() == nil {
		items, err := claim(claimCtx, id, last)
		if err != nil {
			if claimCtx.Err() == nil {
				claimErr = err
			}
			break
		}
		if len(items) == 0 {
			break
		}
		for i, r := range items {
			last = r.ID
			e.backlog.Add(1)
			select {
			case jobs <- r:
			case <-claimCtx.Done():
				e.backlog.Add(-1)
				for _, pending := range items[i:] {
					results <- result{id: pending.ID, status: "pending", campaignIdentity: c.trackingIdentity, subscriberIdentity: pending.TrackingIdentity}
				}
				break claimLoop
			}
		}
	}
	close(jobs)
	if claimErr != nil {
		stop()
	}
	senders.Wait()
	stop()
	<-refreshDone
	close(results)
	writerErr := <-writerDone
	if claimErr != nil || gmailErr != nil || writerErr != nil {
		return errors.Join(claimErr, gmailErr, writerErr)
	}
	if ctx.Err() != nil {
		return nil
	}
	return e.finishCampaign(context.Background(), id)
}

// prepareCampaignRun owns validation, snapshot creation and quota reservation.
// Callers hold runMu so these stages cannot race another local campaign run.
func (e *Engine) prepareCampaignRun(ctx context.Context, id int64) (campaign, error) {
	if e.cfg.RecoveryPolicy != "unknown" {
		return campaign{}, errors.New("recovery retry is prohibited")
	}
	if !e.enabled.Load() {
		return campaign{}, errors.New("global sending is disabled")
	}
	if len(e.cfg.TokenSecret) < 16 {
		return campaign{}, errors.New("tracking token secret must contain at least 16 bytes")
	}
	c, err := e.prepare(ctx, id, true)
	if err != nil {
		return campaign{}, err
	}
	if c.status == "sent" || c.status == "cancelled" || c.status == "paused" {
		return campaign{}, fmt.Errorf("campaign is %s", c.status)
	}
	gmailRate, err := e.GmailSendsPerMinute(ctx)
	if err != nil {
		return campaign{}, err
	}
	c.gmailInterval = sendInterval(gmailRate)
	c.delivery, err = e.resolveBrand(ctx, c.brand)
	if err != nil {
		return campaign{}, err
	}
	if err = campaignSenderError(ctx, c.delivery.sender, c.fromEmail); err != nil {
		return campaign{}, err
	}
	if err = e.snapshot(ctx, c); err != nil {
		return campaign{}, err
	}
	c.audience, c.audienceArgs, err = e.audienceSQL(ctx, c)
	if err != nil {
		return campaign{}, err
	}
	q, err := c.delivery.sender.GetAccount(ctx)
	if err != nil {
		return campaign{}, err
	}
	if err = c.delivery.setQuota(q); err != nil {
		return campaign{}, err
	}
	c.delivery.syncCampaignRate()
	var remaining int64
	if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=? AND status='pending'`, id).Scan(&remaining); err != nil {
		return campaign{}, err
	}
	if err = ReserveUserCampaign(ctx, e.db, id, remaining); err != nil {
		return campaign{}, err
	}
	if float64(remaining) > q.Max24HourSend-q.SentLast24Hours {
		return campaign{}, errors.New("campaign exceeds remaining SES daily quota; schedule later or split recipients")
	}
	return c, nil
}

// activateCampaignRun owns the single transaction that makes the campaign live.
func (e *Engine) activateCampaignRun(ctx context.Context, id int64) error {
	return e.db.Immediate(ctx, func(conn *sql.Conn) error {
		if !e.enabled.Load() {
			return errNotSending
		}
		var enabled string
		err := conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='sending_enabled'`).Scan(&enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if enabled == "false" {
			return errNotSending
		}
		res, err := conn.ExecContext(ctx, `UPDATE campaigns SET status='sending',started_at=coalesce(started_at,?),updated_at=? WHERE id=? AND status IN ('draft','scheduled','preparing','sending')`, time.Now().UnixMilli(), time.Now().UnixMilli(), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errNotSending
		}
		return e.emitLifecycleTx(ctx, conn, id, "campaign_started")
	})
}

// startResultWriter owns result persistence until results is closed by the
// coordinator, after all workers have stopped.
func (e *Engine) startResultWriter(id int64, results <-chan result, stop context.CancelFunc) <-chan error {
	writerDone := make(chan error, 1)
	go func() {
		t := time.NewTicker(e.cfg.FlushInterval)
		defer t.Stop()
		batch := make([]result, 0, e.cfg.ResultBatch)
		var firstErr error
		flush := func() {
			if len(batch) == 0 || firstErr != nil {
				return
			}
			var writeErr error
			for attempt := 0; attempt < 8; attempt++ {
				writeErr = e.writeResults(context.Background(), id, batch)
				if writeErr == nil {
					break
				}
				stop()
				_ = waitBackoff(context.Background(), min(attempt, 3))
			}
			if writeErr != nil {
				if firstErr == nil {
					firstErr = writeErr
				}
				return
			}
			batch = batch[:0]
		}
		for {
			select {
			case r, ok := <-results:
				if !ok {
					firstErr = nil
					flush()
					writerDone <- firstErr
					return
				}
				batch = append(batch, r)
				if len(batch) >= e.cfg.ResultBatch {
					flush()
				}
			case <-t.C:
				flush()
			}
		}
	}()
	return writerDone
}
