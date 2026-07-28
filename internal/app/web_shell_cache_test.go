package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestShellStatusCacheReusesValueWithinFiveSeconds(t *testing.T) {
	now := time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC)
	loads := 0
	cache := newShellStatusCache(5*time.Second, func() time.Time { return now }, func(context.Context) (shellStatusResponse, error) {
		loads++
		return shellStatusResponse{State: "current", ActiveRuns: loads}, nil
	})

	first, err := cache.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	second, err := cache.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if loads != 1 {
		t.Fatalf("status loads=%d, want 1 within the cache lifetime", loads)
	}
	if first != second {
		t.Fatalf("cached status changed: first=%+v second=%+v", first, second)
	}
}

func TestShellStatusCacheCoalescesConcurrentRefreshes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	loads := 0
	var loadMu sync.Mutex
	cache := newShellStatusCache(5*time.Second, time.Now, func(context.Context) (shellStatusResponse, error) {
		loadMu.Lock()
		loads++
		if loads == 1 {
			close(started)
		}
		loadMu.Unlock()
		<-release
		return shellStatusResponse{State: "current", ActiveRuns: 2}, nil
	})

	const readers = 8
	results := make(chan shellStatusResponse, readers)
	errors := make(chan error, readers)
	for range readers {
		go func() {
			value, err := cache.Read(context.Background())
			results <- value
			errors <- err
		}()
	}
	<-started
	close(release)

	for range readers {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.ActiveRuns != 2 {
			t.Fatalf("active runs=%d, want 2", result.ActiveRuns)
		}
	}
	loadMu.Lock()
	defer loadMu.Unlock()
	if loads != 1 {
		t.Fatalf("status loads=%d, want 1 for concurrent readers", loads)
	}
}

func TestShellStatusCacheRefreshesAtFiveSeconds(t *testing.T) {
	now := time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC)
	loads := 0
	cache := newShellStatusCache(5*time.Second, func() time.Time { return now }, func(context.Context) (shellStatusResponse, error) {
		loads++
		return shellStatusResponse{State: "current", ActiveRuns: loads}, nil
	})

	first, err := cache.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	second, err := cache.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if loads != 2 {
		t.Fatalf("status loads=%d, want 2 after the cache lifetime", loads)
	}
	if first.ActiveRuns != 1 || second.ActiveRuns != 2 {
		t.Fatalf("status did not refresh: first=%+v second=%+v", first, second)
	}
}

func TestShellStatusCacheSharesConcurrentRefreshFailure(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	loads := 0
	loadErr := errors.New("status unavailable")
	cache := newShellStatusCache(5*time.Second, time.Now, func(context.Context) (shellStatusResponse, error) {
		loads++
		if loads == 1 {
			close(started)
		}
		<-release
		return shellStatusResponse{}, loadErr
	})

	firstError := make(chan error, 1)
	go func() {
		_, err := cache.Read(context.Background())
		firstError <- err
	}()
	<-started

	waiting := &observedDoneContext{Context: context.Background(), observed: make(chan struct{})}
	secondError := make(chan error, 1)
	go func() {
		_, err := cache.Read(waiting)
		secondError <- err
	}()
	<-waiting.observed
	close(release)

	if err := <-firstError; !errors.Is(err, loadErr) {
		t.Fatalf("first error=%v, want %v", err, loadErr)
	}
	if err := <-secondError; !errors.Is(err, loadErr) {
		t.Fatalf("second error=%v, want %v", err, loadErr)
	}
	if loads != 1 {
		t.Fatalf("status loads=%d, want 1 when a concurrent refresh fails", loads)
	}
}
