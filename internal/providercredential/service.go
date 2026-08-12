// Package providercredential owns recoverable Assistant provider credentials
// and the short-lived loopback proxy sessions that use them. Callers receive a
// process-bound capability, never the upstream credential.
package providercredential

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"scriptboard/internal/assistant/providerproxy"
	"scriptboard/internal/secretstore"
)

var (
	ErrInvalidRecord   = errors.New("provider credential record is invalid")
	ErrNotFound        = errors.New("provider credential was not found")
	ErrForbidden       = errors.New("provider credential is not available to this user")
	ErrSessionCapacity = errors.New("provider proxy session capacity reached")
)

const (
	storePurpose           = "assistant-provider-records-v1"
	storeFilename          = "assistant-provider-records.enc"
	maxRecords             = 64
	maxStoreBytes          = 1 << 20
	maxCredentialBytes     = 8 << 10
	maxProxySessions       = 16
	defaultSessionLifetime = 15 * time.Minute
)

type Record struct {
	ID, OwnerUserID, Provider, Model, Endpoint string
	Shared                                     bool
}

type Session struct {
	Endpoint, Capability, Handle string
}

type Options struct {
	StateRoot       string
	SecretStore     *secretstore.Store
	SessionLifetime time.Duration
}

type storedRecord struct {
	OwnerUserID string `json:"owner_user_id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Endpoint    string `json:"endpoint"`
	Credential  string `json:"credential"`
	Shared      bool   `json:"shared"`
}

type activeSession struct {
	proxy *providerproxy.Session
	timer *time.Timer
}

type Service struct {
	path            string
	vault           *secretstore.Store
	sessionLifetime time.Duration
	mu              sync.Mutex
	sessions        map[string]activeSession
	closed          bool
}

func New(options Options) (*Service, error) {
	root, err := filepath.Abs(strings.TrimSpace(options.StateRoot))
	if err != nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("provider credential service requires a State Root")
	}
	directory := filepath.Join(root, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create provider credential directory: %w", err)
	}
	_ = os.Chmod(directory, 0o700)
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(root)
		if err != nil {
			return nil, fmt.Errorf("initialize provider credential store: %w", err)
		}
	}
	lifetime := options.SessionLifetime
	if lifetime <= 0 || lifetime > defaultSessionLifetime {
		lifetime = defaultSessionLifetime
	}
	return &Service{
		path: filepath.Join(directory, storeFilename), vault: vault,
		sessionLifetime: lifetime, sessions: make(map[string]activeSession),
	}, nil
}

func (service *Service) Store(_ context.Context, actorUserID string, record Record, credential string) error {
	record = normalizeRecord(record)
	actorUserID, credential = strings.TrimSpace(actorUserID), strings.TrimSpace(credential)
	if actorUserID == "" || actorUserID != record.OwnerUserID || !validRecord(record) || !validCredential(credential, true) {
		return ErrInvalidRecord
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return errors.New("provider credential service is closed")
	}
	values, err := service.load()
	if err != nil {
		return err
	}
	previous, exists := values[record.ID]
	if exists && previous.OwnerUserID != actorUserID {
		return ErrForbidden
	}
	if !exists && len(values) >= maxRecords {
		return ErrInvalidRecord
	}
	if credential == "" {
		if !exists || strings.TrimSpace(previous.Credential) == "" {
			return ErrNotFound
		}
		credential = previous.Credential
	}
	values[record.ID] = storedRecord{
		OwnerUserID: record.OwnerUserID, Provider: record.Provider, Model: record.Model,
		Endpoint: record.Endpoint, Credential: credential, Shared: record.Shared,
	}
	return service.write(values)
}

func (service *Service) Delete(_ context.Context, actorUserID, id string) error {
	actorUserID, id = strings.TrimSpace(actorUserID), strings.TrimSpace(id)
	if actorUserID == "" || !validIdentifier(id) {
		return ErrInvalidRecord
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	values, err := service.load()
	if err != nil {
		return err
	}
	entry, exists := values[id]
	if !exists {
		return nil
	}
	if entry.OwnerUserID != actorUserID {
		return ErrForbidden
	}
	delete(values, id)
	return service.write(values)
}

func (service *Service) Start(_ context.Context, actorUserID, id string) (Session, error) {
	actorUserID, id = strings.TrimSpace(actorUserID), strings.TrimSpace(id)
	if actorUserID == "" || !validIdentifier(id) {
		return Session{}, ErrInvalidRecord
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return Session{}, errors.New("provider credential service is closed")
	}
	if len(service.sessions) >= maxProxySessions {
		service.mu.Unlock()
		return Session{}, ErrSessionCapacity
	}
	values, err := service.load()
	entry, exists := values[id]
	service.mu.Unlock()
	if err != nil {
		return Session{}, err
	}
	if !exists {
		return Session{}, ErrNotFound
	}
	if entry.OwnerUserID != actorUserID && !entry.Shared {
		return Session{}, ErrForbidden
	}
	proxy, err := providerproxy.Start(providerproxy.Config{
		Provider: entry.Provider, Model: entry.Model, Endpoint: entry.Endpoint, Credential: entry.Credential,
	})
	if err != nil {
		return Session{}, fmt.Errorf("start provider proxy: %w", err)
	}
	handle, err := randomHandle()
	if err != nil {
		_ = proxy.Close(context.Background())
		return Session{}, err
	}
	service.mu.Lock()
	if service.closed || len(service.sessions) >= maxProxySessions {
		service.mu.Unlock()
		_ = proxy.Close(context.Background())
		return Session{}, ErrSessionCapacity
	}
	active := activeSession{proxy: proxy}
	active.timer = time.AfterFunc(service.sessionLifetime, func() { _ = service.Stop(context.Background(), handle) })
	service.sessions[handle] = active
	service.mu.Unlock()
	return Session{Endpoint: proxy.Endpoint(), Capability: proxy.Capability(), Handle: handle}, nil
}

func (service *Service) Stop(ctx context.Context, handle string) error {
	handle = strings.TrimSpace(handle)
	if !validHandle(handle) {
		return ErrInvalidRecord
	}
	service.mu.Lock()
	active, exists := service.sessions[handle]
	delete(service.sessions, handle)
	service.mu.Unlock()
	if !exists {
		return nil
	}
	if active.timer != nil {
		active.timer.Stop()
	}
	return active.proxy.Close(ctx)
}

func (service *Service) Close(ctx context.Context) error {
	service.mu.Lock()
	service.closed = true
	sessions := service.sessions
	service.sessions = make(map[string]activeSession)
	service.mu.Unlock()
	var result error
	for _, active := range sessions {
		if active.timer != nil {
			active.timer.Stop()
		}
		if err := active.proxy.Close(ctx); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func (service *Service) load() (map[string]storedRecord, error) {
	body, err := os.ReadFile(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]storedRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read provider credentials: %w", err)
	}
	if len(body) > maxStoreBytes {
		return nil, errors.New("provider credential store is too large")
	}
	plain, err := service.vault.Unseal(storePurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal provider credentials: %w", err)
	}
	values := map[string]storedRecord{}
	if json.Unmarshal(plain, &values) != nil || len(values) > maxRecords {
		return nil, errors.New("decode provider credentials")
	}
	for id, entry := range values {
		record := Record{ID: id, OwnerUserID: entry.OwnerUserID, Provider: entry.Provider, Model: entry.Model, Endpoint: entry.Endpoint, Shared: entry.Shared}
		if !validRecord(record) || !validCredential(entry.Credential, false) {
			return nil, errors.New("provider credential store contains invalid data")
		}
	}
	return values, nil
}

func (service *Service) write(values map[string]storedRecord) error {
	plain, err := json.Marshal(values)
	if err != nil || len(plain) > maxStoreBytes {
		return errors.New("encode provider credentials")
	}
	body, err := service.vault.Seal(storePurpose, plain)
	if err != nil {
		return fmt.Errorf("seal provider credentials: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(service.path), ".assistant-provider-records-*.tmp")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
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
	return os.Rename(path, service.path)
}

func normalizeRecord(record Record) Record {
	record.ID = strings.TrimSpace(record.ID)
	record.OwnerUserID = strings.TrimSpace(record.OwnerUserID)
	record.Provider = strings.ToLower(strings.TrimSpace(record.Provider))
	record.Model = strings.TrimSpace(record.Model)
	record.Endpoint = strings.TrimRight(strings.TrimSpace(record.Endpoint), "/")
	return record
}

func validRecord(record Record) bool {
	if !validIdentifier(record.ID) || !validIdentifier(record.OwnerUserID) || record.Model == "" || len(record.Model) > 160 || !utf8.ValidString(record.Model) || strings.ContainsAny(record.Model, "\r\n\x00") {
		return false
	}
	switch record.Provider {
	case "openai", "anthropic", "openai-compatible":
	default:
		return false
	}
	parsed, err := url.Parse(record.Endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(record.Endpoint) > 2048 {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return parsed.Scheme == "http" && isLoopback(parsed.Hostname())
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 160 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func validCredential(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	return len(value) <= maxCredentialBytes && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomHandle() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create provider session handle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validHandle(value string) bool {
	if len(value) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil
}
