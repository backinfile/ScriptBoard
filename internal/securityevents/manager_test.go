package securityevents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
