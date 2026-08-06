package externaltrigger

import (
	"sync"
	"time"
)

type LimiterOptions struct {
	RequestsPerMinute int
	Concurrent        int
	Now               func() time.Time
}

type limitState struct {
	windowStart time.Time
	requests    int
	active      int
}

type Limiter struct {
	mu                sync.Mutex
	states            map[string]limitState
	requestsPerMinute int
	concurrent        int
	now               func() time.Time
}

func NewLimiter(options LimiterOptions) *Limiter {
	if options.RequestsPerMinute <= 0 {
		options.RequestsPerMinute = 60
	}
	if options.Concurrent <= 0 {
		options.Concurrent = 4
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Limiter{states: make(map[string]limitState), requestsPerMinute: options.RequestsPerMinute, concurrent: options.Concurrent, now: options.Now}
}

func (limiter *Limiter) Acquire(keyID string) (func(), bool) {
	limiter.mu.Lock()
	now := limiter.now()
	state := limiter.states[keyID]
	if state.windowStart.IsZero() || now.Sub(state.windowStart) >= time.Minute {
		state.windowStart, state.requests = now, 0
	}
	if state.requests >= limiter.requestsPerMinute || state.active >= limiter.concurrent {
		limiter.states[keyID] = state
		limiter.mu.Unlock()
		return func() {}, false
	}
	state.requests++
	state.active++
	limiter.states[keyID] = state
	limiter.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			state := limiter.states[keyID]
			if state.active > 0 {
				state.active--
			}
			limiter.states[keyID] = state
			limiter.mu.Unlock()
		})
	}, true
}
