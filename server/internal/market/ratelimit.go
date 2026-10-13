package market

import (
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	count int
	reset time.Time
}
type Limiter struct {
	mu         sync.Mutex
	buckets    map[string]bucket
	nextSweep  time.Time
	maxEntries int
}

func NewLimiter() *Limiter { return &Limiter{buckets: make(map[string]bucket), maxEntries: 50000} }
func (l *Limiter) Allow(key string, maximum int, window time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.nextSweep) {
		for k, b := range l.buckets {
			if !now.Before(b.reset) {
				delete(l.buckets, k)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	b, ok := l.buckets[key]
	if !ok || !now.Before(b.reset) {
		if !ok && len(l.buckets) >= l.maxEntries {
			return false
		}
		b = bucket{reset: now.Add(window)}
	}
	if b.count >= maximum {
		return false
	}
	b.count++
	l.buckets[key] = b
	return true
}
func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range strings.Split(value, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
func trusted(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
func clientIP(remote, forwarded string, prefixes []netip.Prefix) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	// Only a trusted immediate peer can supply a forwarding chain. Walk from the
	// right, stopping at the first untrusted hop so a client cannot prepend a fake IP.
	if trusted(peer, prefixes) && forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) <= 16 {
			for i := len(parts) - 1; i >= 0; i-- {
				addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
				if err != nil {
					break
				}
				peer = addr.Unmap()
				if !trusted(peer, prefixes) {
					break
				}
			}
		}
	}
	if peer.Is6() {
		return netip.PrefixFrom(peer, 64).Masked().String()
	}
	return peer.String()
}
