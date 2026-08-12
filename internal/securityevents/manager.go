package securityevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/outboundpolicy"
)

const (
	maxPendingEvents = 10_000
	maxResponseBytes = 32 << 10
	maxAlertLogBytes = 10 << 20
	maxDetectionKeys = 8192
	circuitThreshold = 8
	circuitOpenFor   = 5 * time.Minute
)

type Options struct {
	StateRoot    string
	Endpoint     string
	Token        string
	AllowPrivate bool
	Client       *http.Client
	Now          func() time.Time
}

type Alert struct {
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
	WindowHits int    `json:"window_hits,omitempty"`
}

type Envelope struct {
	Type          string                  `json:"type"`
	SchemaVersion int                     `json:"schema_version"`
	SentAt        string                  `json:"sent_at"`
	Audit         auditlog.CommittedEvent `json:"audit"`
	Alerts        []Alert                 `json:"alerts,omitempty"`
}

type Status struct {
	WebhookEnabled   bool
	EndpointHost     string
	Pending          int
	Capacity         int
	LocalAlerts      bool
	LocalAlertBytes  int64
	DeliveryFailures int
	CircuitOpen      bool
	NextAttemptAt    time.Time
}

type Manager struct {
	endpoint         string
	token            string
	client           *http.Client
	now              func() time.Time
	spool            string
	alertLog         string
	wake             chan struct{}
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	mu               sync.Mutex
	windows          map[string][]time.Time
	deliveryFailures int
	nextAttemptAt    time.Time
}

func New(options Options) (*Manager, error) {
	root, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint != "" {
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return nil, errors.New("security event endpoint must be an HTTPS URL without credentials or fragment")
		}
	}
	client := options.Client
	if client == nil {
		client = &http.Client{
			Transport:     outboundpolicy.Policy{AllowPrivate: options.AllowPrivate}.Transport(),
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logs := filepath.Join(root, "logs")
	spool := filepath.Join(root, "security-events", "outbox")
	for _, directory := range []string{logs, spool} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		_ = os.Chmod(directory, 0o700)
	}
	manager := &Manager{
		endpoint: endpoint, token: strings.TrimSpace(options.Token), client: client, now: now,
		spool: spool, alertLog: filepath.Join(logs, "security-alerts.jsonl"), wake: make(chan struct{}, 1), windows: make(map[string][]time.Time),
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.cancel = cancel
	if endpoint != "" {
		manager.wg.Add(1)
		go manager.run(ctx)
		manager.signal()
	}
	return manager, nil
}

func (manager *Manager) Observe(event auditlog.CommittedEvent) {
	now := manager.now().UTC()
	alerts := manager.detect(event, now)
	envelope := Envelope{Type: "scriptboard.security-event", SchemaVersion: 1, SentAt: now.Format(time.RFC3339Nano), Audit: event, Alerts: alerts}
	if len(alerts) > 0 {
		_ = manager.appendAlerts(envelope)
	}
	if manager.endpoint == "" {
		return
	}
	if manager.persist(envelope) == nil {
		manager.signal()
	}
}

func (manager *Manager) Close() error {
	if manager == nil {
		return nil
	}
	manager.cancel()
	manager.wg.Wait()
	return nil
}

// Pending returns the number of durable events waiting for a remote receiver.
func (manager *Manager) Pending() (int, error) {
	entries, err := os.ReadDir(manager.spool)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count, nil
}

func (manager *Manager) Status() (Status, error) {
	pending, err := manager.Pending()
	if err != nil {
		return Status{}, err
	}
	status := Status{WebhookEnabled: manager.endpoint != "", Pending: pending, Capacity: maxPendingEvents}
	manager.mu.Lock()
	status.DeliveryFailures = manager.deliveryFailures
	status.NextAttemptAt = manager.nextAttemptAt
	status.CircuitOpen = manager.deliveryFailures >= circuitThreshold && manager.now().Before(manager.nextAttemptAt)
	manager.mu.Unlock()
	if manager.endpoint != "" {
		if parsed, parseErr := url.Parse(manager.endpoint); parseErr == nil {
			status.EndpointHost = parsed.Host
		}
	}
	if info, statErr := os.Stat(manager.alertLog); statErr == nil && info.Mode().IsRegular() {
		status.LocalAlerts = true
		status.LocalAlertBytes = info.Size()
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Status{}, statErr
	}
	return status, nil
}

func (manager *Manager) detect(event auditlog.CommittedEvent, now time.Time) []Alert {
	action := strings.ToLower(event.Event.Action)
	result := strings.ToLower(event.Event.Result)
	failed := result == "failed" || result == "rejected" || result == "blocked" || result == "rate_limited"
	var alerts []Alert
	if failed && strings.Contains(action, "signature") {
		alerts = append(alerts, Alert{Kind: "signature_validation_failure", Severity: "critical", Summary: "A cryptographic signature validation failed"})
	}
	if failed && (strings.Contains(action, "runner") || strings.Contains(action, "runtime_sandbox")) {
		alerts = append(alerts, Alert{Kind: "execution_boundary_denial", Severity: "high", Summary: "An isolated execution boundary denied an operation"})
	}
	if (action == "login" || action == "step_up_authentication") && failed {
		key := "auth\x00" + event.Event.SourceAddress + "\x00" + event.Event.ActorUsername
		if hits := manager.windowHit(key, now, 5*time.Minute); hits == 5 || hits%10 == 0 {
			alerts = append(alerts, Alert{Kind: "authentication_failure_burst", Severity: "high", Summary: "Repeated authentication failures crossed the alert threshold", WindowHits: hits})
		}
	}
	if action == "authorization_denied" && failed {
		if hits := manager.windowHit("authorization\x00"+event.Event.SourceAddress, now, 5*time.Minute); hits == 10 || hits%25 == 0 {
			alerts = append(alerts, Alert{Kind: "authorization_denial_burst", Severity: "high", Summary: "Permission denials crossed the alert threshold", WindowHits: hits})
		}
	}
	if strings.HasPrefix(action, "external_trigger_") && failed {
		if hits := manager.windowHit("trigger\x00"+event.Event.SourceAddress, now, time.Minute); hits == 20 || hits%50 == 0 {
			alerts = append(alerts, Alert{Kind: "external_trigger_rejection_burst", Severity: "high", Summary: "External trigger rejections crossed the alert threshold", WindowHits: hits})
		}
	}
	return alerts
}

func (manager *Manager) windowHit(key string, now time.Time, window time.Duration) int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.windows[key]; !exists && len(manager.windows) >= maxDetectionKeys {
		for existing, values := range manager.windows {
			if len(values) == 0 || values[len(values)-1].Before(now.Add(-5*time.Minute)) {
				delete(manager.windows, existing)
			}
		}
		if _, exists := manager.windows[key]; !exists && len(manager.windows) >= maxDetectionKeys {
			// Aggregate excess cardinality into one bounded bucket; source values
			// remain present in each audit event sent to the receiver.
			key = "overflow"
		}
	}
	cutoff := now.Add(-window)
	values := manager.windows[key]
	first := 0
	for first < len(values) && values[first].Before(cutoff) {
		first++
	}
	values = append(values[first:], now)
	manager.windows[key] = values
	return len(values)
}

func (manager *Manager) persist(envelope Envelope) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entries, err := os.ReadDir(manager.spool)
	if err != nil {
		return err
	}
	if len(entries) >= maxPendingEvents {
		return errors.New("security event outbox is full")
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", envelope.Audit.ID, envelope.Audit.EventSHA256)
	temporary, err := os.CreateTemp(manager.spool, ".event-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(manager.spool, name))
}

func (manager *Manager) appendAlerts(envelope Envelope) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if info, err := os.Stat(manager.alertLog); err == nil && info.Size() >= maxAlertLogBytes {
		_ = os.Remove(manager.alertLog + ".1")
		if err := os.Rename(manager.alertLog, manager.alertLog+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(manager.alertLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (manager *Manager) run(ctx context.Context) {
	defer manager.wg.Done()
	failures := 0
	for {
		if err := manager.forwardOne(ctx); err == nil {
			failures = 0
			manager.setDeliveryState(0, time.Time{})
			select {
			case <-ctx.Done():
				return
			case <-manager.wake:
			default:
			}
			continue
		} else if errors.Is(err, os.ErrNotExist) {
			failures = 0
			manager.setDeliveryState(0, time.Time{})
			select {
			case <-ctx.Done():
				return
			case <-manager.wake:
			}
			continue
		}
		failures++
		delay := deliveryRetryDelay(failures)
		manager.setDeliveryState(failures, manager.now().Add(delay))
		timer := time.NewTimer(delay)
		if failures >= circuitThreshold {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-manager.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func deliveryRetryDelay(failures int) time.Duration {
	if failures >= circuitThreshold {
		return circuitOpenFor
	}
	delay := time.Second
	for attempt := 1; attempt < failures && delay < time.Minute; attempt++ {
		delay *= 2
	}
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func (manager *Manager) setDeliveryState(failures int, nextAttemptAt time.Time) {
	manager.mu.Lock()
	manager.deliveryFailures = failures
	manager.nextAttemptAt = nextAttemptAt.UTC()
	manager.mu.Unlock()
}

func (manager *Manager) forwardOne(ctx context.Context) error {
	entries, err := os.ReadDir(manager.spool)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return os.ErrNotExist
	}
	sort.Strings(names)
	path := filepath.Join(manager.spool, names[0])
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, manager.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "ScriptBoard-SecurityEvents/1")
	if manager.token != "" {
		request.Header.Set("Authorization", "Bearer "+manager.token)
	}
	response, err := manager.client.Do(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("security event endpoint returned %s", strconv.Itoa(response.StatusCode))
	}
	return os.Remove(path)
}

func (manager *Manager) signal() {
	select {
	case manager.wake <- struct{}{}:
	default:
	}
}
