package mcpaccess

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

func SourceKey(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil {
			address = address.Unmap()
			if address.Is6() {
				return netip.PrefixFrom(address, 64).Masked().String()
			}
			return address.String()
		}
		return host
	}
	return remoteAddress
}

type limitBucket struct {
	Window, LastSeen time.Time
	Requests, Active int
}
type Limiter struct {
	mu                   sync.Mutex
	buckets              map[string]limitBucket
	requests, concurrent int
	maxBuckets           int
	lastCleanup          time.Time
	now                  func() time.Time
}

func NewLimiter(requestsPerMinute, concurrent int) *Limiter {
	if requestsPerMinute <= 0 {
		requestsPerMinute = 60
	}
	if concurrent <= 0 {
		concurrent = 8
	}
	return &Limiter{buckets: map[string]limitBucket{}, requests: requestsPerMinute, concurrent: concurrent, maxBuckets: 4096, now: time.Now}
}
func (limiter *Limiter) Acquire(key string) (func(), bool) {
	limiter.mu.Lock()
	now := limiter.now()
	if limiter.lastCleanup.IsZero() || now.Sub(limiter.lastCleanup) >= time.Minute || len(limiter.buckets) >= limiter.maxBuckets {
		limiter.prune(now)
	}
	bucket, exists := limiter.buckets[key]
	if !exists && len(limiter.buckets) >= limiter.maxBuckets {
		limiter.mu.Unlock()
		return func() {}, false
	}
	if bucket.Window.IsZero() || now.Sub(bucket.Window) >= time.Minute {
		bucket.Window = now
		bucket.Requests = 0
	}
	bucket.LastSeen = now
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
			bucket.LastSeen = limiter.now()
			limiter.buckets[key] = bucket
			limiter.mu.Unlock()
		})
	}, true
}

func (limiter *Limiter) prune(now time.Time) {
	// 修复公网来源桶永久保留导致状态无界：空闲窗口到期后回收，并由 maxBuckets 提供硬上限。
	for key, bucket := range limiter.buckets {
		if bucket.Active == 0 && !bucket.LastSeen.IsZero() && now.Sub(bucket.LastSeen) >= time.Minute {
			delete(limiter.buckets, key)
		}
	}
	limiter.lastCleanup = now
}
