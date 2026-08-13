package securityevents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scriptboard/internal/auditlog"
)

func TestManagerRejectsNonHTTPSRemoteEndpoint(t *testing.T) {
	if _, err := New(Options{StateRoot: t.TempDir(), Endpoint: "http://siem.example/events"}); err == nil {
		t.Fatal("HTTP security event endpoint was accepted")
	}
}

func TestAuthenticationBurstCreatesLocalAlert(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	manager, err := New(Options{StateRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for id := int64(1); id <= 5; id++ {
		manager.Observe(auditlog.CommittedEvent{ID: id, EventSHA256: "digest", Event: auditlog.Event{Action: "login", Result: "failed", SourceAddress: "192.0.2.10", ActorUsername: "admin"}})
	}
	body, err := os.ReadFile(filepath.Join(root, "logs", "security-alerts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Alerts) != 1 || envelope.Alerts[0].Kind != "authentication_failure_burst" || envelope.Alerts[0].WindowHits != 5 {
		t.Fatalf("alerts=%#v", envelope.Alerts)
	}
}

func TestDetectionSourceCardinalityIsBounded(t *testing.T) {
	manager, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for index := 0; index < maxDetectionKeys+100; index++ {
		manager.Observe(auditlog.CommittedEvent{ID: int64(index + 1), EventSHA256: "digest", Event: auditlog.Event{
			Action: "login", Result: "failed", SourceAddress: "source-" + strconv.Itoa(index), ActorUsername: "admin",
		}})
	}
	manager.mu.Lock()
	count := len(manager.windows)
	manager.mu.Unlock()
	if count > maxDetectionKeys+1 {
		t.Fatalf("detection keys=%d", count)
	}
}

func TestRemoteForwardingRetriesDurableOrderedOutbox(t *testing.T) {
	var attempts atomic.Int32
	received := make(chan Envelope, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer receiver-token" {
			http.Error(response, "missing token", http.StatusUnauthorized)
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(response, "retry", http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(request.Body)
		var envelope Envelope
		_ = json.Unmarshal(body, &envelope)
		received <- envelope
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := server.Client()
	root := t.TempDir()
	manager, err := New(Options{StateRoot: root, Endpoint: server.URL, Token: "receiver-token", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	manager.Observe(auditlog.CommittedEvent{ID: 7, EventSHA256: "abc", Event: auditlog.Event{Action: "login", Result: "succeeded"}})
	select {
	case envelope := <-received:
		if envelope.Audit.ID != 7 || envelope.Audit.EventSHA256 != "abc" {
			t.Fatalf("envelope=%#v", envelope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("security event was not retried")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(root, "security-events", "outbox"))
		if len(entries) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	manager.Close()
	entries, err := os.ReadDir(filepath.Join(root, "security-events", "outbox"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("outbox entries=%d err=%v", len(entries), err)
	}
}

func TestStatusExposesOnlyEndpointHostAndBoundedOutboxState(t *testing.T) {
	root := t.TempDir()
	manager, err := New(Options{StateRoot: root, Endpoint: "https://notify.example:8443/events?tenant=secret", Token: "must-not-be-exposed",
		BrokerEmailRelayEndpoint: "https://mail.example:9443/send?tenant=hidden", BrokerEmailRecipient: "administrator@example.com",
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Observe(auditlog.CommittedEvent{ID: 1, EventSHA256: "abc", Event: auditlog.Event{Action: "state_backup.create", Result: "succeeded"}})
	status, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.WebhookEnabled || status.EndpointHost != "notify.example:8443" || status.Pending != 1 || status.Capacity != maxPendingEvents || !status.WebsiteTemplates ||
		!status.EmailEnabled || status.EmailRelayHost != "mail.example:9443" || status.EmailRecipient != "a***@example.com" || !status.EmailTemplates {
		t.Fatalf("notification status = %#v", status)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "tenant=secret") || strings.Contains(string(encoded), "tenant=hidden") || strings.Contains(string(encoded), "must-not-be-exposed") || strings.Contains(string(encoded), "administrator@example.com") {
		t.Fatalf("status exposed endpoint query or token: %s", encoded)
	}
}

func TestWebsiteTransitionsUseBoundedStructuredNotificationTemplate(t *testing.T) {
	event := auditlog.CommittedEvent{ID: 9, EventSHA256: "digest", Event: auditlog.Event{Action: "website_monitor_down", Target: "monitor-safe-id", Result: "failed"}}
	notification := NotificationFor(event)
	if notification == nil || notification.Template != "website-monitor-result-v1" || notification.State != "down" || notification.ResourceID != "monitor-safe-id" {
		t.Fatalf("notification=%#v", notification)
	}
	manager, err := New(Options{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Observe(event)
	body, err := os.ReadFile(filepath.Join(manager.alertLog))
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Notification == nil || envelope.Notification.State != "down" || len(envelope.Alerts) != 1 {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
}

func TestRunTemplateOnlyAcceptsTerminalRunAudit(t *testing.T) {
	run := NotificationFor(auditlog.CommittedEvent{Event: auditlog.Event{Action: "run_completed", Target: "run-safe-id", Result: "timed_out"}})
	if run == nil || run.Template != "run-result-v1" || run.Severity != "high" {
		t.Fatalf("run notification=%#v", run)
	}
	runtimeFailure := NotificationFor(auditlog.CommittedEvent{Event: auditlog.Event{Action: "assistant_runtime_install", Target: "runtime", Result: "failed"}})
	if runtimeFailure == nil || runtimeFailure.Template != "security-alert-v1" {
		t.Fatalf("runtime failure was misclassified: %#v", runtimeFailure)
	}
}

func TestNotificationOnlyEmailChannelSendsFixedTemplateAndRecipient(t *testing.T) {
	requests := make(chan *http.Request, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.Clone(request.Context())
		body := io.NopCloser(strings.NewReader("ok"))
		return &http.Response{StatusCode: http.StatusNoContent, Body: body, Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	manager, err := New(Options{StateRoot: root, Endpoint: "https://mail.example/send", Token: "broker-secret", Client: client,
		Channel: "email-outbox", EnvelopeType: "scriptboard.email-notification", Recipient: "admin@example.com", NotificationsOnly: true, DisableLocalAlerts: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Queue(auditlog.CommittedEvent{ID: 1, EventSHA256: "ignored", Event: auditlog.Event{Action: "page_view", Result: "success"}}); err != nil {
		t.Fatal(err)
	}
	if pending, err := manager.Pending(); err != nil || pending != 0 {
		t.Fatalf("non-notification pending=%d err=%v", pending, err)
	}
	if err := manager.Queue(auditlog.CommittedEvent{ID: 2, EventSHA256: "safe-digest", Event: auditlog.Event{Action: "state_backup.create", Target: "backup-safe-id", Result: "succeeded"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if request.Header.Get("Authorization") != "Bearer broker-secret" {
			t.Fatalf("authorization header=%q", request.Header.Get("Authorization"))
		}
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var envelope Envelope
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Type != "scriptboard.email-notification" || envelope.Recipient != "admin@example.com" || envelope.Notification == nil || envelope.Notification.Template != "state-backup-result-v1" {
			t.Fatalf("email envelope=%#v err=%v", envelope, err)
		}
	case <-time.After(time.Second):
		t.Fatal("email notification was not delivered")
	}
}

func TestDeliveryRetryDelayOpensCircuitAfterBoundedFailures(t *testing.T) {
	if delay := deliveryRetryDelay(1); delay != time.Second {
		t.Fatalf("first retry delay = %s", delay)
	}
	if delay := deliveryRetryDelay(circuitThreshold - 1); delay != time.Minute {
		t.Fatalf("bounded retry delay = %s", delay)
	}
	if delay := deliveryRetryDelay(circuitThreshold); delay != circuitOpenFor {
		t.Fatalf("circuit-open delay = %s", delay)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
