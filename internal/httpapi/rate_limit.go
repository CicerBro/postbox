package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const maxRateBuckets = 10000

// peerIP accepts socket addresses and bare IPs, never hostnames or zones.
func peerIP(raw string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

func (a *API) trustedProxy(ip netip.Addr) bool {
	for _, prefix := range a.cfg.TrustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP ignores forwarding headers unless the socket peer is trusted. Walk
// X-Forwarded-For from the closest proxy toward the client, stopping at the
// first untrusted hop. Earlier entries cannot influence that identity, even if
// a client supplied a malformed prefix before the ingress proxy appended its
// observed peer. Forwarded and X-Real-IP are intentionally ignored.
func (a *API) clientIP(r *http.Request) string {
	peer, ok := peerIP(r.RemoteAddr)
	if !ok {
		return "unknown"
	}
	if !a.trustedProxy(peer) {
		return peer.String()
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return peer.String()
	}
	raw := strings.Join(values, ",")
	if len(raw) > 4096 {
		return peer.String()
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return peer.String()
	}
	for j := len(parts) - 1; j >= 0; j-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[j]))
		if err != nil || ip.Zone() != "" {
			return peer.String()
		}
		peer = ip.Unmap()
		if !a.trustedProxy(peer) {
			return peer.String()
		}
	}
	return peer.String()
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
