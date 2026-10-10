package send

import (
	"context"
	"errors"
	"math"
	"sync"

	"golang.org/x/time/rate"
)

// BrandSender captures credentials for one operation. Scope identifies the SES
// account and region, so different brands using that quota share one pacer.
type BrandSender struct {
	Sender Sender
	Scope  string
}

type BrandSenderProvider interface {
	ResolveBrand(context.Context, int64) (BrandSender, error)
}

type accountPacer struct {
	mu                    sync.Mutex
	paceMu                sync.Mutex
	limiter               *rate.Limiter
	configured, effective float64
}

type brandDelivery struct {
	sender Sender
	pacer  *accountPacer
	engine *Engine
}

func (e *Engine) resolveBrand(ctx context.Context, brandID int64) (*brandDelivery, error) {
	if e.cfg.BrandSenderProvider == nil {
		return &brandDelivery{sender: e.sender, engine: e}, nil
	}
	resolved, err := e.cfg.BrandSenderProvider.ResolveBrand(ctx, brandID)
	if err != nil {
		return nil, err
	}
	if resolved.Sender == nil || resolved.Scope == "" {
		return nil, errors.New("brand SES sender has no account quota scope")
	}
	e.mu.Lock()
	pacer := e.scopes[resolved.Scope]
	if pacer == nil {
		pacer = &accountPacer{limiter: rate.NewLimiter(1, 1)}
		e.scopes[resolved.Scope] = pacer
	}
	e.mu.Unlock()
	return &brandDelivery{sender: resolved.Sender, pacer: pacer, engine: e}, nil
}

func (d *brandDelivery) setQuota(q Quota) error {
	if d.pacer == nil {
		return d.engine.setQuota(q)
	}
	if q.MaxSendRate <= 0 || math.IsNaN(q.MaxSendRate) || math.IsInf(q.MaxSendRate, 0) {
		return errors.New("SES send rate must be positive and finite")
	}
	p := d.pacer
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.configured
	p.configured = q.MaxSendRate
	if p.effective == 0 || p.effective == old || p.effective > q.MaxSendRate {
		p.effective = q.MaxSendRate
	}
	p.limiter.SetBurst(quotaBurst(p.effective))
	p.limiter.SetLimit(rate.Limit(p.effective))
	return nil
}

func (d *brandDelivery) adjust(throttle bool) {
	if d.pacer == nil {
		return
	}
	p := d.pacer
	p.mu.Lock()
	defer p.mu.Unlock()
	if throttle {
		p.effective = math.Max(.1, p.effective*.7)
	} else {
		p.effective = math.Min(p.configured, p.effective+math.Max(.1, p.configured*.01))
	}
	p.limiter.SetBurst(quotaBurst(p.effective))
	p.limiter.SetLimit(rate.Limit(p.effective))
}

// Campaign worker sizing and metrics follow its captured account's rate. A
// previous campaign's throttling must not carry over to another SES account.
func (d *brandDelivery) syncCampaignRate() {
	if d.pacer == nil {
		return
	}
	d.pacer.mu.Lock()
	configured, effective := d.pacer.configured, d.pacer.effective
	d.pacer.mu.Unlock()
	d.engine.mu.Lock()
	d.engine.configured, d.engine.effective = configured, effective
	d.engine.updateTargetLocked()
	d.engine.mu.Unlock()
}

func (d *brandDelivery) wait(ctx context.Context) error {
	if d.pacer == nil {
		return d.engine.waitToken(ctx)
	}
	// Serialize reservations across all brands sharing this account and region.
	d.pacer.paceMu.Lock()
	defer d.pacer.paceMu.Unlock()
	return d.pacer.limiter.Wait(ctx)
}
