package externaltrigger

import (
	"sync"
	"time"
)

type LimiterOptions struct {
	RequestsPerMinute       int
	Concurrent              int
	SourceRequestsPerMinute int
	SourceConcurrent        int
	ActionRequestsPerMinute int
	ActionConcurrent        int
	GlobalRequestsPerMinute int
	GlobalConcurrent        int
	MaxSubjects             int
	Now                     func() time.Time
}

type LimitSubject struct {
	KeyID  string
	Source string
	Action ActionType
}

type limitState struct {
	windowStart time.Time
	requests    int
	active      int
}

type bucketLimit struct {
	requestsPerMinute int
	concurrent        int
}

type Limiter struct {
	mu          sync.Mutex
	states      map[string]limitState
	key         bucketLimit
	source      bucketLimit
	action      bucketLimit
	global      bucketLimit
	maxSubjects int
	lastCleanup time.Time
	now         func() time.Time
}

func NewLimiter(options LimiterOptions) *Limiter {
	options.RequestsPerMinute = positiveOr(options.RequestsPerMinute, 60)
	options.Concurrent = positiveOr(options.Concurrent, 4)
	options.SourceRequestsPerMinute = positiveOr(options.SourceRequestsPerMinute, 120)
	options.SourceConcurrent = positiveOr(options.SourceConcurrent, 8)
	options.ActionRequestsPerMinute = positiveOr(options.ActionRequestsPerMinute, 60)
	options.ActionConcurrent = positiveOr(options.ActionConcurrent, 4)
	options.GlobalRequestsPerMinute = positiveOr(options.GlobalRequestsPerMinute, 600)
	options.GlobalConcurrent = positiveOr(options.GlobalConcurrent, 32)
	options.MaxSubjects = positiveOr(options.MaxSubjects, 8192)
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Limiter{
		states:      make(map[string]limitState),
		key:         bucketLimit{requestsPerMinute: options.RequestsPerMinute, concurrent: options.Concurrent},
		source:      bucketLimit{requestsPerMinute: options.SourceRequestsPerMinute, concurrent: options.SourceConcurrent},
		action:      bucketLimit{requestsPerMinute: options.ActionRequestsPerMinute, concurrent: options.ActionConcurrent},
		global:      bucketLimit{requestsPerMinute: options.GlobalRequestsPerMinute, concurrent: options.GlobalConcurrent},
		maxSubjects: options.MaxSubjects,
		now:         options.Now,
	}
}

func positiveOr(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (limiter *Limiter) Acquire(subject LimitSubject) (func(), bool) {
	return limiter.acquire(
		[]string{"global", "key\x00" + subject.KeyID, "source\x00" + subject.Source, "action\x00" + string(subject.Action)},
		[]bucketLimit{limiter.global, limiter.key, limiter.source, limiter.action},
	)
}

// AcquireSource applies only the global and source limits. It is intended for
// unauthenticated work that must be bounded before a key or action is known.
func (limiter *Limiter) AcquireSource(source string) (func(), bool) {
	return limiter.acquire(
		[]string{"global", "source\x00" + source},
		[]bucketLimit{limiter.global, limiter.source},
	)
}

func (limiter *Limiter) acquire(keys []string, limits []bucketLimit) (func(), bool) {
	limiter.mu.Lock()
	now := limiter.now()
	limiter.cleanup(now)
	newSubjects := 0
	for _, key := range keys {
		if _, exists := limiter.states[key]; !exists {
			newSubjects++
		}
	}
	if len(limiter.states)+newSubjects > limiter.maxSubjects {
		limiter.mu.Unlock()
		return func() {}, false
	}
	states := make([]limitState, len(keys))
	for index, key := range keys {
		state := limiter.states[key]
		if state.windowStart.IsZero() || now.Sub(state.windowStart) >= time.Minute {
			state.windowStart, state.requests = now, 0
		}
		if state.requests >= limits[index].requestsPerMinute || state.active >= limits[index].concurrent {
			limiter.states[key] = state
			limiter.mu.Unlock()
			return func() {}, false
		}
		states[index] = state
	}
	for index, key := range keys {
		state := states[index]
		state.requests++
		state.active++
		limiter.states[key] = state
	}
	limiter.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			for _, key := range keys {
				state := limiter.states[key]
				if state.active > 0 {
					state.active--
				}
				limiter.states[key] = state
			}
			limiter.mu.Unlock()
		})
	}, true
}

func (limiter *Limiter) cleanup(now time.Time) {
	if !limiter.lastCleanup.IsZero() && now.Sub(limiter.lastCleanup) < time.Minute {
		return
	}
	for key, state := range limiter.states {
		if key != "global" && state.active == 0 && !state.windowStart.IsZero() && now.Sub(state.windowStart) >= time.Minute {
			delete(limiter.states, key)
		}
	}
	limiter.lastCleanup = now
}
