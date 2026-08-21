// Package fleetstatus owns read-only observation of other ScriptBoard instances.
package fleetstatus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/hoststatus"
	"scriptboard/internal/outboundpolicy"
	"scriptboard/internal/secretstore"
)

const (
	ProtocolVersion = 1
	ExportPath      = "/api/fleet/v1/status"
	credentialUse   = "fleet-peer-access-token-v1"
	maxPeers        = 100
	maxTokens       = 20
	maxResponseSize = 256 << 10
)

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS fleet_access_tokens (
		id TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		hint TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS fleet_peers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		endpoint TEXT NOT NULL UNIQUE,
		access_token_cipher BLOB NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		overview_json TEXT NOT NULL DEFAULT '{}',
		last_seen_at INTEGER NOT NULL DEFAULT 0,
		last_attempt_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
}

type Export struct {
	ProtocolVersion int                 `json:"protocolVersion"`
	Overview        hoststatus.Overview `json:"overview"`
}

type AccessToken struct {
	ID, Label, Hint string
	CreatedAt       time.Time
	LastUsedAt      time.Time
}

type AddPeerInput struct {
	Name, Endpoint, AccessToken string
}

type UpdatePeerInput struct {
	Name, Endpoint, AccessToken string
}

type Peer struct {
	ID, Name, Endpoint   string
	Enabled              bool
	Overview             hoststatus.Overview
	LastSeenAt           time.Time
	LastAttemptAt        time.Time
	LastError            string
	CreatedAt, UpdatedAt time.Time
}

func (peer Peer) Online(now time.Time) bool {
	return peer.Enabled && peer.LastError == "" && !peer.LastSeenAt.IsZero() && now.Sub(peer.LastSeenAt) <= 45*time.Second && !peer.Overview.Stale
}

type Options struct {
	DB          *sql.DB
	SecretStore *secretstore.Store
	Client      *http.Client
	Now         func() time.Time
	Interval    time.Duration
}

type Manager struct {
	db       *sql.DB
	vault    *secretstore.Store
	client   *http.Client
	now      func() time.Time
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(options Options) (*Manager, error) {
	if options.DB == nil || options.SecretStore == nil {
		return nil, errors.New("fleet status database and credential store are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Interval <= 0 {
		options.Interval = 15 * time.Second
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second, Transport: outboundpolicy.Policy{AllowPrivate: true, AllowAnyPort: true}.Transport()}
	} else {
		copy := *client
		client = &copy
		if client.Timeout <= 0 {
			client.Timeout = 8 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("ScriptBoard node redirects are not allowed")
	}
	return &Manager{db: options.DB, vault: options.SecretStore, client: client, now: options.Now, interval: options.Interval}, nil
}

func (manager *Manager) Start(parent context.Context) {
	manager.mu.Lock()
	if manager.cancel != nil {
		manager.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	manager.cancel = cancel
	manager.wg.Add(1)
	manager.mu.Unlock()
	go func() {
		defer manager.wg.Done()
		manager.RefreshAll(ctx)
		ticker := time.NewTicker(manager.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				manager.RefreshAll(ctx)
			}
		}
	}()
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	cancel := manager.cancel
	manager.cancel = nil
	manager.mu.Unlock()
	if cancel != nil {
		cancel()
		manager.wg.Wait()
	}
}

func (manager *Manager) CreateAccessToken(ctx context.Context, label string) (AccessToken, string, error) {
	label = strings.TrimSpace(label)
	if !validLabel(label, 64) {
		return AccessToken{}, "", errors.New("access token label is invalid")
	}
	var count int
	if err := manager.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fleet_access_tokens`).Scan(&count); err != nil {
		return AccessToken{}, "", err
	}
	if count >= maxTokens {
		return AccessToken{}, "", errors.New("access token limit reached")
	}
	id, err := randomID(18)
	if err != nil {
		return AccessToken{}, "", err
	}
	raw, err := randomID(32)
	if err != nil {
		return AccessToken{}, "", err
	}
	secret := "sbf_" + raw
	hash := tokenHash(secret)
	hint := secret[len(secret)-6:]
	now := manager.now().UTC()
	if _, err := manager.db.ExecContext(ctx, `INSERT INTO fleet_access_tokens (id, label, token_hash, hint, created_at) VALUES (?, ?, ?, ?, ?)`, id, label, hash, hint, now.Unix()); err != nil {
		return AccessToken{}, "", err
	}
	return AccessToken{ID: id, Label: label, Hint: hint, CreatedAt: now}, secret, nil
}

func (manager *Manager) ListAccessTokens(ctx context.Context) ([]AccessToken, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, label, hint, created_at, last_used_at FROM fleet_access_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AccessToken
	for rows.Next() {
		var value AccessToken
		var created, used int64
		if err := rows.Scan(&value.ID, &value.Label, &value.Hint, &created, &used); err != nil {
			return nil, err
		}
		value.CreatedAt = unixTime(created)
		value.LastUsedAt = unixTime(used)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (manager *Manager) AuthenticateAccessToken(ctx context.Context, secret string) bool {
	if len(secret) < 16 || len(secret) > 4096 || !utf8.ValidString(secret) || strings.ContainsAny(secret, "\r\n\x00") {
		return false
	}
	hash := tokenHash(secret)
	var id, candidate string
	if err := manager.db.QueryRowContext(ctx, `SELECT id, token_hash FROM fleet_access_tokens WHERE token_hash = ?`, hash).Scan(&id, &candidate); err != nil {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(candidate)) != 1 {
		return false
	}
	_, _ = manager.db.ExecContext(ctx, `UPDATE fleet_access_tokens SET last_used_at = ? WHERE id = ?`, manager.now().UTC().Unix(), id)
	return true
}

func (manager *Manager) RevokeAccessToken(ctx context.Context, id string) error {
	result, err := manager.db.ExecContext(ctx, `DELETE FROM fleet_access_tokens WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) AddPeer(ctx context.Context, input AddPeerInput) (Peer, error) {
	name := strings.TrimSpace(input.Name)
	endpoint, err := normalizeEndpoint(input.Endpoint)
	if !validLabel(name, 64) || err != nil || !validSecret(input.AccessToken) {
		return Peer{}, errors.New("ScriptBoard node configuration is invalid")
	}
	var count int
	if err := manager.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fleet_peers`).Scan(&count); err != nil {
		return Peer{}, err
	}
	if count >= maxPeers {
		return Peer{}, errors.New("ScriptBoard node limit reached")
	}
	id, err := randomID(18)
	if err != nil {
		return Peer{}, err
	}
	overview, err := manager.fetch(ctx, endpoint, input.AccessToken)
	if err != nil {
		return Peer{}, err
	}
	ciphertext, err := manager.vault.Seal(credentialUse, []byte(input.AccessToken))
	if err != nil {
		return Peer{}, err
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		return Peer{}, err
	}
	now := manager.now().UTC()
	_, err = manager.db.ExecContext(ctx, `INSERT INTO fleet_peers
		(id, name, endpoint, access_token_cipher, overview_json, last_seen_at, last_attempt_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, name, endpoint, ciphertext, string(encoded), now.Unix(), now.Unix(), now.Unix(), now.Unix())
	if err != nil {
		return Peer{}, err
	}
	return Peer{ID: id, Name: name, Endpoint: endpoint, Enabled: true, Overview: overview, LastSeenAt: now, LastAttemptAt: now, CreatedAt: now, UpdatedAt: now}, nil
}

// UpdatePeer validates the replacement connection before committing it. A
// blank access token preserves the existing encrypted credential.
func (manager *Manager) UpdatePeer(ctx context.Context, id string, input UpdatePeerInput) (Peer, error) {
	id = strings.TrimSpace(id)
	name := strings.TrimSpace(input.Name)
	endpoint, err := normalizeEndpoint(input.Endpoint)
	if id == "" || !validLabel(name, 64) || err != nil {
		return Peer{}, errors.New("ScriptBoard node configuration is invalid")
	}

	var existingCiphertext []byte
	if err := manager.db.QueryRowContext(ctx, `SELECT access_token_cipher FROM fleet_peers WHERE id = ?`, id).Scan(&existingCiphertext); err != nil {
		return Peer{}, err
	}
	token := input.AccessToken
	keepExistingToken := token == ""
	var plaintext []byte
	if keepExistingToken {
		plaintext, err = manager.vault.Unseal(credentialUse, existingCiphertext)
		if err != nil {
			return Peer{}, err
		}
		token = string(plaintext)
	} else if !validSecret(token) {
		return Peer{}, errors.New("ScriptBoard node configuration is invalid")
	}
	if len(plaintext) > 0 {
		defer func() {
			for index := range plaintext {
				plaintext[index] = 0
			}
		}()
	}

	overview, err := manager.fetch(ctx, endpoint, token)
	if err != nil {
		return Peer{}, err
	}
	ciphertext := existingCiphertext
	if !keepExistingToken {
		ciphertext, err = manager.vault.Seal(credentialUse, []byte(token))
		if err != nil {
			return Peer{}, err
		}
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		return Peer{}, err
	}
	now := manager.now().UTC().Unix()
	result, err := manager.db.ExecContext(ctx, `UPDATE fleet_peers SET name = ?, endpoint = ?, access_token_cipher = ?, overview_json = ?, last_seen_at = ?, last_attempt_at = ?, last_error = '', updated_at = ? WHERE id = ?`,
		name, endpoint, ciphertext, string(encoded), now, now, now, id)
	if err != nil {
		return Peer{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Peer{}, sql.ErrNoRows
	}
	return manager.Peer(ctx, id)
}

func (manager *Manager) DeletePeer(ctx context.Context, id string) error {
	result, err := manager.db.ExecContext(ctx, `DELETE FROM fleet_peers WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) ListPeers(ctx context.Context) ([]Peer, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, name, endpoint, enabled, overview_json, last_seen_at, last_attempt_at, last_error, created_at, updated_at FROM fleet_peers ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Peer
	for rows.Next() {
		var peer Peer
		var enabled int
		var encoded string
		var seen, attempted, created, updated int64
		if err := rows.Scan(&peer.ID, &peer.Name, &peer.Endpoint, &enabled, &encoded, &seen, &attempted, &peer.LastError, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &peer.Overview); err != nil {
			return nil, fmt.Errorf("decode node %s snapshot: %w", peer.ID, err)
		}
		peer.Enabled = enabled == 1
		peer.LastSeenAt, peer.LastAttemptAt = unixTime(seen), unixTime(attempted)
		peer.CreatedAt, peer.UpdatedAt = unixTime(created), unixTime(updated)
		result = append(result, peer)
	}
	return result, rows.Err()
}

func (manager *Manager) Peer(ctx context.Context, id string) (Peer, error) {
	peers, err := manager.ListPeers(ctx)
	if err != nil {
		return Peer{}, err
	}
	for _, peer := range peers {
		if peer.ID == id {
			return peer, nil
		}
	}
	return Peer{}, sql.ErrNoRows
}

func (manager *Manager) RefreshPeer(ctx context.Context, id string) error {
	var endpoint string
	var sealed []byte
	if err := manager.db.QueryRowContext(ctx, `SELECT endpoint, access_token_cipher FROM fleet_peers WHERE id = ? AND enabled = 1`, id).Scan(&endpoint, &sealed); err != nil {
		return err
	}
	plain, err := manager.vault.Unseal(credentialUse, sealed)
	if err != nil {
		return manager.recordFailure(ctx, id, err)
	}
	overview, err := manager.fetch(ctx, endpoint, string(plain))
	for index := range plain {
		plain[index] = 0
	}
	if err != nil {
		return manager.recordFailure(ctx, id, err)
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		return manager.recordFailure(ctx, id, err)
	}
	now := manager.now().UTC().Unix()
	_, err = manager.db.ExecContext(ctx, `UPDATE fleet_peers SET overview_json = ?, last_seen_at = ?, last_attempt_at = ?, last_error = '', updated_at = ? WHERE id = ?`, string(encoded), now, now, now, id)
	return err
}

func (manager *Manager) RefreshAll(ctx context.Context) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id FROM fleet_peers WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	semaphore := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			_ = manager.RefreshPeer(ctx, id)
		}()
	}
	wg.Wait()
}

func (manager *Manager) recordFailure(ctx context.Context, id string, cause error) error {
	now := manager.now().UTC().Unix()
	message := strings.TrimSpace(cause.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	_, _ = manager.db.ExecContext(ctx, `UPDATE fleet_peers SET last_attempt_at = ?, last_error = ?, updated_at = ? WHERE id = ?`, now, message, now, id)
	return cause
}

func (manager *Manager) fetch(ctx context.Context, endpoint, token string) (hoststatus.Overview, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+ExportPath, nil)
	if err != nil {
		return hoststatus.Overview{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ScriptBoard-Fleet/1")
	response, err := manager.client.Do(request)
	if err != nil {
		return hoststatus.Overview{}, fmt.Errorf("connect to ScriptBoard node: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return hoststatus.Overview{}, fmt.Errorf("ScriptBoard node returned HTTP %d", response.StatusCode)
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		return hoststatus.Overview{}, errors.New("ScriptBoard node returned an unexpected content type")
	}
	limited := io.LimitReader(response.Body, maxResponseSize+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var export Export
	if err := decoder.Decode(&export); err != nil {
		return hoststatus.Overview{}, errors.New("ScriptBoard node returned invalid status data")
	}
	if export.ProtocolVersion != ProtocolVersion || decoder.Decode(&struct{}{}) != io.EOF {
		return hoststatus.Overview{}, errors.New("ScriptBoard node protocol is incompatible")
	}
	if err := validateOverview(export.Overview); err != nil {
		return hoststatus.Overview{}, err
	}
	return export.Overview, nil
}

func validateOverview(value hoststatus.Overview) error {
	if (value.Facts.Hostname != "" && !validLabel(value.Facts.Hostname, 255)) || len(value.Facts.Platform) > 128 || len(value.Facts.PlatformVersion) > 128 || len(value.Facts.Architecture) > 32 || len(value.Facts.CPUModel) > 256 || len(value.Current.Filesystems) > 128 || len(value.Current.Disks) > 128 || len(value.Current.Interfaces) > 128 || len(value.Errors) > 32 {
		return errors.New("ScriptBoard node status exceeds the supported bounds")
	}
	percentages := []float64{}
	if value.Current.CPU != nil {
		percentages = append(percentages, value.Current.CPU.UsedPercent, value.Current.CPU.UserPercent, value.Current.CPU.SystemPercent, value.Current.CPU.IOWaitPercent)
	}
	if value.Current.Memory != nil {
		percentages = append(percentages, value.Current.Memory.UsedPercent, value.Current.Memory.SwapUsedPercent)
	}
	if value.Current.Storage != nil {
		percentages = append(percentages, value.Current.Storage.UsedPercent)
	}
	for _, percentage := range percentages {
		if math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0 || percentage > 100 {
			return errors.New("ScriptBoard node status contains an invalid percentage")
		}
	}
	return nil
}

func normalizeEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("node endpoint must be an HTTP or HTTPS origin")
	}
	if len(parsed.Host) > 255 || strings.ContainsAny(parsed.Host, "\r\n\x00") {
		return "", errors.New("node endpoint is invalid")
	}
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func validLabel(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && len([]rune(value)) <= maximum && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validSecret(value string) bool {
	return len(value) >= 16 && len(value) <= 4096 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func randomID(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func tokenHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}
