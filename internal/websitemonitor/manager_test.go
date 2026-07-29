package websitemonitor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

func TestCreateImmediatelyChecksHTTPMonitor(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != UserAgent {
			t.Errorf("user agent = %q, want %q", request.UserAgent(), UserAgent)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	manager := newTestManager(t, Options{})
	created, err := manager.Create(context.Background(), Config{
		Name:  "本机管理入口",
		Scope: ScopeLocal,
		Kind:  KindHTTP,
		URL:   target.URL,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	monitor := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateUp
	})
	if monitor.Latest.StatusCode != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", monitor.Latest.StatusCode, http.StatusNoContent)
	}
	if monitor.Latest.CheckedAt.IsZero() || monitor.Latest.Latency < 0 {
		t.Fatalf("latest evidence = %#v", monitor.Latest)
	}
}

func TestTwoConsecutiveFailuresConfirmAnIncident(t *testing.T) {
	probe := &sequenceProbe{results: []Evidence{
		{ErrorCategory: "connect", Summary: "网站拒绝连接"},
		{ErrorCategory: "connect", Summary: "网站仍然拒绝连接"},
	}}
	manager := newTestManager(t, Options{
		Probe:      probe,
		Tick:       5 * time.Millisecond,
		RetryDelay: 20 * time.Millisecond,
	})
	created, err := manager.Create(context.Background(), Config{
		Name:  "媒体代理",
		Scope: ScopeLocal,
		Kind:  KindHTTP,
		URL:   "http://127.0.0.1:1/",
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	verifying := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateVerifying
	})
	if verifying.FailureCount != 1 {
		t.Fatalf("first failure count = %d, want 1", verifying.FailureCount)
	}

	down := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateDown
	})
	if down.FailureCount != 2 || down.Latest.Summary != "网站仍然拒绝连接" {
		t.Fatalf("confirmed failure = %#v", down)
	}
	incidents, err := manager.Incidents(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 1 || !incidents[0].EndedAt.IsZero() {
		t.Fatalf("incidents = %#v, want one open incident", incidents)
	}
}

func TestPingPongRequiresAMatchingPongControlFrame(t *testing.T) {
	t.Run("matching pong succeeds", func(t *testing.T) {
		server := newWebSocketServer(t, nil)
		manager := newTestManager(t, Options{})
		created, err := manager.Create(context.Background(), Config{
			Name:              "实时通知通道",
			Scope:             ScopeExternal,
			Kind:              KindWebSocket,
			URL:               websocketURL(server.URL),
			Timeout:           time.Second,
			WebSocketSuccess:  WebSocketPingPong,
			PingPayloadFormat: PayloadHex,
			PingPayload:       "00 ff 68 69",
		})
		if err != nil {
			t.Fatalf("create ping/pong monitor: %v", err)
		}
		monitor := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
			return value.State == StateUp
		})
		if monitor.Latest.Summary != "Pong 载荷与 Ping 完全一致" {
			t.Fatalf("summary = %q", monitor.Latest.Summary)
		}
	})

	t.Run("text message with the same bytes is not a pong", func(t *testing.T) {
		server := newWebSocketServer(t, func(connection *websocket.Conn) {
			connection.SetPingHandler(func(payload string) error {
				return connection.WriteMessage(websocket.TextMessage, []byte(payload))
			})
		})
		manager := newTestManager(t, Options{})
		created, err := manager.Create(context.Background(), Config{
			Name:              "错误的活性通道",
			Scope:             ScopeExternal,
			Kind:              KindWebSocket,
			URL:               websocketURL(server.URL),
			Timeout:           100 * time.Millisecond,
			WebSocketSuccess:  WebSocketPingPong,
			PingPayloadFormat: PayloadText,
			PingPayload:       "health",
		})
		if err != nil {
			t.Fatalf("create ping/pong monitor: %v", err)
		}
		monitor := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
			return value.State == StateVerifying
		})
		if monitor.Latest.ErrorCategory != "timeout" {
			t.Fatalf("text frame result = %#v, want timeout waiting for Pong", monitor.Latest)
		}
	})
}

func TestPingPongValidatesOpaqueControlFramePayload(t *testing.T) {
	manager := newTestManager(t, Options{Probe: &sequenceProbe{results: []Evidence{{Success: true}}}})
	tests := []struct {
		name    string
		format  PayloadFormat
		payload string
	}{
		{name: "invalid hex", format: PayloadHex, payload: "0f f"},
		{name: "invalid base64", format: PayloadBase64, payload: "%%%"},
		{name: "more than 125 decoded bytes", format: PayloadText, payload: strings.Repeat("界", 42)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.Create(context.Background(), Config{
				Name:              test.name,
				Scope:             ScopeExternal,
				Kind:              KindWebSocket,
				URL:               "ws://127.0.0.1:1/",
				WebSocketSuccess:  WebSocketPingPong,
				PingPayloadFormat: test.format,
				PingPayload:       test.payload,
			})
			if err == nil {
				t.Fatal("invalid Ping payload was accepted")
			}
		})
	}

	_, err := manager.Create(context.Background(), Config{
		Name:              "最大合法 Ping",
		Scope:             ScopeExternal,
		Kind:              KindWebSocket,
		URL:               "ws://127.0.0.1:1/",
		WebSocketSuccess:  WebSocketPingPong,
		PingPayloadFormat: PayloadBase64,
		PingPayload:       base64.StdEncoding.EncodeToString(make([]byte, 125)),
	})
	if err != nil {
		t.Fatalf("125-byte Ping payload rejected: %v", err)
	}
}

func TestConfigurationRejectsInvalidProtocolRules(t *testing.T) {
	manager := newTestManager(t, Options{Probe: &sequenceProbe{results: []Evidence{{Success: true}}}})
	tests := []Config{
		{Name: "无效 HTTP 模式", Kind: KindHTTP, URL: "https://example.com/", HTTPSuccessMode: "anything"},
		{Name: "空状态码", Kind: KindHTTP, URL: "https://example.com/", HTTPSuccessMode: HTTPSuccessExact},
		{Name: "无效 WS 模式", Kind: KindWebSocket, URL: "wss://example.com/", WebSocketSuccess: "anything"},
		{Name: "空匹配消息", Kind: KindWebSocket, URL: "wss://example.com/", WebSocketSuccess: WebSocketMatchingMessage, ReceiveType: MessageText},
		{Name: "无效二进制期望", Kind: KindWebSocket, URL: "wss://example.com/", WebSocketSuccess: WebSocketMatchingMessage, ReceiveType: MessageBinary, ExpectedMessage: "%%%"},
		{Name: "无效二进制发送", Kind: KindWebSocket, URL: "wss://example.com/", WebSocketSuccess: WebSocketAnyMessage, SendType: MessageBinary, SendPayload: "%%%"},
	}
	for _, config := range tests {
		t.Run(config.Name, func(t *testing.T) {
			if _, err := manager.Create(context.Background(), config); err == nil {
				t.Fatalf("invalid configuration was accepted: %#v", config)
			}
		})
	}
}

func TestListKeepsAdministratorOrderWhenStatesChange(t *testing.T) {
	manager := newTestManager(t, Options{Probe: probeFunc(func(_ context.Context, config Config) Evidence {
		if config.Name == "第二个网站" {
			return Evidence{ErrorCategory: "http-status", Summary: "网站返回 HTTP 503"}
		}
		return Evidence{Success: true, StatusCode: http.StatusOK, Summary: "网站返回 HTTP 200"}
	})})
	first, err := manager.Create(context.Background(), Config{
		Name: "第一个网站", Scope: ScopeExternal, Kind: KindHTTP, URL: "https://first.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(context.Background(), Config{
		Name: "第二个网站", Scope: ScopeLocal, Kind: KindHTTP, URL: "http://second.local/",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForMonitor(t, manager, first.ID, func(value Monitor) bool { return value.State == StateUp })
	waitForMonitor(t, manager, second.ID, func(value Monitor) bool { return value.State == StateVerifying })

	monitors, err := manager.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 2 || monitors[0].ID != first.ID || monitors[1].ID != second.ID {
		t.Fatalf("state change rewrote administrator order: %#v", monitors)
	}
	if err := manager.Move(context.Background(), second.ID, -1); err != nil {
		t.Fatalf("move second monitor up: %v", err)
	}
	monitors, err = manager.List(context.Background(), Filter{Scope: ScopeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 1 || monitors[0].ID != second.ID {
		t.Fatalf("scope filter = %#v", monitors)
	}
	all, err := manager.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("saved order = %#v", all)
	}
}

func TestPauseIgnoresALateInFlightResultAndResumeChecksAgain(t *testing.T) {
	probe := &blockingProbe{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	manager := newTestManager(t, Options{Probe: probe})
	created, err := manager.Create(context.Background(), Config{
		Name: "维护中的网站", Scope: ScopeLocal, Kind: KindHTTP, URL: "http://maintenance.local/",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-probe.started:
	case <-time.After(time.Second):
		t.Fatal("initial check did not start")
	}
	if err := manager.Pause(context.Background(), created.ID); err != nil {
		t.Fatalf("pause monitor: %v", err)
	}
	close(probe.release)
	time.Sleep(30 * time.Millisecond)
	paused, err := manager.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != StatePaused || !paused.Latest.CheckedAt.IsZero() {
		t.Fatalf("late result changed paused monitor: %#v", paused)
	}

	if err := manager.Resume(context.Background(), created.ID); err != nil {
		t.Fatalf("resume monitor: %v", err)
	}
	resumed := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateUp
	})
	if resumed.Latest.Summary != "恢复后的检查成功" {
		t.Fatalf("resume result = %#v", resumed.Latest)
	}
}

func TestLocalDialOverridePreservesHostAcrossSameHostRedirect(t *testing.T) {
	var wantHost string
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Host != wantHost {
			t.Errorf("Host = %q, want %q", request.Host, wantHost)
		}
		if request.URL.Path == "/" {
			http.Redirect(response, request, "/ready", http.StatusFound)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	parsed, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	wantHost = "service.internal:" + parsed.Port()

	manager := newTestManager(t, Options{})
	created, err := manager.Create(context.Background(), Config{
		Name:     "本机虚拟主机",
		Scope:    ScopeLocal,
		Kind:     KindHTTP,
		URL:      "http://" + wantHost + "/",
		DialHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	monitor := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateUp
	})
	if monitor.Latest.StatusCode != http.StatusNoContent {
		t.Fatalf("local redirect result = %#v", monitor.Latest)
	}
}

func TestChecksRespectTheGlobalConcurrencyLimit(t *testing.T) {
	probe := &gateProbe{
		started: make(chan string, 5),
		release: make(chan struct{}),
	}
	manager := newTestManager(t, Options{Probe: probe, MaxConcurrency: 2})
	var monitors []Monitor
	for index := range 5 {
		monitor, err := manager.Create(context.Background(), Config{
			Name:  fmt.Sprintf("并发检查 %d", index),
			Scope: ScopeExternal,
			Kind:  KindHTTP,
			URL:   fmt.Sprintf("https://concurrency-%d.example/", index),
		})
		if err != nil {
			t.Fatal(err)
		}
		monitors = append(monitors, monitor)
	}
	for range 2 {
		select {
		case <-probe.started:
		case <-time.After(time.Second):
			t.Fatal("expected two checks to start")
		}
	}
	select {
	case name := <-probe.started:
		t.Fatalf("check %q exceeded the concurrency limit", name)
	case <-time.After(50 * time.Millisecond):
	}
	close(probe.release)
	for _, monitor := range monitors {
		waitForMonitor(t, manager, monitor.ID, func(value Monitor) bool {
			return value.State == StateUp
		})
	}
	if maximum := probe.maximum(); maximum != 2 {
		t.Fatalf("maximum concurrent checks = %d, want 2", maximum)
	}
}

func TestUpdateInvalidatesInFlightResultsAndChecksTheNewConfiguration(t *testing.T) {
	probe := &editingProbe{
		firstStarted: make(chan struct{}),
		firstRelease: make(chan struct{}),
	}
	manager := newTestManager(t, Options{Probe: probe})
	created, err := manager.Create(context.Background(), Config{
		Name: "原网站", Scope: ScopeExternal, Kind: KindHTTP, URL: "https://old.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-probe.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("initial check did not start")
	}
	updated, err := manager.Update(context.Background(), created.ID, Config{
		Name: "新网站", Scope: ScopeExternal, Kind: KindHTTP, URL: "https://new.example/",
	})
	if err != nil {
		t.Fatalf("update monitor: %v", err)
	}
	if updated.Config.Name != "新网站" || updated.Config.URL != "https://new.example/" {
		t.Fatalf("updated config = %#v", updated.Config)
	}
	close(probe.firstRelease)
	current := waitForMonitor(t, manager, created.ID, func(value Monitor) bool {
		return value.State == StateUp && value.Latest.Summary == "新配置检查成功"
	})
	if current.Latest.StatusCode != http.StatusNoContent {
		t.Fatalf("new configuration evidence = %#v", current.Latest)
	}
}

func TestReorderRequiresEveryActiveMonitorExactlyOnce(t *testing.T) {
	manager := newTestManager(t, Options{Probe: &sequenceProbe{results: []Evidence{{Success: true}}}})
	first, _ := manager.Create(context.Background(), Config{Name: "甲", Kind: KindHTTP, URL: "https://a.example/"})
	second, _ := manager.Create(context.Background(), Config{Name: "乙", Kind: KindHTTP, URL: "https://b.example/"})
	third, _ := manager.Create(context.Background(), Config{Name: "丙", Kind: KindHTTP, URL: "https://c.example/"})

	if err := manager.Reorder(context.Background(), []string{third.ID, first.ID, second.ID}); err != nil {
		t.Fatalf("reorder monitors: %v", err)
	}
	ordered, err := manager.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].ID != third.ID || ordered[1].ID != first.ID || ordered[2].ID != second.ID {
		t.Fatalf("order = %#v", ordered)
	}
	if err := manager.Reorder(context.Background(), []string{first.ID, first.ID, third.ID}); err == nil {
		t.Fatal("duplicate monitor ID was accepted")
	}
}

func TestAvailabilityUsesPersistedChecksAndMaintainsBoundedHistory(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 17, 0, 0, time.UTC)
	manager := newTestManager(t, Options{
		Now: func() time.Time { return now },
		Probe: probeFunc(func(context.Context, Config) Evidence {
			return Evidence{Success: true, StatusCode: http.StatusOK, Latency: 20 * time.Millisecond}
		}),
	})
	created, err := manager.Create(context.Background(), Config{
		Name: "历史网站", Kind: KindHTTP, URL: "https://history.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForMonitor(t, manager, created.ID, func(value Monitor) bool { return value.State == StateUp })

	buckets, err := manager.Availability24h(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 48 || buckets[47] != AvailabilityUp {
		t.Fatalf("availability = %#v", buckets)
	}
	var total, successful, average int
	if err := manager.db.QueryRow(`SELECT total_checks, successful_checks, average_latency_ms
		FROM website_hourly_aggregates WHERE monitor_id = ?`, created.ID).
		Scan(&total, &successful, &average); err != nil {
		t.Fatalf("read hourly aggregate: %v", err)
	}
	if total != 1 || successful != 1 || average != 20 {
		t.Fatalf("aggregate total=%d successful=%d average=%d", total, successful, average)
	}

	oldRaw := now.Add(-25 * time.Hour).UnixNano()
	oldAggregate := now.Add(-31 * 24 * time.Hour).Truncate(time.Hour).UnixNano()
	if _, err := manager.db.Exec(`INSERT INTO website_check_results
		(monitor_id, checked_at, success) VALUES (?, ?, 0)`, created.ID, oldRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.db.Exec(`INSERT INTO website_hourly_aggregates
		(monitor_id, bucket_at, total_checks, successful_checks, failed_checks, average_latency_ms, maximum_latency_ms)
		VALUES (?, ?, 1, 0, 1, 10, 10)`, created.ID, oldAggregate); err != nil {
		t.Fatal(err)
	}
	if err := manager.Maintain(context.Background()); err != nil {
		t.Fatalf("maintain history: %v", err)
	}
	var oldResults, oldAggregates int
	_ = manager.db.QueryRow(`SELECT COUNT(*) FROM website_check_results WHERE checked_at < ?`,
		now.Add(-24*time.Hour).UnixNano()).Scan(&oldResults)
	_ = manager.db.QueryRow(`SELECT COUNT(*) FROM website_hourly_aggregates WHERE bucket_at < ?`,
		now.Add(-30*24*time.Hour).Truncate(time.Hour).UnixNano()).Scan(&oldAggregates)
	if oldResults != 0 || oldAggregates != 0 {
		t.Fatalf("retention left raw=%d aggregate=%d", oldResults, oldAggregates)
	}
}

type blockingProbe struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	calls   int
}

func (p *blockingProbe) Check(ctx context.Context, _ Config) Evidence {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	p.started <- struct{}{}
	if call == 1 {
		select {
		case <-p.release:
		case <-ctx.Done():
			return Evidence{ErrorCategory: "timeout", Summary: "请求已取消"}
		}
	}
	return Evidence{Success: true, StatusCode: http.StatusOK, Summary: "恢复后的检查成功"}
}

type gateProbe struct {
	mu        sync.Mutex
	active    int
	maxActive int
	started   chan string
	release   chan struct{}
}

func (p *gateProbe) Check(ctx context.Context, config Config) Evidence {
	p.mu.Lock()
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()
	p.started <- config.Name
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return Evidence{Success: true, StatusCode: http.StatusOK, Summary: "检查成功"}
}

func (p *gateProbe) maximum() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

type editingProbe struct {
	mu           sync.Mutex
	calls        int
	firstStarted chan struct{}
	firstRelease chan struct{}
}

func (p *editingProbe) Check(ctx context.Context, config Config) Evidence {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		close(p.firstStarted)
		select {
		case <-p.firstRelease:
		case <-ctx.Done():
		}
		return Evidence{Success: true, StatusCode: http.StatusOK, Summary: "旧配置检查成功"}
	}
	if config.URL != "https://new.example/" {
		return Evidence{ErrorCategory: "configuration", Summary: "检查使用了错误配置"}
	}
	return Evidence{Success: true, StatusCode: http.StatusNoContent, Summary: "新配置检查成功"}
}

func newWebSocketServer(t *testing.T, configure func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if configure != nil {
			configure(connection)
		}
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

type sequenceProbe struct {
	mu      sync.Mutex
	results []Evidence
	next    int
}

type probeFunc func(context.Context, Config) Evidence

func (function probeFunc) Check(ctx context.Context, config Config) Evidence {
	return function(ctx, config)
}

func (p *sequenceProbe) Check(_ context.Context, _ Config) Evidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.next
	if index >= len(p.results) {
		index = len(p.results) - 1
	} else {
		p.next++
	}
	return p.results[index]
}

func newTestManager(t *testing.T, options Options) *Manager {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("initialize schema: %v", err)
		}
	}
	manager, err := New(db, options)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() {
		manager.Close()
		_ = db.Close()
	})
	return manager
}

func waitForMonitor(t *testing.T, manager *Manager, id string, ready func(Monitor) bool) Monitor {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, err := manager.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get monitor: %v", err)
		}
		if ready(value) {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	value, err := manager.Get(context.Background(), id)
	t.Fatalf("monitor did not reach expected state: value=%#v err=%v", value, err)
	return Monitor{}
}
