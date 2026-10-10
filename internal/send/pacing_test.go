package send

import (
	"math"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestQuotaBurstPreservesRefillEnvelope(t *testing.T) {
	for _, quota := range []float64{.1, 1, 175, 250, 251, 1000, 5000} {
		burst := quotaBurst(quota)
		if quota == 175 && burst != 5 {
			t.Fatalf("175/s burst changed to %d", burst)
		}
		if burst > max(1, int(math.Floor(quota))) {
			t.Fatalf("quota %.1f burst %d exceeds one second", quota, burst)
		}
		limiter := rate.NewLimiter(rate.Limit(quota), burst)
		start := time.Now()
		var accepted []time.Time
		for tick := 0; tick < 400; tick++ {
			// Irregular polling models a scheduler that occasionally wakes late.
			now := start.Add(time.Duration(tick*tick) * 100 * time.Microsecond)
			for limiter.AllowN(now, 1) {
				accepted = append(accepted, now)
			}
			for left := 0; left < len(accepted); left += max(1, len(accepted)/20) {
				count := len(accepted) - left
				allowance := float64(burst) + quota*now.Sub(accepted[left]).Seconds()
				if float64(count) > allowance+1e-6 {
					t.Fatalf("quota %.1f sent %d with allowance %.3f", quota, count, allowance)
				}
			}
		}
		if limiter.Limit() != rate.Limit(quota) {
			t.Fatalf("quota %.1f changed fill rate to %v", quota, limiter.Limit())
		}
	}
}
