package externaltrigger

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
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ActionLog      ActionType = "log"
	ActionUpload   ActionType = "upload"
	ActionQuickRun ActionType = "quick_run"
	ActionVariable ActionType = "variable"
)

const (
	VariableBoolean VariableType = "boolean"
	VariableInteger VariableType = "integer"
	VariableEnum    VariableType = "enum"
	VariableText    VariableType = "text"
)

var (
	ErrInvalidKey     = errors.New("external trigger key is invalid")
	ErrEntryNotFound  = errors.New("external trigger entry does not exist")
	ErrEntryDisabled  = errors.New("external trigger entry is disabled")
	ErrInvalidInput   = errors.New("external trigger input is invalid")
	ErrKeyLabelExists = errors.New("external trigger key name already exists")
	entryNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS external_trigger_keys (
		id TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		token_hint TEXT NOT NULL,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		expires_at INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		last_used_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS external_trigger_entries (
		id TEXT PRIMARY KEY,
		key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		label TEXT NOT NULL,
		action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable')),
		target TEXT NOT NULL DEFAULT '',
		config_json TEXT NOT NULL DEFAULT '{}',
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (key_id, name)
	)`,
	`CREATE TABLE IF NOT EXISTS external_trigger_requests (
		id TEXT PRIMARY KEY,
		occurred_at INTEGER NOT NULL,
		key_id TEXT NOT NULL,
		key_label TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		entry_name TEXT NOT NULL,
		action_type TEXT NOT NULL,
		result TEXT NOT NULL,
		http_status INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		bytes_received INTEGER NOT NULL DEFAULT 0,
		run_id TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		source_address TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_entries_key_idx ON external_trigger_entries(key_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_requests_time_idx ON external_trigger_requests(occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_requests_key_time_idx ON external_trigger_requests(key_id, occurred_at DESC)`,
}

type ActionType string
type VariableType string

type Key struct {
	ID, Label, TokenHint string
	Enabled              bool
	ExpiresAt            *time.Time
	CreatedAt, UpdatedAt time.Time
	LastUsedAt           *time.Time
	Entries              []Entry
}

func (key Key) Active(now time.Time) bool {
	return key.Enabled && (key.ExpiresAt == nil || now.Before(*key.ExpiresAt))
}

func (key Key) Expired(now time.Time) bool {
	return key.ExpiresAt != nil && !now.Before(*key.ExpiresAt)
}

type Entry struct {
	ID, KeyID, Name, Label, Target string
	Type                           ActionType
	ConfigJSON                     string
	Enabled                        bool
	CreatedAt, UpdatedAt           time.Time
}

func (entry Entry) DecodeConfig(destination any) error {
	if err := json.Unmarshal([]byte(entry.ConfigJSON), destination); err != nil {
		return fmt.Errorf("decode external trigger config: %w", err)
	}
	return nil
}

type LogConfig struct {
	File            string `json:"file"`
	Category        string `json:"category"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

type UploadConfig struct {
	Directory      string   `json:"directory"`
	MaxBytes       int64    `json:"max_bytes"`
	Extensions     []string `json:"extensions,omitempty"`
	ConflictPolicy string   `json:"conflict_policy"`
}

type QuickRunConfig struct {
	QuickRunID string `json:"quick_run_id"`
}

type VariableConfig struct {
	VariableName string       `json:"variable_name"`
	Type         VariableType `json:"type"`
	Minimum      *int64       `json:"minimum,omitempty"`
	Maximum      *int64       `json:"maximum,omitempty"`
	Options      []string     `json:"options,omitempty"`
	MaxLength    int          `json:"max_length,omitempty"`
	Pattern      string       `json:"pattern,omitempty"`
	AllowEmpty   bool         `json:"allow_empty"`
}

type CreateKeyInput struct {
	Label     string
	Enabled   bool
	ExpiresAt *time.Time
}

type CreateEntryInput struct {
	KeyID, Name, Label string
	Type               ActionType
	Target             string
	Enabled            bool
	Config             any
}

type UpdateEntryInput struct {
	ID, Name, Label string
	Type            ActionType
	Target          string
	Enabled         bool
	Config          any
}

type Invocation struct {
	ID                     string
	OccurredAt             time.Time
	KeyID, KeyLabel        string
	EntryID, EntryName     string
	ActionType             ActionType
	Result                 string
	HTTPStatus             int
	Duration               time.Duration
	BytesReceived          int64
	RunID, Message, Source string
}

type InvocationFilter struct {
	Query                     string
	FromUnix, ToExclusiveUnix int64
	HasFromDate, HasToDate    bool
}

func (manager *Manager) ListInvocations(ctx context.Context, limit int) ([]Invocation, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	return manager.FindInvocations(ctx, InvocationFilter{}, limit, 0)
}

func (manager *Manager) CountInvocations(ctx context.Context, filter InvocationFilter) (int, error) {
	like := "%" + strings.TrimSpace(filter.Query) + "%"
	var total int
	err := manager.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_trigger_requests
		WHERE (? = '' OR key_label LIKE ? OR entry_name LIKE ? OR action_type LIKE ? OR result LIKE ?
			OR CAST(http_status AS TEXT) LIKE ? OR run_id LIKE ? OR message LIKE ? OR source_address LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)`,
		strings.TrimSpace(filter.Query), like, like, like, like, like, like, like, like,
		filter.HasFromDate, filter.FromUnix,
		filter.HasToDate, filter.ToExclusiveUnix).Scan(&total)
	return total, err
}

func (manager *Manager) FindInvocations(ctx context.Context, filter InvocationFilter, limit, offset int) ([]Invocation, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := strings.TrimSpace(filter.Query)
	like := "%" + query + "%"
	rows, err := manager.db.QueryContext(ctx, `SELECT id, occurred_at, key_id, key_label, entry_id, entry_name, action_type,
		result, http_status, duration_ms, bytes_received, run_id, message, source_address
		FROM external_trigger_requests
		WHERE (? = '' OR key_label LIKE ? OR entry_name LIKE ? OR action_type LIKE ? OR result LIKE ?
			OR CAST(http_status AS TEXT) LIKE ? OR run_id LIKE ? OR message LIKE ? OR source_address LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)
		ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`,
		query, like, like, like, like, like, like, like, like,
		filter.HasFromDate, filter.FromUnix,
		filter.HasToDate, filter.ToExclusiveUnix,
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invocations []Invocation
	for rows.Next() {
		var invocation Invocation
		var occurredAt, durationMS int64
		if err := rows.Scan(&invocation.ID, &occurredAt, &invocation.KeyID, &invocation.KeyLabel, &invocation.EntryID, &invocation.EntryName,
			&invocation.ActionType, &invocation.Result, &invocation.HTTPStatus, &durationMS, &invocation.BytesReceived, &invocation.RunID,
			&invocation.Message, &invocation.Source); err != nil {
			return nil, err
		}
		invocation.OccurredAt = time.Unix(occurredAt, 0).UTC()
		invocation.Duration = time.Duration(durationMS) * time.Millisecond
		invocations = append(invocations, invocation)
	}
	return invocations, rows.Err()
}

type Options struct {
	Now              func() time.Time
	Random           func([]byte) (int, error)
	SecretsDirectory string
}

type Manager struct {
	db               *sql.DB
	now              func() time.Time
	random           func([]byte) (int, error)
	secretsDirectory string
	secretStore      *encryptedSecretStore
}

func New(db *sql.DB, options Options) *Manager {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	random := options.Random
	if random == nil {
		random = rand.Read
	}
	return &Manager{db: db, now: now, random: random, secretsDirectory: options.SecretsDirectory, secretStore: &encryptedSecretStore{directory: options.SecretsDirectory}}
}

func (manager *Manager) KeySecret(id string) (string, error) {
	return manager.secretStore.get(id)
}

func (manager *Manager) CreateKey(ctx context.Context, input CreateKeyInput) (Key, string, error) {
	label := strings.TrimSpace(input.Label)
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return Key{}, "", fmt.Errorf("%w: key label", ErrInvalidInput)
	}
	now := manager.now().UTC()
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return Key{}, "", fmt.Errorf("%w: key expiry", ErrInvalidInput)
	}
	id, err := manager.randomToken(12)
	if err != nil {
		return Key{}, "", err
	}
	secretPart, err := manager.randomToken(32)
	if err != nil {
		return Key{}, "", err
	}
	secret := "sbk_" + id + "." + secretPart
	hint := "sbk_" + id + ".••••" + secretPart[len(secretPart)-4:]
	var expiresAt any
	if input.ExpiresAt != nil {
		expiresAt = input.ExpiresAt.UTC().Unix()
	}
	result, err := manager.db.ExecContext(ctx, `INSERT INTO external_trigger_keys
		(id, label, token_hash, token_hint, enabled, expires_at, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM external_trigger_keys WHERE label = ? COLLATE NOCASE
		)`, id, label, hashToken(secret), hint, input.Enabled, expiresAt, now.Unix(), now.Unix(), label)
	if err != nil {
		return Key{}, "", fmt.Errorf("create external trigger key: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Key{}, "", ErrKeyLabelExists
	}
	if err := manager.secretStore.set(id, secret); err != nil {
		_, _ = manager.db.ExecContext(ctx, "DELETE FROM external_trigger_keys WHERE id = ?", id)
		return Key{}, "", fmt.Errorf("store external trigger key: %w", err)
	}
	key := Key{ID: id, Label: label, TokenHint: hint, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
	if input.ExpiresAt != nil {
		expires := input.ExpiresAt.UTC()
		key.ExpiresAt = &expires
	}
	return key, secret, nil
}

func (manager *Manager) RotateKey(ctx context.Context, id string) (Key, string, error) {
	key, err := manager.Key(ctx, id)
	if err != nil {
		return Key{}, "", err
	}
	secretPart, err := manager.randomToken(32)
	if err != nil {
		return Key{}, "", err
	}
	secret := "sbk_" + id + "." + secretPart
	hint := "sbk_" + id + ".••••" + secretPart[len(secretPart)-4:]
	now := manager.now().UTC()
	previous, previousErr := manager.secretStore.get(id)
	if err := manager.secretStore.set(id, secret); err != nil {
		return Key{}, "", fmt.Errorf("store rotated external trigger key: %w", err)
	}
	if _, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_keys SET token_hash = ?, token_hint = ?, updated_at = ? WHERE id = ?`, hashToken(secret), hint, now.Unix(), id); err != nil {
		if previousErr == nil {
			_ = manager.secretStore.set(id, previous)
		} else {
			_ = manager.secretStore.delete(id)
		}
		return Key{}, "", fmt.Errorf("rotate external trigger key: %w", err)
	}
	key.TokenHint, key.UpdatedAt = hint, now
	return key, secret, nil
}

func (manager *Manager) CreateEntry(ctx context.Context, input CreateEntryInput) (Entry, error) {
	configJSON, target, err := validateEntry(input.Name, input.Label, input.Type, input.Target, input.Config)
	if err != nil {
		return Entry{}, err
	}
	id, err := manager.randomToken(18)
	if err != nil {
		return Entry{}, err
	}
	now := manager.now().UTC()
	_, err = manager.db.ExecContext(ctx, `INSERT INTO external_trigger_entries
		(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.KeyID, input.Name, strings.TrimSpace(input.Label), input.Type, target, configJSON, input.Enabled, now.Unix(), now.Unix())
	if err != nil {
		return Entry{}, fmt.Errorf("create external trigger entry: %w", err)
	}
	return Entry{ID: id, KeyID: input.KeyID, Name: input.Name, Label: strings.TrimSpace(input.Label), Type: input.Type, Target: target, ConfigJSON: configJSON, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}, nil
}

func (manager *Manager) UpdateEntry(ctx context.Context, input UpdateEntryInput) (Entry, error) {
	configJSON, target, err := validateEntry(input.Name, input.Label, input.Type, input.Target, input.Config)
	if err != nil {
		return Entry{}, err
	}
	now := manager.now().UTC()
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_entries SET
		name = ?, label = ?, action_type = ?, target = ?, config_json = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		input.Name, strings.TrimSpace(input.Label), input.Type, target, configJSON, input.Enabled, now.Unix(), input.ID)
	if err != nil {
		return Entry{}, fmt.Errorf("update external trigger entry: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Entry{}, ErrEntryNotFound
	}
	return manager.Entry(ctx, input.ID)
}

func validateEntry(name, label string, actionType ActionType, target string, config any) (string, string, error) {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if !entryNamePattern.MatchString(name) || label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return "", "", fmt.Errorf("%w: entry identity", ErrInvalidInput)
	}
	var normalized any
	switch actionType {
	case ActionLog:
		value, ok := config.(LogConfig)
		value.File = strings.TrimSpace(value.File)
		value.Category = strings.TrimSpace(value.Category)
		if !ok || value.File == "" || len([]byte(value.File)) > 4096 || !utf8.ValidString(value.File) ||
			len([]byte(value.Category)) > 64 || !utf8.ValidString(value.Category) || strings.IndexFunc(value.Category, unicode.IsControl) >= 0 ||
			value.MaxMessageBytes < 0 || value.MaxMessageBytes > 4<<10 {
			return "", "", fmt.Errorf("%w: log config", ErrInvalidInput)
		}
		if value.MaxMessageBytes == 0 {
			value.MaxMessageBytes = 1024
		}
		normalized, target = value, value.File
	case ActionUpload:
		value, ok := config.(UploadConfig)
		if !ok || strings.TrimSpace(value.Directory) == "" || value.MaxBytes <= 0 || value.MaxBytes > 1<<30 || (value.ConflictPolicy != "reject" && value.ConflictPolicy != "rename") || len(value.Extensions) > 32 {
			return "", "", fmt.Errorf("%w: upload config", ErrInvalidInput)
		}
		for index, extension := range value.Extensions {
			extension = strings.ToLower(strings.TrimSpace(extension))
			if extension == "" || !strings.HasPrefix(extension, ".") || len(extension) > 32 || strings.ContainsAny(extension, `/\`) {
				return "", "", fmt.Errorf("%w: upload extension", ErrInvalidInput)
			}
			value.Extensions[index] = extension
		}
		slices.Sort(value.Extensions)
		value.Extensions = slices.Compact(value.Extensions)
		value.Directory = strings.TrimSpace(value.Directory)
		normalized, target = value, value.Directory
	case ActionQuickRun:
		value, ok := config.(QuickRunConfig)
		if !ok || strings.TrimSpace(value.QuickRunID) == "" {
			return "", "", fmt.Errorf("%w: quick run config", ErrInvalidInput)
		}
		value.QuickRunID = strings.TrimSpace(value.QuickRunID)
		normalized, target = value, value.QuickRunID
	case ActionVariable:
		value, ok := config.(VariableConfig)
		if !ok || strings.TrimSpace(value.VariableName) == "" {
			return "", "", fmt.Errorf("%w: variable config", ErrInvalidInput)
		}
		value.VariableName = strings.TrimSpace(value.VariableName)
		if err := validateVariableConfig(value); err != nil {
			return "", "", err
		}
		normalized, target = value, value.VariableName
	default:
		return "", "", fmt.Errorf("%w: action type", ErrInvalidInput)
	}
	content, err := json.Marshal(normalized)
	if err != nil {
		return "", "", fmt.Errorf("encode external trigger config: %w", err)
	}
	return string(content), target, nil
}

func validateVariableConfig(value VariableConfig) error {
	switch value.Type {
	case VariableBoolean:
		if value.Minimum != nil || value.Maximum != nil || len(value.Options) != 0 || value.MaxLength != 0 || value.Pattern != "" {
			return fmt.Errorf("%w: boolean constraints", ErrInvalidInput)
		}
	case VariableInteger:
		if value.Minimum != nil && value.Maximum != nil && *value.Minimum > *value.Maximum {
			return fmt.Errorf("%w: integer range", ErrInvalidInput)
		}
	case VariableEnum:
		if len(value.Options) < 2 || len(value.Options) > 50 {
			return fmt.Errorf("%w: enum options", ErrInvalidInput)
		}
		seen := make(map[string]struct{}, len(value.Options))
		for _, option := range value.Options {
			if option == "" || len([]byte(option)) > 256 || !utf8.ValidString(option) {
				return fmt.Errorf("%w: enum option", ErrInvalidInput)
			}
			if _, exists := seen[option]; exists {
				return fmt.Errorf("%w: duplicate enum option", ErrInvalidInput)
			}
			seen[option] = struct{}{}
		}
	case VariableText:
		if value.MaxLength < 1 || value.MaxLength > 256 {
			return fmt.Errorf("%w: text length", ErrInvalidInput)
		}
		if value.Pattern != "" {
			if len(value.Pattern) > 256 {
				return fmt.Errorf("%w: text pattern", ErrInvalidInput)
			}
			if _, err := regexp.Compile("^(?:" + value.Pattern + ")$"); err != nil {
				return fmt.Errorf("%w: text pattern", ErrInvalidInput)
			}
		}
	default:
		return fmt.Errorf("%w: variable type", ErrInvalidInput)
	}
	return nil
}

func (manager *Manager) Resolve(ctx context.Context, token, name string) (Key, Entry, error) {
	id, ok := tokenKeyID(token)
	if !ok {
		return Key{}, Entry{}, ErrInvalidKey
	}
	var key Key
	var tokenHash string
	var enabled int
	var expiresAt, lastUsedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, label, token_hash, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
		FROM external_trigger_keys WHERE id = ?`, id).Scan(&key.ID, &key.Label, &tokenHash, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt)
	if err != nil || subtle.ConstantTimeCompare([]byte(tokenHash), []byte(hashToken(token))) != 1 {
		return Key{}, Entry{}, ErrInvalidKey
	}
	key.Enabled = enabled != 0
	key.CreatedAt, key.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	setOptionalTimes(&key, expiresAt, lastUsedAt)
	if !key.Active(manager.now().UTC()) {
		return Key{}, Entry{}, ErrInvalidKey
	}
	var entry Entry
	var entryEnabled int
	var entryCreatedAt, entryUpdatedAt int64
	err = manager.db.QueryRowContext(ctx, `SELECT id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE key_id = ? AND name = ?`, key.ID, name).Scan(
		&entry.ID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &entryEnabled, &entryCreatedAt, &entryUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return key, Entry{}, ErrEntryNotFound
	}
	if err != nil {
		return Key{}, Entry{}, err
	}
	entry.Enabled = entryEnabled != 0
	entry.CreatedAt, entry.UpdatedAt = time.Unix(entryCreatedAt, 0).UTC(), time.Unix(entryUpdatedAt, 0).UTC()
	if !entry.Enabled {
		return key, entry, ErrEntryDisabled
	}
	return key, entry, nil
}

func (manager *Manager) Key(ctx context.Context, id string) (Key, error) {
	var key Key
	var enabled int
	var expiresAt, lastUsedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, label, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
		FROM external_trigger_keys WHERE id = ?`, id).Scan(&key.ID, &key.Label, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt)
	if err != nil {
		return Key{}, err
	}
	key.Enabled = enabled != 0
	key.CreatedAt, key.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	setOptionalTimes(&key, expiresAt, lastUsedAt)
	return key, nil
}

func (manager *Manager) Entry(ctx context.Context, id string) (Entry, error) {
	var entry Entry
	var enabled int
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE id = ?`, id).Scan(&entry.ID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return Entry{}, err
	}
	entry.Enabled = enabled != 0
	entry.CreatedAt, entry.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return entry, nil
}

func (manager *Manager) List(ctx context.Context) ([]Key, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, label, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
		FROM external_trigger_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		var key Key
		var enabled int
		var expiresAt, lastUsedAt sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&key.ID, &key.Label, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		key.Enabled = enabled != 0
		key.CreatedAt, key.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		setOptionalTimes(&key, expiresAt, lastUsedAt)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range keys {
		entries, err := manager.entriesForKey(ctx, keys[index].ID)
		if err != nil {
			return nil, err
		}
		keys[index].Entries = entries
	}
	return keys, nil
}

func (manager *Manager) entriesForKey(ctx context.Context, keyID string) ([]Entry, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE key_id = ? ORDER BY created_at, id`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(&entry.ID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		entry.Enabled = enabled != 0
		entry.CreatedAt, entry.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (manager *Manager) SetKeyEnabled(ctx context.Context, id string, enabled bool) error {
	return manager.updateBoolean(ctx, "external_trigger_keys", id, enabled)
}

func (manager *Manager) SetEntryEnabled(ctx context.Context, id string, enabled bool) error {
	return manager.updateBoolean(ctx, "external_trigger_entries", id, enabled)
}

func (manager *Manager) updateBoolean(ctx context.Context, table, id string, enabled bool) error {
	result, err := manager.db.ExecContext(ctx, `UPDATE `+table+` SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, manager.now().UTC().Unix(), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) UpdateKey(ctx context.Context, id, label string, expiresAt *time.Time) error {
	label = strings.TrimSpace(label)
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return fmt.Errorf("%w: key label", ErrInvalidInput)
	}
	var expires any
	if expiresAt != nil {
		expires = expiresAt.UTC().Unix()
	}
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_keys
		SET label = ?, expires_at = ?, updated_at = ?
		WHERE id = ? AND NOT EXISTS (
			SELECT 1 FROM external_trigger_keys AS duplicate
			WHERE duplicate.id <> ? AND duplicate.label = ? COLLATE NOCASE
		)`, label, expires, manager.now().UTC().Unix(), id, id, label)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		if _, keyErr := manager.Key(ctx, id); keyErr != nil {
			return keyErr
		}
		return ErrKeyLabelExists
	}
	return nil
}

func (manager *Manager) DeleteKey(ctx context.Context, id string) error {
	result, err := manager.db.ExecContext(ctx, "DELETE FROM external_trigger_keys WHERE id = ?", id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	if err := manager.secretStore.delete(id); err != nil && !errors.Is(err, ErrSecretUnavailable) {
		return fmt.Errorf("delete external trigger key secret: %w", err)
	}
	return nil
}

func (manager *Manager) DeleteEntry(ctx context.Context, id string) error {
	result, err := manager.db.ExecContext(ctx, "DELETE FROM external_trigger_entries WHERE id = ?", id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) RecordInvocation(ctx context.Context, invocation Invocation) error {
	if invocation.ID == "" {
		var err error
		invocation.ID, err = manager.randomToken(18)
		if err != nil {
			return err
		}
	}
	if invocation.OccurredAt.IsZero() {
		invocation.OccurredAt = manager.now().UTC()
	}
	if len([]byte(invocation.Message)) > 4<<10 {
		invocation.Message = ""
	}
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `INSERT INTO external_trigger_requests
		(id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, result, http_status, duration_ms, bytes_received, run_id, message, source_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, invocation.ID, invocation.OccurredAt.Unix(), invocation.KeyID, invocation.KeyLabel,
		invocation.EntryID, invocation.EntryName, invocation.ActionType, invocation.Result, invocation.HTTPStatus, invocation.Duration.Milliseconds(),
		invocation.BytesReceived, invocation.RunID, invocation.Message, invocation.Source)
	if err != nil {
		return err
	}
	if invocation.Result == "succeeded" || invocation.Result == "accepted" {
		if _, err := transaction.ExecContext(ctx, "UPDATE external_trigger_keys SET last_used_at = ? WHERE id = ?", invocation.OccurredAt.Unix(), invocation.KeyID); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM external_trigger_requests WHERE occurred_at < ? OR id IN
		(SELECT id FROM external_trigger_requests ORDER BY occurred_at DESC, id DESC LIMIT -1 OFFSET 10000)`, invocation.OccurredAt.AddDate(-1, 0, 0).Unix()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (manager *Manager) CompleteInvocation(ctx context.Context, invocation Invocation) error {
	if invocation.ID == "" {
		return fmt.Errorf("%w: invocation id", ErrInvalidInput)
	}
	if len([]byte(invocation.Message)) > 4<<10 {
		invocation.Message = ""
	}
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE external_trigger_requests SET
		result = ?, http_status = ?, duration_ms = ?, bytes_received = ?, run_id = ?, message = ?, source_address = ?
		WHERE id = ?`, invocation.Result, invocation.HTTPStatus, invocation.Duration.Milliseconds(), invocation.BytesReceived,
		invocation.RunID, invocation.Message, invocation.Source, invocation.ID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	if invocation.Result == "succeeded" || invocation.Result == "accepted" {
		usedAt := invocation.OccurredAt
		if usedAt.IsZero() {
			usedAt = manager.now().UTC()
		}
		if _, err := transaction.ExecContext(ctx, "UPDATE external_trigger_keys SET last_used_at = ? WHERE id = ?", usedAt.Unix(), invocation.KeyID); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func ValidateVariableValue(config VariableConfig, raw any) (string, error) {
	if err := validateVariableConfig(config); err != nil {
		return "", err
	}
	var value string
	switch typed := raw.(type) {
	case string:
		value = typed
	case bool:
		if typed {
			value = "true"
		} else {
			value = "false"
		}
	case float64:
		value = fmt.Sprintf("%v", typed)
	case json.Number:
		value = string(typed)
	default:
		return "", fmt.Errorf("%w: variable value type", ErrInvalidInput)
	}
	if value == "" && !config.AllowEmpty {
		return "", fmt.Errorf("%w: empty variable value", ErrInvalidInput)
	}
	switch config.Type {
	case VariableBoolean:
		if value != "true" && value != "false" {
			return "", fmt.Errorf("%w: boolean value", ErrInvalidInput)
		}
	case VariableInteger:
		var integer int64
		if _, err := fmt.Sscan(value, &integer); err != nil || fmt.Sprintf("%d", integer) != value {
			return "", fmt.Errorf("%w: integer value", ErrInvalidInput)
		}
		if config.Minimum != nil && integer < *config.Minimum || config.Maximum != nil && integer > *config.Maximum {
			return "", fmt.Errorf("%w: integer range", ErrInvalidInput)
		}
	case VariableEnum:
		if !slices.Contains(config.Options, value) {
			return "", fmt.Errorf("%w: enum value", ErrInvalidInput)
		}
	case VariableText:
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > config.MaxLength {
			return "", fmt.Errorf("%w: text value", ErrInvalidInput)
		}
		if config.Pattern != "" && !regexp.MustCompile("^(?:"+config.Pattern+")$").MatchString(value) {
			return "", fmt.Errorf("%w: text pattern", ErrInvalidInput)
		}
	}
	return value, nil
}

func (manager *Manager) randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := manager.random(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenKeyID(token string) (string, bool) {
	if !strings.HasPrefix(token, "sbk_") || len(token) > 128 {
		return "", false
	}
	remaining := strings.TrimPrefix(token, "sbk_")
	id, secret, ok := strings.Cut(remaining, ".")
	if !ok || len(id) != 16 || len(secret) != 43 {
		return "", false
	}
	return id, true
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func setOptionalTimes(key *Key, expiresAt, lastUsedAt sql.NullInt64) {
	if expiresAt.Valid {
		value := time.Unix(expiresAt.Int64, 0).UTC()
		key.ExpiresAt = &value
	}
	if lastUsedAt.Valid {
		value := time.Unix(lastUsedAt.Int64, 0).UTC()
		key.LastUsedAt = &value
	}
}
