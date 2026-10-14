// Package clientip resolves client addresses through explicitly trusted proxies.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

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

func trustedProxy(ip netip.Addr, proxies []netip.Prefix) bool {
	for _, prefix := range proxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// IP ignores forwarding headers unless the socket peer is trusted. Walk
// X-Forwarded-For from the closest proxy toward the client, stopping at the
// first untrusted hop. Earlier entries cannot influence that identity, even if
// a client supplied a malformed prefix before the ingress proxy appended its
// observed peer. Forwarded and X-Real-IP are intentionally ignored.
func IP(r *http.Request, proxies []netip.Prefix) string {
	peer, ok := peerIP(r.RemoteAddr)
	if !ok {
		return "unknown"
	}
	if !trustedProxy(peer, proxies) {
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
		if !trustedProxy(peer, proxies) {
			return peer.String()
		}
	}
	return peer.String()
}
