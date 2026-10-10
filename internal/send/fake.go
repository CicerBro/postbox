package send

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/smithy-go"
)

type FakeConfig struct {
	Rate                                                       float64
	Latency, Jitter                                            time.Duration
	RejectEvery, ThrottleEvery, ServerErrorEvery, TimeoutEvery int
	Timeout                                                    time.Duration
	Max24HourSend                                              float64
}
type Fake struct {
	Config     FakeConfig
	Calls      atomic.Int64
	Accepted   atomic.Int64
	mu         sync.Mutex
	Recipients map[string]int
	windows    map[int64]int
	MaxWindow  int
	last       time.Time
	rateTokens float64
}

func NewFake(cfg FakeConfig) *Fake {
	if cfg.Rate <= 0 {
		cfg.Rate = 175
	}
	if cfg.Max24HourSend <= 0 {
		cfg.Max24HourSend = 10000000
	}
	return &Fake{Config: cfg, Recipients: map[string]int{}, windows: map[int64]int{}}
}
func (f *Fake) GetAccount(ctx context.Context) (Quota, error) {
	return Quota{f.Config.Rate, f.Config.Max24HourSend, float64(f.Accepted.Load())}, ctx.Err()
}
func fakeError(code string, server bool) error {
	fault := smithy.FaultClient
	if server {
		fault = smithy.FaultServer
	}
	return &smithy.GenericAPIError{Code: code, Message: "injected fake SES error", Fault: fault}
}
func (f *Fake) Send(ctx context.Context, r Request) (string, error) {
	n := f.Calls.Add(1)
	d := f.Config.Latency
	if f.Config.Jitter > 0 {
		d += time.Duration(rand.Int64N(int64(f.Config.Jitter) + 1))
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-t.C:
	}
	if f.Config.RejectEvery > 0 && n%int64(f.Config.RejectEvery) == 0 {
		return "", fakeError("MessageRejected", false)
	}
	if f.Config.ThrottleEvery > 0 && n%int64(f.Config.ThrottleEvery) == 0 {
		return "", fakeError("TooManyRequestsException", false)
	}
	if f.Config.ServerErrorEvery > 0 && n%int64(f.Config.ServerErrorEvery) == 0 {
		return "", fakeError("InternalServiceError", true)
	}
	f.Accepted.Add(1)
	f.mu.Lock()
	f.Recipients[r.To]++
	sec := time.Now().Unix()
	f.windows[sec]++
	if f.windows[sec] > f.MaxWindow {
		f.MaxWindow = f.windows[sec]
	}
	f.mu.Unlock()
	if f.Config.TimeoutEvery > 0 && n%int64(f.Config.TimeoutEvery) == 0 {
		d := f.Config.Timeout
		if d <= 0 {
			d = 31 * time.Second
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", context.DeadlineExceeded
		}
	}
	return fmt.Sprintf("fake-%d", n), nil
}
func (f *Fake) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/email/account", func(w http.ResponseWriter, r *http.Request) {
		q, err := f.GetAccount(r.Context())
		if err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"SendingEnabled": true, "ProductionAccessEnabled": true, "SendQuota": map[string]float64{"MaxSendRate": q.MaxSendRate, "Max24HourSend": q.Max24HourSend, "SentLast24Hours": q.SentLast24Hours}})
	})
	mux.HandleFunc("POST /v2/email/outbound-emails", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FromEmailAddress     string
			Destination          struct{ ToAddresses []string }
			Content              struct{ Raw struct{ Data []byte } }
			ConfigurationSetName string
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 15<<20)).Decode(&input); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if len(input.Destination.ToAddresses) != 1 || len(input.Content.Raw.Data) == 0 {
			http.Error(w, "one destination and raw message required", 400)
			return
		}
		id, err := f.Send(r.Context(), Request{From: input.FromEmailAddress, To: input.Destination.ToAddresses[0], Raw: input.Content.Raw.Data, ConfigurationSet: input.ConfigurationSetName})
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				panic(http.ErrAbortHandler)
			}
			code := "InternalServiceError"
			status := 500
			if api, ok := err.(smithy.APIError); ok {
				code = api.ErrorCode()
				switch code {
				case "MessageRejected":
					status = 400
				case "TooManyRequestsException":
					status = 429
				}
			}
			w.Header().Set("X-Amzn-Errortype", code)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": err.Error(), "__type": code})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"MessageId": id})
	})
	return mux
}
