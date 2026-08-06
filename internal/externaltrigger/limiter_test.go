package externaltrigger

import (
	"testing"
	"time"
)

func TestLimiterBoundsRateAndConcurrencyPerKey(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewLimiter(LimiterOptions{RequestsPerMinute: 2, Concurrent: 1, Now: func() time.Time { return now }})
	release, ok := limiter.Acquire("one")
	if !ok {
		t.Fatal("first request was rejected")
	}
	if _, ok := limiter.Acquire("one"); ok {
		t.Fatal("concurrent request was accepted")
	}
	release()
	release, ok = limiter.Acquire("one")
	if !ok {
		t.Fatal("second request was rejected")
	}
	release()
	if _, ok := limiter.Acquire("one"); ok {
		t.Fatal("third request inside the minute was accepted")
	}
	now = now.Add(time.Minute)
	release, ok = limiter.Acquire("one")
	if !ok {
		t.Fatal("request after window reset was rejected")
	}
	release()
}
