package httpapi

import (
	"net/http"
	"postbox/internal/clientip"
	"strconv"
	"time"
)

const maxRateBuckets = 10000

func (a *API) clientIP(r *http.Request) string {
	return clientip.IP(r, a.cfg.TrustedProxies)
}

// Authenticated factor operations follow the account across connections and
// sessions. Public operations use the canonical client IP, without a port.
func (a *API) rateKey(r *http.Request, scope string, account bool) string {
	if account {
		if i, ok := r.Context().Value(identityKey{}).(*identity); ok && i.UserID > 0 {
			return scope + ":user:" + strconv.FormatInt(i.UserID, 10)
		}
	}
	return scope + ":ip:" + a.clientIP(r)
}

func (a *API) allow(key string, max int) bool { return a.allowAt(key, max, time.Now()) }

func (a *API) allowAt(key string, max int, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.limits == nil {
		a.limits = map[string]*bucket{}
	}
	// Cleanup runs even for denied attempts. Keep active buckets when full so
	// creating new identities cannot evict an existing throttle.
	if !now.Before(a.nextLimitCleanup) || (a.limits[key] == nil && len(a.limits) >= maxRateBuckets) {
		for k, b := range a.limits {
			if !now.Before(b.until) {
				delete(a.limits, k)
			}
		}
		a.nextLimitCleanup = now.Add(time.Minute)
	}
	b := a.limits[key]
	if b == nil || !now.Before(b.until) {
		if b == nil && len(a.limits) >= maxRateBuckets {
			return false
		}
		b = &bucket{until: now.Add(time.Minute)}
		a.limits[key] = b
	}
	if b.n >= max {
		return false
	}
	b.n++
	return true
}
