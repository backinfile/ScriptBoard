// Package remotewebsite owns credentials for remote ScriptBoard website
// monitor connections and performs the authenticated outbound request without
// returning those credentials to the caller.
package remotewebsite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"scriptboard/internal/outboundpolicy"
	"scriptboard/internal/secretstore"
)

var (
	ErrInvalidConnection  = errors.New("remote website connection is invalid")
	ErrConnectionNotFound = errors.New("remote website connection was not found")
)

const (
	storePurpose       = "remote-website-connections-v1"
	storeFilename      = "remote-website-connections.enc"
	maxConnections     = 20
	maxStoreBytes      = 64 << 10
	MaxResponseBytes   = 4 << 20
	maxConnectionID    = 160
	maxEndpointBytes   = 2048
	maxCredentialBytes = 128
)

type Options struct {
	StateRoot   string
	SecretStore *secretstore.Store
	Client      *http.Client
}

type connection struct {
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
}

type Service struct {
	path   string
	vault  *secretstore.Store
	client *http.Client
	mu     sync.Mutex
}

func New(options Options) (*Service, error) {
	root, err := filepath.Abs(strings.TrimSpace(options.StateRoot))
	if err != nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("remote website service requires a State Root")
	}
	directory := filepath.Join(root, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create remote website secret directory: %w", err)
	}
	_ = os.Chmod(directory, 0o700)
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(root)
		if err != nil {
			return nil, fmt.Errorf("initialize remote website credential store: %w", err)
		}
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Transport: outboundpolicy.Policy{}.Transport(), Timeout: 10 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	return &Service{path: filepath.Join(directory, storeFilename), vault: vault, client: client}, nil
}

func (service *Service) Store(_ context.Context, id, endpoint, key string) error {
	id, endpoint, key = strings.TrimSpace(id), strings.TrimSpace(endpoint), strings.TrimSpace(key)
	if !validID(id) || !validEndpoint(endpoint) || !validKey(key) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	values, err := service.load()
	if err != nil {
		return err
	}
	if _, exists := values[id]; !exists && len(values) >= maxConnections {
		return ErrInvalidConnection
	}
	values[id] = connection{Endpoint: endpoint, Key: key}
	return service.write(values)
}

func (service *Service) Delete(_ context.Context, id string) error {
	id = strings.TrimSpace(id)
	if !validID(id) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	values, err := service.load()
	if err != nil {
		return err
	}
	if _, exists := values[id]; !exists {
		return nil
	}
	delete(values, id)
	return service.write(values)
}

func (service *Service) Fetch(ctx context.Context, id, acceptLanguage string) (json.RawMessage, error) {
	id, acceptLanguage = strings.TrimSpace(id), strings.TrimSpace(acceptLanguage)
	if !validID(id) || len(acceptLanguage) > 64 || strings.ContainsAny(acceptLanguage, "\r\n\x00") {
		return nil, ErrInvalidConnection
	}
	service.mu.Lock()
	values, err := service.load()
	entry, exists := values[id]
	service.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrConnectionNotFound
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.Endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+entry.Key)
	request.Header.Set("Accept", "application/json")
	if acceptLanguage != "" {
		request.Header.Set("Accept-Language", acceptLanguage)
	}
	request.Header.Set("User-Agent", "ScriptBoard/remote-website-monitor")
	response, err := service.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch remote website monitors: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		return nil, fmt.Errorf("remote website monitor status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil || len(body) > MaxResponseBytes {
		return nil, errors.New("remote website monitor response is too large")
	}
	var envelope struct {
		OK            bool            `json:"ok"`
		Action        string          `json:"action"`
		SchemaVersion int             `json:"schema_version"`
		Data          json.RawMessage `json:"data"`
	}
	if !json.Valid(body) || json.Unmarshal(body, &envelope) != nil || !envelope.OK || envelope.Action != "website_monitor" || envelope.SchemaVersion != 1 || len(envelope.Data) == 0 {
		return nil, errors.New("invalid remote website monitor response")
	}
	return append(json.RawMessage(nil), body...), nil
}

func (service *Service) load() (map[string]connection, error) {
	body, err := os.ReadFile(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]connection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read remote website connections: %w", err)
	}
	if len(body) > maxStoreBytes {
		return nil, errors.New("remote website connection store is too large")
	}
	plain, err := service.vault.Unseal(storePurpose, body)
	if err != nil {
		return nil, fmt.Errorf("unseal remote website connections: %w", err)
	}
	values := map[string]connection{}
	if err := json.Unmarshal(plain, &values); err != nil || len(values) > maxConnections {
		return nil, errors.New("decode remote website connections")
	}
	for id, entry := range values {
		if !validID(id) || !validEndpoint(entry.Endpoint) || !validKey(entry.Key) {
			return nil, errors.New("remote website connection store contains invalid data")
		}
	}
	return values, nil
}

func (service *Service) write(values map[string]connection) error {
	plain, err := json.Marshal(values)
	if err != nil || len(plain) > maxStoreBytes {
		return errors.New("encode remote website connections")
	}
	body, err := service.vault.Seal(storePurpose, plain)
	if err != nil {
		return fmt.Errorf("seal remote website connections: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(service.path), ".remote-website-connections-*.tmp")
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
	if err := os.Rename(path, service.path); err != nil {
		return fmt.Errorf("replace remote website connections: %w", err)
	}
	return nil
}

func validID(value string) bool {
	return value != "" && len(value) <= maxConnectionID && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func validEndpoint(value string) bool {
	if value == "" || len(value) > maxEndpointBytes || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == "" && parsed.RawQuery != "" && parsed.Query().Get("name") != ""
}

func validKey(value string) bool {
	if !strings.HasPrefix(value, "sbk_") || len(value) > maxCredentialBytes {
		return false
	}
	identity, secret, ok := strings.Cut(strings.TrimPrefix(value, "sbk_"), ".")
	return ok && len(identity) == 16 && len(secret) == 43 && base64URL(identity) && base64URL(secret)
}

func base64URL(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
