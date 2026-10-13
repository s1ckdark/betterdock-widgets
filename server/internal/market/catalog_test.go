package market

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestCatalogSignatureAndAllowlist(t *testing.T) {
	key, private, _ := ed25519.GenerateKey(rand.Reader)
	data := []byte(`{"schemaVersion":1,"sequence":2,"generatedAt":"2026-01-01T00:00:00Z","expiresAt":"2026-02-01T00:00:00Z","widgets":[{"id":"com.betterdock.clock","version":"1.0.0"}]}`)
	sign := func(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, b))) }
	c, err := verifyCatalog(data, sign(data), key)
	if err != nil || c.Versions["com.betterdock.clock"] != "1.0.0" {
		t.Fatalf("valid catalog: %+v %v", c, err)
	}
	if !c.ExpiresAt.Before(time.Now()) {
		t.Fatal("fixture expected expired; expired signed catalog must retain historical stats access")
	}
	if _, err := verifyCatalog(append(data, ' '), sign(data), key); err == nil {
		t.Fatal("tampered bytes accepted")
	}
	for _, bad := range []string{strings.Replace(string(data), `"sequence":2`, `"sequence":1`, 1), strings.Replace(string(data), `com.betterdock.clock`, `../../bad`, 1), strings.Replace(string(data), `"1.0.0"`, `"1.0"`, 1), strings.Replace(string(data), `"widgets":[`, `"widgets":[{"id":"com.betterdock.clock","version":"1.0.0"},`, 1)} {
		b := []byte(bad)
		if _, err := verifyCatalog(b, sign(b), key); err == nil {
			t.Fatalf("invalid signed catalog accepted: %s", bad)
		}
	}
}
func TestTrustedProxyChainAndLimiter(t *testing.T) {
	proxies, _ := ParseTrustedProxies("127.0.0.1/32,::1/128")
	for _, tc := range []struct{ remote, xff, want string }{
		{"203.0.113.9:1", "1.1.1.1", "203.0.113.9"},
		{"127.0.0.1:2", "1.1.1.1, 203.0.113.7", "203.0.113.7"},
		{"127.0.0.1:2", "203.0.113.7, 127.0.0.1", "203.0.113.7"},
		{"127.0.0.1:2", "invalid", "127.0.0.1"},
		{"[2001:db8:1234:1::a]:2", "", "2001:db8:1234:1::/64"},
	} {
		if got := clientIP(tc.remote, tc.xff, proxies); got != tc.want {
			t.Errorf("IP %q want %q", got, tc.want)
		}
	}
	now := time.Now()
	limiter := NewLimiter()
	limiter.maxEntries = 2
	if !limiter.Allow("a", 1, time.Minute, now) || limiter.Allow("a", 1, time.Minute, now) {
		t.Fatal("limit not enforced")
	}
	if !limiter.Allow("b", 1, time.Minute, now) || limiter.Allow("c", 1, time.Minute, now) {
		t.Fatal("memory bound not enforced")
	}
	if !limiter.Allow("c", 1, time.Minute, now.Add(time.Minute)) {
		t.Fatal("expired buckets not reclaimed")
	}
}
