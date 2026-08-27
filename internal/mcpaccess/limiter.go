package mcpaccess

import (
	"net"
	"sync"
	"time"
)

func SourceKey(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

type limitBucket struct {
	Window           time.Time
	Requests, Active int
}
type Limiter struct {
	mu                   sync.Mutex
	buckets              map[string]limitBucket
	requests, concurrent int
	now                  func() time.Time
}

func NewLimiter(requestsPerMinute, concurrent int) *Limiter {
	if requestsPerMinute <= 0 {
		requestsPerMinute = 60
	}
	if concurrent <= 0 {
		concurrent = 8
	}
	return &Limiter{buckets: map[string]limitBucket{}, requests: requestsPerMinute, concurrent: concurrent, now: time.Now}
}
func (limiter *Limiter) Acquire(key string) (func(), bool) {
	limiter.mu.Lock()
	now := limiter.now()
	bucket := limiter.buckets[key]
	if bucket.Window.IsZero() || now.Sub(bucket.Window) >= time.Minute {
		bucket = limitBucket{Window: now}
	}
	if bucket.Requests >= limiter.requests || bucket.Active >= limiter.concurrent {
		limiter.buckets[key] = bucket
		limiter.mu.Unlock()
		return func() {}, false
	}
	bucket.Requests++
	bucket.Active++
	limiter.buckets[key] = bucket
	limiter.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			bucket := limiter.buckets[key]
			if bucket.Active > 0 {
				bucket.Active--
			}
			limiter.buckets[key] = bucket
			limiter.mu.Unlock()
		})
	}, true
}
