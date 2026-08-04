package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApplicationShellCondensesAQuietHostToOneAttentionSummary(t *testing.T) {
	var rendered bytes.Buffer
	err := applicationShellTemplate.Execute(&rendered, applicationShellData{
		Locale:       localeEnglishUS,
		Environment:  "Local",
		Status:       "Data current",
		StatusState:  "current",
		WebsiteState: "up",
	})
	if err != nil {
		t.Fatal(err)
	}

	page := rendered.String()
	for _, expected := range []string{
		`aria-label="Current status"`,
		`data-shell-attention`,
		`data-shell-attention-empty`,
		`data-shell-attention-item="host" hidden`,
		`data-shell-attention-item="runs" hidden`,
		`data-shell-attention-item="websites" hidden`,
		`data-shell-attention-item="applications" hidden`,
	} {
		if !bytes.Contains(rendered.Bytes(), []byte(expected)) {
			t.Fatalf("quiet attention summary is missing %q: %s", expected, page)
		}
	}
	if bytes.Contains(rendered.Bytes(), []byte(`sidebar-attention__head`)) || bytes.Contains(rendered.Bytes(), []byte(`<strong>Current status</strong>`)) {
		t.Fatalf("quiet attention summary still renders the redundant current-status row: %s", page)
	}
}

func TestApplicationShellShowsOnlyCurrentAttentionItems(t *testing.T) {
	var rendered bytes.Buffer
	err := applicationShellTemplate.Execute(&rendered, applicationShellData{
		Locale:                    localeEnglishUS,
		Environment:               "Remote",
		Status:                    "Data stale",
		StatusState:               "stale",
		ActiveRuns:                2,
		WebsiteState:              "down",
		WebsiteDown:               1,
		WebsiteVerifying:          3,
		StoppedPinnedApplications: 4,
		ApplicationIssueCount:     2,
	})
	if err != nil {
		t.Fatal(err)
	}

	page := rendered.String()
	for _, expected := range []string{
		`data-shell-attention-empty hidden`,
		`data-shell-attention-item="host"`,
		`data-shell-attention-item="runs"`,
		`data-shell-attention-item="websites"`,
		`data-shell-attention-item="applications"`,
		`2 active Runs`,
		`1 website down`,
		`Stopped pinned applications 4`,
		`Application data is temporarily unavailable 2`,
	} {
		if !bytes.Contains(rendered.Bytes(), []byte(expected)) {
			t.Fatalf("attention summary is missing %q: %s", expected, page)
		}
	}
	for _, item := range []string{"host", "runs", "websites", "applications"} {
		if bytes.Contains(rendered.Bytes(), []byte(`data-shell-attention-item="`+item+`" hidden`)) {
			t.Fatalf("attention item %q is unexpectedly hidden: %s", item, page)
		}
	}
}

func TestCollapsedApplicationShellKeepsNavigationTopAlignedAndShowsIssueCount(t *testing.T) {
	var rendered bytes.Buffer
	err := applicationShellTemplate.Execute(&rendered, applicationShellData{
		Locale:            localeEnglishUS,
		Status:            "Attention needed",
		StatusState:       "attention",
		CurrentErrorCount: 3,
		WebsiteState:      "up",
	})
	if err != nil {
		t.Fatal(err)
	}

	page := rendered.String()
	for _, expected := range []string{
		`data-shell-issue-summary`,
		`data-state="attention"`,
		`data-shell-issue-count>3<`,
		`aria-label="Current errors: 3"`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("collapsed issue summary is missing %q: %s", expected, page)
		}
	}

	stylesheet, err := webFiles.ReadFile("web/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	if !strings.Contains(css, `body.sidebar-collapsed .sidebar-nav { display: flex; flex-direction: column; justify-content: flex-start;`) {
		t.Fatalf("collapsed navigation does not retain the expanded top alignment")
	}
	if strings.Contains(css, `body.sidebar-collapsed .sidebar-attention,`) {
		t.Fatalf("collapsed navigation hides the complete attention region, including its issue count")
	}
	if !strings.Contains(css, `.sidebar-attention__compact strong[hidden] { display: none; }`) {
		t.Fatal("collapsed navigation does not honor the hidden state of a zero-error badge")
	}

	script, err := webFiles.ReadFile("web/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, expected := range []string{
		`attention.querySelector("[data-shell-issue-summary]")`,
		`Number(data.issueCount)`,
		`Number(data.websiteDown)`,
		`Number(data.stoppedPinnedApplications)`,
		`Number(data.applicationIssueCount)`,
		`issueCount.textContent = String(currentIssueCount)`,
		`issueCount.hidden = currentIssueCount === 0`,
		`issueSummary.setAttribute("aria-label", label)`,
	} {
		if !strings.Contains(js, expected) {
			t.Fatalf("live issue summary update is missing %q", expected)
		}
	}
}

func TestCollapsedApplicationShellHidesZeroErrorBadge(t *testing.T) {
	var rendered bytes.Buffer
	if err := applicationShellTemplate.Execute(&rendered, applicationShellData{
		Locale:            localeEnglishUS,
		Status:            "Data current",
		StatusState:       "current",
		CurrentErrorCount: 0,
		WebsiteState:      "up",
	}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(rendered.String(), `<strong data-shell-issue-count hidden>0</strong>`) {
		t.Fatalf("zero-error collapsed summary still renders a visible badge: %s", rendered.String())
	}
}

func TestCurrentShellErrorCountIncludesConfirmedHostWebsiteAndApplicationErrors(t *testing.T) {
	status := shellStatusResponse{
		IssueCount:                2,
		WebsiteDown:               1,
		WebsiteVerifying:          7,
		StoppedPinnedApplications: 3,
		ApplicationIssueCount:     4,
	}

	if got := currentShellErrorCount(status); got != 10 {
		t.Fatalf("current error count=%d, want 10 confirmed errors", got)
	}
}

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
