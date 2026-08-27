package mcpaccess

import (
	"testing"
	"time"
)

func TestLimiterBoundsAndExpiresSourceBuckets(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	limiter := NewLimiter(10, 1)
	limiter.now = func() time.Time { return now }
	limiter.maxBuckets = 4
	for _, key := range []string{"one", "two", "three", "four"} {
		release, ok := limiter.Acquire(key)
		if !ok {
			t.Fatalf("initial key %q was rejected", key)
		}
		release()
	}
	if _, ok := limiter.Acquire("five"); ok {
		t.Fatal("limiter accepted a source beyond its bucket bound")
	}
	now = now.Add(2 * time.Minute)
	release, ok := limiter.Acquire("five")
	if !ok {
		t.Fatal("expired buckets were not reclaimed")
	}
	release()
	if len(limiter.buckets) != 1 {
		t.Fatalf("bucket count=%d, want 1 after expiry", len(limiter.buckets))
	}
}

func TestSourceKeyGroupsIPv6PrivacyAddressesByPrefix(t *testing.T) {
	first := SourceKey("[2001:db8:abcd:12::1]:443")
	second := SourceKey("[2001:db8:abcd:12:ffff::2]:8443")
	other := SourceKey("[2001:db8:abcd:13::1]:443")
	if first != second {
		t.Fatalf("same /64 keys differ: %q %q", first, second)
	}
	if first == other {
		t.Fatalf("different /64 keys match: %q", first)
	}
}
