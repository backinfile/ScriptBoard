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
	// Channel isolates durable queues owned by different processes. The empty
	// value preserves the original SIEM webhook queue.
	Channel                  string
	EnvelopeType             string
	Recipient                string
	NotificationsOnly        bool
	DisableLocalAlerts       bool
	BrokerEmailRelayEndpoint string
	BrokerEmailRecipient     string
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
	Notification  *Notification           `json:"notification,omitempty"`
	Recipient     string                  `json:"recipient,omitempty"`
}

type Notification struct {
	Template   string `json:"template"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
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
	WebsiteTemplates bool
	EmailEnabled     bool
	EmailRelayHost   string
	EmailRecipient   string
	EmailTemplates   bool
}

type Manager struct {
	endpoint             string
	token                string
	client               *http.Client
	now                  func() time.Time
	spool                string
	alertLog             string
	envelopeType         string
	recipient            string
	notificationsOnly    bool
	disableLocalAlerts   bool
	brokerEmailRelayHost string
	brokerEmailRecipient string
	wake                 chan struct{}
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	mu                   sync.Mutex
	windows              map[string][]time.Time
	deliveryFailures     int
	nextAttemptAt        time.Time
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
	channel := strings.TrimSpace(options.Channel)
	if channel == "" {
		channel = "outbox"
	}
	if channel != "outbox" && channel != "email-outbox" {
		return nil, errors.New("security event channel is invalid")
	}
	spool := filepath.Join(root, "security-events", channel)
	for _, directory := range []string{logs, spool} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		_ = os.Chmod(directory, 0o700)
	}
	envelopeType := strings.TrimSpace(options.EnvelopeType)
	if envelopeType == "" {
		envelopeType = "scriptboard.security-event"
	}
	manager := &Manager{
		endpoint: endpoint, token: strings.TrimSpace(options.Token), client: client, now: now,
		spool: spool, alertLog: filepath.Join(logs, "security-alerts.jsonl"), wake: make(chan struct{}, 1), windows: make(map[string][]time.Time),
		envelopeType: envelopeType, recipient: strings.TrimSpace(options.Recipient), notificationsOnly: options.NotificationsOnly,
		disableLocalAlerts:   options.DisableLocalAlerts,
		brokerEmailRelayHost: endpointHost(options.BrokerEmailRelayEndpoint), brokerEmailRecipient: maskEmail(options.BrokerEmailRecipient),
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
	_ = manager.Queue(event)
}

// Queue converts a committed, already-redacted audit event into a bounded
// delivery envelope. Callers that keep a durable source cursor use the error
// to avoid advancing past an event that could not be persisted.
func (manager *Manager) Queue(event auditlog.CommittedEvent) error {
	now := manager.now().UTC()
	alerts := manager.detect(event, now)
	notification := NotificationFor(event)
	if manager.notificationsOnly && notification == nil {
		return nil
	}
	envelope := Envelope{Type: manager.envelopeType, SchemaVersion: 1, SentAt: now.Format(time.RFC3339Nano), Audit: event, Alerts: alerts, Notification: notification, Recipient: manager.recipient}
	if len(alerts) > 0 && !manager.disableLocalAlerts {
		_ = manager.appendAlerts(envelope)
	}
	if manager.endpoint == "" {
		return nil
	}
	if err := manager.persist(envelope); err != nil {
		return err
	}
	manager.signal()
	return nil
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
	status := Status{WebhookEnabled: manager.endpoint != "", Pending: pending, Capacity: maxPendingEvents, WebsiteTemplates: true,
		EmailEnabled: manager.brokerEmailRelayHost != "", EmailRelayHost: manager.brokerEmailRelayHost, EmailRecipient: manager.brokerEmailRecipient, EmailTemplates: true}
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

func endpointHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	return parsed.Host
}

func maskEmail(value string) string {
	value = strings.TrimSpace(value)
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 {
		return ""
	}
	return value[:1] + "***" + value[at:]
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
	if action == "website_monitor_down" && failed {
		alerts = append(alerts, Alert{Kind: "website_monitor_outage", Severity: "high", Summary: "A website monitor entered the confirmed down state"})
	}
	return alerts
}

func NotificationFor(event auditlog.CommittedEvent) *Notification {
	action := strings.ToLower(event.Event.Action)
	switch action {
	case "website_monitor_down":
		return &Notification{Template: "website-monitor-result-v1", Severity: "high", Title: "Website monitor confirmed an outage", Summary: "Two consecutive checks failed. Review the current incident in ScriptBoard.", ResourceID: event.Event.Target, State: "down"}
	case "website_monitor_recovered":
		return &Notification{Template: "website-monitor-result-v1", Severity: "info", Title: "Website monitor recovered", Summary: "A successful check closed the current incident.", ResourceID: event.Event.Target, State: "recovered"}
	}
	result := strings.ToLower(event.Event.Result)
	if strings.HasPrefix(action, "state_backup.") || strings.HasPrefix(action, "state_backup_") {
		return &Notification{Template: "state-backup-result-v1", Severity: resultSeverity(result), Title: "State backup operation completed", Summary: "Review the recorded backup operation in ScriptBoard.", ResourceID: event.Event.Target, State: result}
	}
	if strings.HasPrefix(action, "update_") || strings.HasPrefix(action, "update.") {
		return &Notification{Template: "update-result-v1", Severity: resultSeverity(result), Title: "ScriptBoard update operation completed", Summary: "Review the signed update result in ScriptBoard.", ResourceID: event.Event.Target, State: result}
	}
	if action == "run_completed" && (result == "succeeded" || result == "failed" || result == "cancelled" || result == "timed_out") {
		return &Notification{Template: "run-result-v1", Severity: resultSeverity(result), Title: "Run operation completed", Summary: "Review the bounded Run result in ScriptBoard.", ResourceID: event.Event.Target, State: result}
	}
	if result == "failed" || result == "rejected" || result == "blocked" || result == "rate_limited" {
		return &Notification{Template: "security-alert-v1", Severity: "high", Title: "ScriptBoard security event", Summary: "A protected operation was denied or failed. Review the audit trail.", ResourceID: event.Event.Target, State: result}
	}
	return nil
}

func resultSeverity(result string) string {
	switch result {
	case "failed", "rejected", "blocked", "rate_limited", "timed_out":
		return "high"
	default:
		return "info"
	}
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
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", envelope.Audit.ID, envelope.Audit.EventSHA256)
	destination := filepath.Join(manager.spool, name)
	if existing, readErr := os.ReadFile(destination); readErr == nil {
		if sameEnvelopeIdentity(existing, envelope) {
			return nil
		}
		return errors.New("security event outbox identity collision")
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	entries, err := os.ReadDir(manager.spool)
	if err != nil {
		return err
	}
	if len(entries) >= maxPendingEvents {
		return errors.New("security event outbox is full")
	}
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
	if err := os.Rename(temporaryPath, destination); err != nil {
		if existing, readErr := os.ReadFile(destination); readErr == nil && sameEnvelopeIdentity(existing, envelope) {
			return nil
		}
		return err
	}
	return nil
}

func sameEnvelopeIdentity(body []byte, expected Envelope) bool {
	var existing Envelope
	return json.Unmarshal(body, &existing) == nil && existing.Type == expected.Type && existing.Recipient == expected.Recipient &&
		existing.Audit.ID == expected.Audit.ID && existing.Audit.EventSHA256 == expected.Audit.EventSHA256
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
