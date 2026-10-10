package send

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

type brandResolverFunc func(context.Context, int64) (BrandSender, error)

func (f brandResolverFunc) ResolveBrand(ctx context.Context, id int64) (BrandSender, error) {
	return f(ctx, id)
}

type quotaSender struct {
	policySender
	quota Quota
}

func (s *quotaSender) GetAccount(context.Context) (Quota, error) { return s.quota, nil }

func (s *quotaSender) Send(ctx context.Context, request Request) (string, error) {
	id, err := s.policySender.Send(ctx, request)
	return fmt.Sprintf("%s-%d", id, s.calls), err
}

func TestBrandSenderRoutesEveryOperationAndFreezesCampaignClient(t *testing.T) {
	db, e, _ := fixture(t, 2)
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<p>Hello [Name]</p>'`); err != nil {
		t.Fatal(err)
	}
	global := &policySender{}
	selected := &quotaSender{quota: Quota{1000, 10000, 0}}
	replacement := &quotaSender{quota: Quota{1, 10000, 0}}
	e.sender = global
	e.cfg.MaxWorkers = 1
	current := selected
	resolutions := 0
	e.cfg.BrandSenderProvider = brandResolverFunc(func(_ context.Context, brand int64) (BrandSender, error) {
		if brand != 1 {
			t.Fatalf("resolved brand %d", brand)
		}
		resolutions++
		return BrandSender{current, "123:eu-west-1"}, nil
	})
	estimate, err := e.Estimate(context.Background(), 1)
	if err != nil || estimate.Quota.MaxSendRate != 1000 {
		t.Fatalf("estimate=%+v error=%v", estimate, err)
	}
	checklist, err := e.Checklist(context.Background(), 1, PreflightOptions{})
	if err != nil || checklist.Quota.MaxSendRate != 1000 || !checklist.Checks.Ready {
		t.Fatalf("checklist=%+v error=%v", checklist, err)
	}
	if err = e.TestCampaign(context.Background(), 1, []string{"test@example.org"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SendNotification(context.Background(), Request{BrandID: 1}); err != nil {
		t.Fatal(err)
	}
	selected.afterSend = func() { current = replacement }
	if err = e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if selected.calls != 4 || replacement.calls != 0 || global.calls != 0 || resolutions != 5 {
		t.Fatalf("selected=%d replacement=%d global=%d resolutions=%d", selected.calls, replacement.calls, global.calls, resolutions)
	}
}

func TestBrandSenderResolutionFailsClosed(t *testing.T) {
	_, e, _ := fixture(t, 1)
	global := &policySender{}
	e.sender = global
	want := errors.New("brand credentials cannot be decrypted")
	e.cfg.BrandSenderProvider = brandResolverFunc(func(context.Context, int64) (BrandSender, error) {
		return BrandSender{}, want
	})
	if _, err := e.Estimate(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("estimate error %v", err)
	}
	if err := e.TestCampaign(context.Background(), 1, []string{"test@example.org"}, 0); !errors.Is(err, want) {
		t.Fatalf("test send error %v", err)
	}
	if _, err := e.SendNotification(context.Background(), Request{BrandID: 1}); !errors.Is(err, want) {
		t.Fatalf("notification error %v", err)
	}
	if err := e.RunCampaign(context.Background(), 1); !errors.Is(err, want) {
		t.Fatalf("campaign error %v", err)
	}
	if global.calls != 0 || e.requests.Load() != 0 {
		t.Fatal("failed brand resolution dispatched through global credentials")
	}
}

func TestAccountPacingSharedAcrossBrandsAndSeparatedByRegion(t *testing.T) {
	_, e, _ := fixture(t, 1)
	s := &policySender{}
	e.cfg.BrandSenderProvider = brandResolverFunc(func(_ context.Context, brand int64) (BrandSender, error) {
		scope := "123:eu-west-1"
		if brand == 3 {
			scope = "123:eu-west-2"
		}
		return BrandSender{s, scope}, nil
	})
	a, _ := e.resolveBrand(context.Background(), 1)
	b, _ := e.resolveBrand(context.Background(), 2)
	c, _ := e.resolveBrand(context.Background(), 3)
	if a.pacer != b.pacer || a.pacer == c.pacer {
		t.Fatal("SES quota scope is not shared by account and region")
	}
	if err := a.setQuota(Quota{MaxSendRate: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Refreshing another brand must retain consumed tokens and throttled rate.
	if err := b.setQuota(Quota{MaxSendRate: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := b.wait(ctx); err == nil {
		t.Fatal("second brand bypassed account pacing")
	}
	a.adjust(true)
	if err := b.setQuota(Quota{MaxSendRate: 1}); err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(b.pacer.limiter.Limit())-.7) > 1e-9 {
		t.Fatal("brand quota refresh reset shared throttling")
	}
	b.syncCampaignRate()
	if e.Metrics().EffectiveRate != .7 {
		t.Fatal("campaign metrics lost shared account throttling")
	}
	if err := c.setQuota(Quota{MaxSendRate: 200}); err != nil {
		t.Fatal(err)
	}
	c.syncCampaignRate()
	if e.Metrics().EffectiveRate != 200 {
		t.Fatal("throttling carried over to another region")
	}
}

func TestBrandSenderTimeoutRemainsTerminal(t *testing.T) {
	db, e, _ := fixture(t, 1)
	s := &policySender{errors: []error{context.DeadlineExceeded}}
	e.cfg.BrandSenderProvider = brandResolverFunc(func(context.Context, int64) (BrandSender, error) {
		return BrandSender{s, "123:eu-west-1"}, nil
	})
	if err := e.RunCampaign(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 || e.Metrics().Unknown != 1 {
		t.Fatal("brand timeout was retried or not marked unknown")
	}
	assertPolicyJournal(t, db, []string{"unknown"})
}
