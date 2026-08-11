package externaltrigger

import (
	"testing"
	"time"
)

func TestLimiterBoundsRateAndConcurrencyPerKey(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewLimiter(LimiterOptions{RequestsPerMinute: 2, Concurrent: 1, Now: func() time.Time { return now }})
	subject := LimitSubject{KeyID: "one", Source: "192.0.2.1", Action: ActionLog}
	release, ok := limiter.Acquire(subject)
	if !ok {
		t.Fatal("first request was rejected")
	}
	if _, ok := limiter.Acquire(subject); ok {
		t.Fatal("concurrent request was accepted")
	}
	release()
	release, ok = limiter.Acquire(subject)
	if !ok {
		t.Fatal("second request was rejected")
	}
	release()
	if _, ok := limiter.Acquire(subject); ok {
		t.Fatal("third request inside the minute was accepted")
	}
	now = now.Add(time.Minute)
	release, ok = limiter.Acquire(subject)
	if !ok {
		t.Fatal("request after window reset was rejected")
	}
	release()
}

func TestLimiterBoundsSourceAcrossDifferentKeys(t *testing.T) {
	limiter := NewLimiter(LimiterOptions{
		RequestsPerMinute: 100, Concurrent: 10, SourceRequestsPerMinute: 2, SourceConcurrent: 10,
		ActionRequestsPerMinute: 100, ActionConcurrent: 10, GlobalRequestsPerMinute: 100, GlobalConcurrent: 10,
	})
	for _, keyID := range []string{"one", "two"} {
		release, ok := limiter.Acquire(LimitSubject{KeyID: keyID, Source: "192.0.2.2", Action: ActionLog})
		if !ok {
			t.Fatalf("source request for %q was rejected early", keyID)
		}
		release()
	}
	if _, ok := limiter.Acquire(LimitSubject{KeyID: "three", Source: "192.0.2.2", Action: ActionUpload}); ok {
		t.Fatal("source bypassed its rate limit by changing keys and actions")
	}
}

func TestLimiterBoundsActionAcrossDifferentKeys(t *testing.T) {
	limiter := NewLimiter(LimiterOptions{
		RequestsPerMinute: 100, Concurrent: 10, SourceRequestsPerMinute: 100, SourceConcurrent: 10,
		ActionRequestsPerMinute: 1, ActionConcurrent: 10, GlobalRequestsPerMinute: 100, GlobalConcurrent: 10,
	})
	logSubject := LimitSubject{KeyID: "one", Source: "192.0.2.3", Action: ActionLog}
	release, ok := limiter.Acquire(logSubject)
	if !ok {
		t.Fatal("first action request was rejected")
	}
	release()
	if _, ok := limiter.Acquire(LimitSubject{KeyID: "two", Source: "192.0.2.4", Action: ActionLog}); ok {
		t.Fatal("action limit was bypassed with a different key and source")
	}
	release, ok = limiter.Acquire(LimitSubject{KeyID: "two", Source: "192.0.2.4", Action: ActionUpload})
	if !ok {
		t.Fatal("one action exhausted an unrelated action bucket")
	}
	release()
}

func TestLimiterAppliesGlobalCircuitLimit(t *testing.T) {
	limiter := NewLimiter(LimiterOptions{
		RequestsPerMinute: 100, Concurrent: 100, SourceRequestsPerMinute: 100, SourceConcurrent: 100,
		ActionRequestsPerMinute: 100, ActionConcurrent: 100, GlobalRequestsPerMinute: 2, GlobalConcurrent: 100,
	})
	for index, subject := range []LimitSubject{
		{KeyID: "one", Source: "192.0.2.4", Action: ActionLog},
		{KeyID: "two", Source: "192.0.2.5", Action: ActionUpload},
	} {
		release, ok := limiter.Acquire(subject)
		if !ok {
			t.Fatalf("global request %d was rejected early", index+1)
		}
		release()
	}
	if _, ok := limiter.Acquire(LimitSubject{KeyID: "three", Source: "192.0.2.6", Action: ActionQuickRun}); ok {
		t.Fatal("global request ceiling was bypassed")
	}
}

func TestLimiterBoundsSubjectCardinalityAndCleansExpiredState(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewLimiter(LimiterOptions{
		RequestsPerMinute: 100, Concurrent: 100, SourceRequestsPerMinute: 100, SourceConcurrent: 100,
		ActionRequestsPerMinute: 100, ActionConcurrent: 100, GlobalRequestsPerMinute: 100, GlobalConcurrent: 100,
		MaxSubjects: 4, Now: func() time.Time { return now },
	})
	first := LimitSubject{KeyID: "one", Source: "192.0.2.7", Action: ActionLog}
	release, ok := limiter.Acquire(first)
	if !ok {
		t.Fatal("first bounded subject was rejected")
	}
	release()
	if _, ok := limiter.Acquire(LimitSubject{KeyID: "two", Source: "192.0.2.8", Action: ActionLog}); ok {
		t.Fatal("subject cardinality ceiling was bypassed")
	}
	now = now.Add(time.Minute)
	release, ok = limiter.Acquire(LimitSubject{KeyID: "two", Source: "192.0.2.8", Action: ActionLog})
	if !ok {
		t.Fatal("expired inactive subjects were not cleaned")
	}
	release()
}
