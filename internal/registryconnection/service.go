// Package registryconnection owns Registry monitor connections behind the
// privileged Broker seam. It persists endpoint bindings and credentials as one
// sealed record and performs outbound inspection without returning credentials
// to the Web process.
package registryconnection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"scriptboard/internal/outboundpolicy"
	"scriptboard/internal/registrymonitor"
	"scriptboard/internal/secretstore"
)

var (
	ErrInvalidConnection = errors.New("Registry connection is invalid")
	ErrNotFound          = errors.New("Registry connection was not found")
)

const (
	storePurpose         = "registry-monitor-connections-v1"
	storeFilename        = "registry-monitor-connections.enc"
	maxConnections       = 100
	maxPendingOperations = 128
	maxCompleted         = 10_000
	maxStoreBytes        = 1 << 20
	maxCredentialBytes   = 8 << 10
	maxDockerConfigBytes = 1 << 20
)

type Options struct {
	StateRoot              string
	SecretStore            *secretstore.Store
	Client                 *http.Client
	DockerDaemonConfigPath string
}

type preparedOperation struct {
	CardID string       `json:"card_id"`
	Delete bool         `json:"delete,omitempty"`
	Record storedRecord `json:"record,omitempty"`
}

type storedRecord struct {
	Revision string                 `json:"revision"`
	Config   registrymonitor.Config `json:"config"`
	Password string                 `json:"password,omitempty"`
}

type persistedState struct {
	Active         map[string]storedRecord      `json:"active"`
	Pending        map[string]preparedOperation `json:"pending"`
	Completed      []string                     `json:"completed,omitempty"`
	LegacyMigrated bool                         `json:"legacy_migrated,omitempty"`
}

type Service struct {
	path             string
	vault            *secretstore.Store
	inspector        *registrymonitor.Client
	dockerConfigPath string
	mu               sync.Mutex
}

func New(options Options) (*Service, error) {
	root, err := filepath.Abs(strings.TrimSpace(options.StateRoot))
	if err != nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("Registry connection service requires a State Root")
	}
	directory := filepath.Join(root, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Registry connection directory: %w", err)
	}
	_ = os.Chmod(directory, 0o700)
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(root)
		if err != nil {
			return nil, fmt.Errorf("initialize Registry connection store: %w", err)
		}
	}
	client := options.Client
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			Transport: outboundpolicy.Policy{
				AllowPrivate: true,
				AllowAnyPort: true,
			}.Transport(),
		}
	} else {
		copy := *client
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("Registry redirects are not allowed")
	}
	dockerConfigPath := strings.TrimSpace(options.DockerDaemonConfigPath)
	if dockerConfigPath == "" {
		dockerConfigPath = defaultDockerDaemonConfigPath()
	}
	if !filepath.IsAbs(dockerConfigPath) {
		return nil, errors.New("Docker daemon configuration path must be absolute")
	}
	return &Service{
		path: filepath.Join(directory, storeFilename), vault: vault,
		inspector: registrymonitor.New(client), dockerConfigPath: filepath.Clean(dockerConfigPath),
	}, nil
}

func defaultDockerDaemonConfigPath() string {
	if runtime.GOOS == "windows" {
		root := strings.TrimSpace(os.Getenv("ProgramData"))
		if root == "" {
			root = `C:\ProgramData`
		}
		return filepath.Join(root, "docker", "config", "daemon.json")
	}
	return "/etc/docker/daemon.json"
}

func insecureRegistryHost(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("only an HTTP Registry origin can be registered")
	}
	if len(parsed.Host) > 255 || strings.ContainsAny(parsed.Host, "\r\n\x00") {
		return "", errors.New("Registry host is invalid")
	}
	return parsed.Host, nil
}

func (service *Service) InsecureConfigured(_ context.Context, endpoint string) (bool, error) {
	host, err := insecureRegistryHost(endpoint)
	if err != nil {
		return false, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	registries, _, err := service.readDockerInsecureRegistries()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, registry := range registries {
		if strings.EqualFold(strings.TrimSpace(registry), host) {
			return true, nil
		}
	}
	return false, nil
}

func (service *Service) RegisterInsecure(_ context.Context, endpoint string) (bool, error) {
	host, err := insecureRegistryHost(endpoint)
	if err != nil {
		return false, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	registries, document, err := service.readDockerInsecureRegistries()
	if errors.Is(err, os.ErrNotExist) {
		registries, document, err = nil, map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return false, err
	}
	for _, registry := range registries {
		if strings.EqualFold(strings.TrimSpace(registry), host) {
			return false, nil
		}
	}
	registries = append(registries, host)
	encodedRegistries, err := json.Marshal(registries)
	if err != nil {
		return false, err
	}
	document["insecure-registries"] = encodedRegistries
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode Docker daemon configuration: %w", err)
	}
	body = append(body, '\n')
	if err := writeDockerConfigAtomic(service.dockerConfigPath, body); err != nil {
		return false, err
	}
	return true, nil
}

func (service *Service) readDockerInsecureRegistries() ([]string, map[string]json.RawMessage, error) {
	info, err := os.Lstat(service.dockerConfigPath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxDockerConfigBytes {
		return nil, nil, errors.New("Docker daemon configuration must be a bounded regular file")
	}
	body, err := os.ReadFile(service.dockerConfigPath)
	if err != nil {
		return nil, nil, err
	}
	var document map[string]json.RawMessage
	if len(strings.TrimSpace(string(body))) == 0 || json.Unmarshal(body, &document) != nil || document == nil {
		return nil, nil, errors.New("Docker daemon configuration is not a valid JSON object")
	}
	var registries []string
	if raw, ok := document["insecure-registries"]; ok && json.Unmarshal(raw, &registries) != nil {
		return nil, nil, errors.New("Docker insecure-registries must be a string array")
	}
	return registries, document, nil
}

func writeDockerConfigAtomic(path string, body []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create Docker configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".scriptboard-daemon-*.json")
	if err != nil {
		return fmt.Errorf("create Docker daemon configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Docker daemon configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync Docker daemon configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Docker daemon configuration: %w", err)
	}
	return nil
}

// Prepare stores an inert mutation. The active connection is unchanged until
// Commit succeeds, so a caller can commit its SQLite transaction first.
func (service *Service) Prepare(_ context.Context, operationID, cardID string, config registrymonitor.Config, password string, preserve bool) error {
	operationID, cardID = strings.TrimSpace(operationID), strings.TrimSpace(cardID)
	config = registrymonitor.NormalizeConfig(config)
	if !validID(operationID) || !validID(cardID) || registrymonitor.ValidateConfig(config) != nil || !validCredential(password, true) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return err
	}
	if completed(state, operationID) {
		return nil
	}
	if existing, ok := state.Pending[operationID]; ok {
		candidate := preparedOperation{CardID: cardID, Record: storedRecord{Revision: operationID, Config: config, Password: password}}
		if preserve && candidate.Record.Password == "" {
			candidate.Record.Password = state.Active[cardID].Password
		}
		if equalOperation(existing, candidate) {
			return nil
		}
		return ErrInvalidConnection
	}
	if len(state.Pending) >= maxPendingOperations {
		return ErrInvalidConnection
	}
	if _, exists := state.Active[cardID]; !exists && len(state.Active) >= maxConnections {
		return ErrInvalidConnection
	}
	if config.AuthMode == "anonymous" {
		password = ""
	} else if password == "" && preserve {
		password = state.Active[cardID].Password
	}
	if config.AuthMode == "basic" && password == "" {
		return ErrNotFound
	}
	state.Pending[operationID] = preparedOperation{
		CardID: cardID,
		Record: storedRecord{Revision: operationID, Config: config, Password: password},
	}
	return service.write(state)
}

func (service *Service) PrepareDelete(_ context.Context, operationID, cardID string) error {
	operationID, cardID = strings.TrimSpace(operationID), strings.TrimSpace(cardID)
	if !validID(operationID) || !validID(cardID) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return err
	}
	if completed(state, operationID) {
		return nil
	}
	candidate := preparedOperation{CardID: cardID, Delete: true}
	if existing, ok := state.Pending[operationID]; ok {
		if equalOperation(existing, candidate) {
			return nil
		}
		return ErrInvalidConnection
	}
	if len(state.Pending) >= maxPendingOperations {
		return ErrInvalidConnection
	}
	state.Pending[operationID] = candidate
	return service.write(state)
}

func (service *Service) Commit(_ context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if !validID(operationID) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return err
	}
	if completed(state, operationID) {
		return nil
	}
	operation, ok := state.Pending[operationID]
	if !ok {
		return ErrNotFound
	}
	// Completed IDs are retained until the SQLite coordinator explicitly
	// acknowledges them. Refuse the mutation before applying it when the
	// durable receipt set is full; silently evicting receipts can make crash
	// recovery impossible.
	if len(state.Completed) >= maxCompleted {
		return ErrInvalidConnection
	}
	if operation.Delete {
		delete(state.Active, operation.CardID)
	} else {
		state.Active[operation.CardID] = operation.Record
	}
	delete(state.Pending, operationID)
	state.Completed = append(state.Completed, operationID)
	return service.write(state)
}

// Acknowledge releases a durable commit receipt after the SQLite coordinator
// has recorded that Commit succeeded. It is idempotent so recovery may repeat
// the acknowledgement after either process is interrupted.
func (service *Service) Acknowledge(_ context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if !validID(operationID) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return err
	}
	if _, pending := state.Pending[operationID]; pending {
		return ErrInvalidConnection
	}
	for index, candidate := range state.Completed {
		if candidate != operationID {
			continue
		}
		state.Completed = append(state.Completed[:index], state.Completed[index+1:]...)
		return service.write(state)
	}
	return nil
}

func (service *Service) Abort(_ context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if !validID(operationID) {
		return ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return err
	}
	if completed(state, operationID) {
		return nil
	}
	if _, ok := state.Pending[operationID]; !ok {
		return nil
	}
	delete(state.Pending, operationID)
	return service.write(state)
}

func (service *Service) Configured(_ context.Context, cardID string) (bool, error) {
	cardID = strings.TrimSpace(cardID)
	if !validID(cardID) {
		return false, ErrInvalidConnection
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil {
		return false, err
	}
	record, ok := state.Active[cardID]
	return ok && record.Config.AuthMode == "basic" && record.Password != "", nil
}

func (service *Service) Inspect(ctx context.Context, cardID string) ([]registrymonitor.ImageResult, error) {
	cardID = strings.TrimSpace(cardID)
	if !validID(cardID) {
		return nil, ErrInvalidConnection
	}
	service.mu.Lock()
	state, err := service.load()
	record, ok := state.Active[cardID]
	service.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	config := record.Config
	config.Password = record.Password
	return service.inspector.Inspect(ctx, config)
}

func (service *Service) Test(ctx context.Context, cardID string, config registrymonitor.Config, password string, preserve bool) ([]registrymonitor.ImageResult, error) {
	config = registrymonitor.NormalizeConfig(config)
	cardID = strings.TrimSpace(cardID)
	// 匿名连接没有凭据；忽略上层误传的保留标记，避免读取不存在的旧密码。
	if config.AuthMode == "anonymous" {
		password = ""
		preserve = false
	}
	if registrymonitor.ValidateConfig(config) != nil || !validCredential(password, true) || preserve && !validID(cardID) {
		return nil, ErrInvalidConnection
	}
	if preserve && password == "" {
		service.mu.Lock()
		state, err := service.load()
		record, ok := state.Active[cardID]
		service.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if !ok || record.Password == "" {
			return nil, ErrNotFound
		}
		password = record.Password
	}
	if config.AuthMode == "basic" && password == "" {
		return nil, ErrInvalidConnection
	}
	config.Password = password
	return service.inspector.Inspect(ctx, config)
}

func (service *Service) load() (persistedState, error) {
	state := persistedState{Active: map[string]storedRecord{}, Pending: map[string]preparedOperation{}}
	body, err := os.ReadFile(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return persistedState{}, fmt.Errorf("read Registry connections: %w", err)
	}
	if len(body) > maxStoreBytes {
		return persistedState{}, errors.New("Registry connection store is too large")
	}
	plain, err := service.vault.Unseal(storePurpose, body)
	if err != nil {
		return persistedState{}, fmt.Errorf("unseal Registry connections: %w", err)
	}
	if json.Unmarshal(plain, &state) != nil || state.Active == nil || state.Pending == nil || len(state.Active) > maxConnections || len(state.Pending) > maxPendingOperations || len(state.Completed) > maxCompleted {
		return persistedState{}, errors.New("decode Registry connections")
	}
	return state, nil
}

func (service *Service) write(state persistedState) error {
	plain, err := json.Marshal(state)
	if err != nil || len(plain) > maxStoreBytes {
		return errors.New("encode Registry connections")
	}
	body, err := service.vault.Seal(storePurpose, plain)
	if err != nil {
		return fmt.Errorf("seal Registry connections: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(service.path), ".registry-monitor-connections-*.tmp")
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

func validID(value string) bool {
	return value != "" && len(value) <= 160 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func validCredential(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	return len(value) <= maxCredentialBytes && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func completed(state persistedState, operationID string) bool {
	for _, candidate := range state.Completed {
		if candidate == operationID {
			return true
		}
	}
	return false
}

func equalOperation(left, right preparedOperation) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}
