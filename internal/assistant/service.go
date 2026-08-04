// Package assistant owns ScriptBoard's AI conversation state and the private
// configuration seam used by the web layer. Pi protocol and process details do
// not cross this package boundary.
package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	ProviderOpenAI                    = "openai"
	ProviderAnthropic                 = "anthropic"
	ProviderOpenAICompatible          = "openai-compatible"
	ProfileGeneral                    = "general"
	ProfileDiagnoseFailedRun          = "diagnose-failed-run"
	ProfileInvestigateWebsiteIncident = "investigate-website-incident"
	ProfileTriageHostPressure         = "triage-host-pressure"
	ProfileReviewScriptSafety         = "review-script-safety"
	ProfileDesignSchedule             = "design-schedule"
	CapabilityBundleVersion           = "1.0.0"
)

var (
	ErrNotFound         = errors.New("assistant record not found")
	ErrDisabled         = errors.New("assistant is disabled")
	ErrModelRequired    = errors.New("an LLM configuration is required")
	ErrModelUnavailable = errors.New("the selected LLM configuration is unavailable")
	ErrConversationBusy = errors.New("the assistant conversation already has an active turn")
	ErrInvalidInput     = errors.New("invalid assistant input")
)

const maxMessageRunes = 32768

// SchemaStatements are applied by the application's single schema transaction.
// The package does not migrate or open a second database connection itself.
var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS assistant_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL DEFAULT 0,
		max_active_conversations INTEGER NOT NULL DEFAULT 2 CHECK (max_active_conversations BETWEEN 1 AND 8),
		default_auto_approval INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0,
		updated_by_user_id TEXT NOT NULL DEFAULT ''
	)`,
	`INSERT OR IGNORE INTO assistant_settings (id) VALUES (1)`,
	`CREATE TABLE IF NOT EXISTS assistant_models (
		id TEXT PRIMARY KEY,
		owner_user_id TEXT NOT NULL,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		provider TEXT NOT NULL CHECK (provider IN ('openai', 'anthropic', 'openai-compatible')),
		model TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		credential_configured INTEGER NOT NULL DEFAULT 0,
		connection_ok INTEGER NOT NULL DEFAULT 0,
		supports_images INTEGER NOT NULL DEFAULT 0,
		is_shared INTEGER NOT NULL DEFAULT 0,
		is_default INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		updated_by_user_id TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS assistant_conversations (
		id TEXT PRIMARY KEY,
		owner_user_id TEXT NOT NULL,
		title TEXT NOT NULL,
		model_id TEXT NOT NULL REFERENCES assistant_models(id) ON DELETE RESTRICT,
		auto_approval INTEGER NOT NULL DEFAULT 0,
		pi_session_file TEXT NOT NULL DEFAULT '',
		runtime_version TEXT NOT NULL DEFAULT '',
		capability_profile TEXT NOT NULL DEFAULT 'general' CHECK (capability_profile IN ('general', 'diagnose-failed-run', 'investigate-website-incident', 'triage-host-pressure', 'review-script-safety', 'design-schedule')),
		profile_version TEXT NOT NULL DEFAULT '',
		thinking_level TEXT NOT NULL DEFAULT 'medium' CHECK (thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')),
		stats_user_messages INTEGER NOT NULL DEFAULT 0,
		stats_assistant_messages INTEGER NOT NULL DEFAULT 0,
		stats_tool_calls INTEGER NOT NULL DEFAULT 0,
		stats_tool_results INTEGER NOT NULL DEFAULT 0,
		stats_total_messages INTEGER NOT NULL DEFAULT 0,
		stats_input_tokens INTEGER NOT NULL DEFAULT 0,
		stats_output_tokens INTEGER NOT NULL DEFAULT 0,
		stats_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		stats_cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		stats_total_tokens INTEGER NOT NULL DEFAULT 0,
		stats_cost REAL NOT NULL DEFAULT 0,
		stats_context_tokens INTEGER NOT NULL DEFAULT 0,
		stats_context_window INTEGER NOT NULL DEFAULT 0,
		stats_context_percent REAL,
		stats_updated_at INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'running', 'waiting_approval', 'interrupted', 'failed')),
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		archived_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS assistant_conversations_owner_active_idx ON assistant_conversations(owner_user_id, archived_at, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS assistant_messages (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES assistant_conversations(id) ON DELETE CASCADE,
		sequence INTEGER NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
		body TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('streaming', 'complete', 'interrupted', 'error')),
		created_at INTEGER NOT NULL,
		finished_at INTEGER,
		UNIQUE (conversation_id, sequence)
	)`,
	`CREATE INDEX IF NOT EXISTS assistant_messages_conversation_idx ON assistant_messages(conversation_id, sequence)`,
	`CREATE TABLE IF NOT EXISTS assistant_context_refs (
		conversation_id TEXT NOT NULL REFERENCES assistant_conversations(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('directory', 'file', 'application', 'website', 'run', 'quick_run', 'schedule')),
		stable_id TEXT NOT NULL,
		label TEXT NOT NULL,
		position INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (conversation_id, kind, stable_id)
	)`,
	`CREATE TABLE IF NOT EXISTS assistant_tool_calls (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES assistant_conversations(id) ON DELETE CASCADE,
		message_id TEXT REFERENCES assistant_messages(id) ON DELETE SET NULL,
		body_offset INTEGER NOT NULL DEFAULT 0,
		tool_name TEXT NOT NULL,
		target_summary TEXT NOT NULL DEFAULT '',
		parameter_summary TEXT NOT NULL DEFAULT '',
		request_json TEXT NOT NULL DEFAULT '{}',
		response_json TEXT NOT NULL DEFAULT 'null',
		status TEXT NOT NULL,
		error_code TEXT NOT NULL DEFAULT '',
		result_summary TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS assistant_tool_calls_conversation_idx ON assistant_tool_calls(conversation_id, started_at)`,
	`CREATE TABLE IF NOT EXISTS assistant_approvals (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES assistant_conversations(id) ON DELETE CASCADE,
		tool_call_id TEXT NOT NULL REFERENCES assistant_tool_calls(id) ON DELETE CASCADE,
		parameter_digest TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'cancelled')),
		requested_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		decided_at INTEGER,
		decided_by_user_id TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS assistant_approvals_pending_idx ON assistant_approvals(conversation_id, status, expires_at)`,
}

type Options struct {
	StateRoot string
	Now       func() time.Time
}

type Actor struct {
	UserID   string
	Username string
}

type ModelInput struct {
	Name, Provider, Model, Endpoint, APIKey string
	MakeDefault, SupportsImages, Shared     bool
}

type ModelConfig struct {
	ID, OwnerUserID, Name, Provider, Model, Endpoint string
	CredentialConfigured, ConnectionOK               bool
	Default, SupportsImages, Shared                  bool
	Owned                                            bool
	CreatedAt, UpdatedAt                             time.Time
}

type SettingsInput struct {
	Enabled, DefaultAutoApproval bool
	MaxActiveConversations       int
}

type Settings struct {
	Enabled, DefaultAutoApproval bool
	MaxActiveConversations       int
	UpdatedAt                    time.Time
	UpdatedByUserID              string
}

type ConversationInput struct {
	Title, ModelID, InitialMessage, CapabilityProfile string
	Context                                           []ContextRef
	AutoApproval                                      *bool
}

type ContextRef struct {
	Kind, StableID, Label string
	CreatedAt             time.Time
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversationId"`
	Role           string     `json:"role"`
	Body           string     `json:"body"`
	Status         string     `json:"status"`
	Sequence       uint64     `json:"sequence"`
	CreatedAt      time.Time  `json:"createdAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

type Turn struct {
	User, Assistant Message
}

type ConversationFilter struct {
	Archived bool
	Query    string
}

type Conversation struct {
	ID, OwnerUserID, Title, ModelID, ModelName, Provider, Model string
	CapabilityProfile, ProfileVersion, ThinkingLevel            string
	AutoApproval                                                bool
	Status                                                      string
	Revision                                                    uint64
	Telemetry                                                   SessionTelemetry
	CreatedAt, UpdatedAt                                        time.Time
	ArchivedAt                                                  *time.Time
}

type SessionTelemetry struct {
	UserMessages, AssistantMessages, ToolCalls, ToolResults, TotalMessages    int64
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens, TotalTokens int64
	Cost                                                                      float64
	ContextTokens, ContextWindow                                              int64
	ContextPercent                                                            *float64
	UpdatedAt                                                                 time.Time
}

type Service struct {
	db             *sql.DB
	now            func() time.Time
	credentialPath string
	credentialMu   sync.Mutex
}

func New(db *sql.DB, options Options) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("assistant database is required")
	}
	stateRoot := strings.TrimSpace(options.StateRoot)
	if stateRoot == "" {
		return nil, fmt.Errorf("assistant State Root is required")
	}
	secretsDirectory := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretsDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("prepare assistant secrets directory: %w", err)
	}
	if err := os.Chmod(secretsDirectory, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return nil, fmt.Errorf("protect assistant secrets directory: %w", err)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, now: now, credentialPath: filepath.Join(secretsDirectory, "assistant-provider.json")}, nil
}

func (s *Service) SaveModel(ctx context.Context, actor Actor, id string, input ModelInput) (ModelConfig, error) {
	if strings.TrimSpace(actor.UserID) == "" {
		return ModelConfig{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	normalized, err := normalizeModelInput(input)
	if err != nil {
		return ModelConfig{}, err
	}
	creating := strings.TrimSpace(id) == ""
	if creating && normalized.APIKey == "" {
		return ModelConfig{}, fmt.Errorf("%w: provider credential is required", ErrInvalidInput)
	}
	if creating {
		id, err = randomID()
		if err != nil {
			return ModelConfig{}, fmt.Errorf("generate LLM configuration ID: %w", err)
		}
	} else {
		var ownerUserID string
		if err := s.db.QueryRowContext(ctx, "SELECT owner_user_id FROM assistant_models WHERE id = ?", id).Scan(&ownerUserID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ModelConfig{}, ErrNotFound
			}
			return ModelConfig{}, fmt.Errorf("read LLM configuration owner: %w", err)
		}
		if ownerUserID != actor.UserID {
			return ModelConfig{}, ErrNotFound
		}
	}

	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	credentials, err := s.loadCredentials()
	if err != nil {
		return ModelConfig{}, err
	}
	previousCredentials := cloneCredentials(credentials)
	if normalized.APIKey != "" {
		credentials[id] = normalized.APIKey
	}
	credentialConfigured := strings.TrimSpace(credentials[id]) != ""

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelConfig{}, fmt.Errorf("begin LLM configuration update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var conflictingID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM assistant_models WHERE name = ? COLLATE NOCASE AND id <> ?", normalized.Name, id).Scan(&conflictingID)
	if err == nil {
		return ModelConfig{}, fmt.Errorf("%w: LLM configuration name already exists", ErrInvalidInput)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ModelConfig{}, fmt.Errorf("check LLM configuration name: %w", err)
	}
	now := s.now().UTC()
	if normalized.MakeDefault {
		if _, err := tx.ExecContext(ctx, "UPDATE assistant_models SET is_default = 0 WHERE owner_user_id = ?", actor.UserID); err != nil {
			return ModelConfig{}, fmt.Errorf("clear default LLM configuration: %w", err)
		}
	}
	defaultValue := 0
	if normalized.MakeDefault {
		defaultValue = 1
	} else if creating {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_models WHERE owner_user_id = ?", actor.UserID).Scan(&count); err != nil {
			return ModelConfig{}, fmt.Errorf("count LLM configurations: %w", err)
		}
		if count == 0 {
			defaultValue = 1
		}
	}
	if creating {
		_, err = tx.ExecContext(ctx, `INSERT INTO assistant_models
			(id, owner_user_id, name, provider, model, endpoint, credential_configured, connection_ok, supports_images, is_shared, is_default, created_at, updated_at, updated_by_user_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)`,
			id, actor.UserID, normalized.Name, normalized.Provider, normalized.Model, normalized.Endpoint,
			boolInt(credentialConfigured), boolInt(normalized.SupportsImages), boolInt(normalized.Shared), defaultValue, now.UnixNano(), now.UnixNano(), actor.UserID)
	} else {
		if !normalized.MakeDefault {
			if err := tx.QueryRowContext(ctx, "SELECT is_default FROM assistant_models WHERE id = ? AND owner_user_id = ?", id, actor.UserID).Scan(&defaultValue); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ModelConfig{}, ErrNotFound
				}
				return ModelConfig{}, fmt.Errorf("read LLM configuration: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE assistant_models SET name = ?, provider = ?, model = ?, endpoint = ?,
			credential_configured = ?, connection_ok = 0, supports_images = ?, is_shared = ?, is_default = ?, updated_at = ?, updated_by_user_id = ? WHERE id = ? AND owner_user_id = ?`,
			normalized.Name, normalized.Provider, normalized.Model, normalized.Endpoint,
			boolInt(credentialConfigured), boolInt(normalized.SupportsImages), boolInt(normalized.Shared), defaultValue, now.UnixNano(), actor.UserID, id, actor.UserID)
	}
	if err != nil {
		return ModelConfig{}, fmt.Errorf("save LLM configuration: %w", err)
	}
	if normalized.APIKey != "" {
		if err := s.writeCredentials(credentials); err != nil {
			return ModelConfig{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		if normalized.APIKey != "" {
			_ = s.writeCredentials(previousCredentials)
		}
		return ModelConfig{}, fmt.Errorf("commit LLM configuration: %w", err)
	}
	model, err := s.model(ctx, id)
	model.Owned = err == nil && model.OwnerUserID == actor.UserID
	return model, err
}

func normalizeModelInput(input ModelInput) (ModelInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.Model = strings.TrimSpace(input.Model)
	input.Endpoint = strings.TrimRight(strings.TrimSpace(input.Endpoint), "/")
	input.APIKey = strings.TrimSpace(input.APIKey)
	if !boundedText(input.Name, 1, 48) || !boundedText(input.Model, 1, 160) {
		return ModelInput{}, fmt.Errorf("%w: LLM name or model is invalid", ErrInvalidInput)
	}
	switch input.Provider {
	case ProviderOpenAI, ProviderAnthropic, ProviderOpenAICompatible:
	default:
		return ModelInput{}, fmt.Errorf("%w: unsupported LLM provider", ErrInvalidInput)
	}
	endpoint, err := url.Parse(input.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return ModelInput{}, fmt.Errorf("%w: invalid LLM endpoint", ErrInvalidInput)
	}
	secure := endpoint.Scheme == "https"
	loopback := endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname())
	if !secure && !loopback {
		return ModelInput{}, fmt.Errorf("%w: LLM endpoint must use HTTPS or loopback HTTP", ErrInvalidInput)
	}
	if len(input.APIKey) > 8<<10 || strings.ContainsRune(input.APIKey, 0) || !utf8.ValidString(input.APIKey) {
		return ModelInput{}, fmt.Errorf("%w: invalid provider credential", ErrInvalidInput)
	}
	return input, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func boundedText(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func (s *Service) model(ctx context.Context, id string) (ModelConfig, error) {
	var model ModelConfig
	var credentialConfigured, connectionOK, defaultValue, supportsImages, shared int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_user_id, name, provider, model, endpoint, credential_configured, connection_ok, supports_images,
		is_shared, is_default, created_at, updated_at FROM assistant_models WHERE id = ?`, id).Scan(
		&model.ID, &model.OwnerUserID, &model.Name, &model.Provider, &model.Model, &model.Endpoint, &credentialConfigured,
		&connectionOK, &supportsImages, &shared, &defaultValue, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelConfig{}, ErrNotFound
	}
	if err != nil {
		return ModelConfig{}, fmt.Errorf("read LLM configuration: %w", err)
	}
	model.CredentialConfigured = credentialConfigured == 1
	model.ConnectionOK = connectionOK == 1
	model.SupportsImages = supportsImages == 1
	model.Shared = shared == 1
	model.Default = defaultValue == 1
	model.CreatedAt = time.Unix(0, createdAt).UTC()
	model.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return model, nil
}

func (s *Service) ListModels(ctx context.Context, actor Actor) ([]ModelConfig, error) {
	if strings.TrimSpace(actor.UserID) == "" {
		return nil, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, owner_user_id, name, provider, model, endpoint, credential_configured, connection_ok, supports_images,
		is_shared, is_default, created_at, updated_at FROM assistant_models WHERE owner_user_id = ? OR is_shared = 1
		ORDER BY CASE WHEN owner_user_id = ? THEN 0 ELSE 1 END, is_default DESC, created_at, name`, actor.UserID, actor.UserID)
	if err != nil {
		return nil, fmt.Errorf("list LLM configurations: %w", err)
	}
	defer rows.Close()
	models := make([]ModelConfig, 0)
	for rows.Next() {
		var model ModelConfig
		var credentialConfigured, connectionOK, defaultValue, supportsImages, shared int
		var createdAt, updatedAt int64
		if err := rows.Scan(&model.ID, &model.OwnerUserID, &model.Name, &model.Provider, &model.Model, &model.Endpoint,
			&credentialConfigured, &connectionOK, &supportsImages, &shared, &defaultValue, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan LLM configuration: %w", err)
		}
		model.CredentialConfigured = credentialConfigured == 1
		model.ConnectionOK = connectionOK == 1
		model.SupportsImages = supportsImages == 1
		model.Shared = shared == 1
		model.Owned = model.OwnerUserID == actor.UserID
		model.Default = model.Owned && defaultValue == 1
		model.CreatedAt = time.Unix(0, createdAt).UTC()
		model.UpdatedAt = time.Unix(0, updatedAt).UTC()
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate LLM configurations: %w", err)
	}
	return models, nil
}

func (s *Service) ModelForActor(ctx context.Context, actor Actor, id string) (ModelConfig, error) {
	if strings.TrimSpace(actor.UserID) == "" {
		return ModelConfig{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	model, err := s.model(ctx, strings.TrimSpace(id))
	if err != nil {
		return ModelConfig{}, err
	}
	if model.OwnerUserID != actor.UserID && !model.Shared {
		return ModelConfig{}, ErrNotFound
	}
	model.Owned = model.OwnerUserID == actor.UserID
	model.Default = model.Owned && model.Default
	return model, nil
}

func (s *Service) ModelCredential(ctx context.Context, id string) (string, error) {
	if _, err := s.model(ctx, id); err != nil {
		return "", err
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	credentials, err := s.loadCredentials()
	if err != nil {
		return "", err
	}
	credential := strings.TrimSpace(credentials[id])
	if credential == "" {
		return "", ErrModelUnavailable
	}
	return credential, nil
}

func (s *Service) Model(ctx context.Context, id string) (ModelConfig, error) {
	return s.model(ctx, id)
}

// SetModelConnectionOK records the latest observed provider connection result.
// It is informational only and never changes whether the model can be selected.
func (s *Service) SetModelConnectionOK(ctx context.Context, id string, ok bool) error {
	result, err := s.db.ExecContext(ctx, "UPDATE assistant_models SET connection_ok = ? WHERE id = ?", boolInt(ok), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("update LLM connection status: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetDefaultModel(ctx context.Context, actor Actor, id string) error {
	if strings.TrimSpace(actor.UserID) == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin default LLM update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var configured int
	if err := tx.QueryRowContext(ctx, "SELECT credential_configured FROM assistant_models WHERE id = ? AND owner_user_id = ?", id, actor.UserID).Scan(&configured); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read default LLM candidate: %w", err)
	}
	if configured != 1 {
		return ErrModelUnavailable
	}
	if _, err := tx.ExecContext(ctx, "UPDATE assistant_models SET is_default = 0 WHERE owner_user_id = ?", actor.UserID); err != nil {
		return fmt.Errorf("clear default LLM: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE assistant_models SET is_default = 1, updated_at = ?, updated_by_user_id = ? WHERE id = ? AND owner_user_id = ?", s.now().UTC().UnixNano(), actor.UserID, id, actor.UserID)
	if err != nil {
		return fmt.Errorf("set default LLM: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Service) DeleteModel(ctx context.Context, actor Actor, id string) error {
	if strings.TrimSpace(actor.UserID) == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	credentials, err := s.loadCredentials()
	if err != nil {
		return err
	}
	previous := cloneCredentials(credentials)
	delete(credentials, id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin LLM deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var isDefault, references int
	if err := tx.QueryRowContext(ctx, `SELECT is_default,
		EXISTS(SELECT 1 FROM assistant_conversations WHERE model_id = assistant_models.id)
		FROM assistant_models WHERE id = ? AND owner_user_id = ?`, id, actor.UserID).Scan(&isDefault, &references); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read LLM before deletion: %w", err)
	}
	if isDefault == 1 {
		return fmt.Errorf("%w: default LLM cannot be deleted", ErrInvalidInput)
	}
	if references == 1 {
		return fmt.Errorf("%w: LLM configuration is referenced by a conversation", ErrInvalidInput)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM assistant_models WHERE id = ? AND owner_user_id = ?", id, actor.UserID); err != nil {
		return fmt.Errorf("delete LLM configuration: %w", err)
	}
	if err := s.writeCredentials(credentials); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = s.writeCredentials(previous)
		return fmt.Errorf("commit LLM deletion: %w", err)
	}
	return nil
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	var enabled, defaultAutoApproval int
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT enabled, max_active_conversations, default_auto_approval,
		updated_at, updated_by_user_id FROM assistant_settings WHERE id = 1`).Scan(
		&enabled, &settings.MaxActiveConversations, &defaultAutoApproval, &updatedAt, &settings.UpdatedByUserID,
	)
	if err != nil {
		return Settings{}, fmt.Errorf("read assistant settings: %w", err)
	}
	settings.Enabled = enabled == 1
	settings.DefaultAutoApproval = defaultAutoApproval == 1
	if updatedAt > 0 {
		settings.UpdatedAt = time.Unix(0, updatedAt).UTC()
	}
	return settings, nil
}

func (s *Service) UpdateSettings(ctx context.Context, actor Actor, input SettingsInput) error {
	if strings.TrimSpace(actor.UserID) == "" || input.MaxActiveConversations < 1 || input.MaxActiveConversations > 8 {
		return fmt.Errorf("%w: invalid assistant settings", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE assistant_settings SET enabled = ?, max_active_conversations = ?,
		default_auto_approval = ?, updated_at = ?, updated_by_user_id = ? WHERE id = 1`,
		boolInt(input.Enabled), input.MaxActiveConversations, boolInt(input.DefaultAutoApproval), s.now().UTC().UnixNano(), actor.UserID)
	if err != nil {
		return fmt.Errorf("update assistant settings: %w", err)
	}
	return nil
}

func (s *Service) CreateConversation(ctx context.Context, actor Actor, input ConversationInput) (Conversation, error) {
	if strings.TrimSpace(actor.UserID) == "" {
		return Conversation{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	input.ModelID = strings.TrimSpace(input.ModelID)
	if input.ModelID == "" {
		return Conversation{}, ErrModelRequired
	}
	model, err := s.ModelForActor(ctx, actor, input.ModelID)
	if errors.Is(err, ErrNotFound) || err == nil && !model.CredentialConfigured {
		return Conversation{}, ErrModelUnavailable
	}
	if err != nil {
		return Conversation{}, err
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = "New conversation"
	}
	if !boundedText(input.Title, 1, 120) {
		return Conversation{}, fmt.Errorf("%w: invalid conversation title", ErrInvalidInput)
	}
	input.InitialMessage = strings.TrimSpace(input.InitialMessage)
	if input.InitialMessage != "" && !boundedText(input.InitialMessage, 1, maxMessageRunes) {
		return Conversation{}, fmt.Errorf("%w: invalid initial message", ErrInvalidInput)
	}
	input.CapabilityProfile = strings.TrimSpace(input.CapabilityProfile)
	if input.CapabilityProfile == "" {
		input.CapabilityProfile = ProfileGeneral
	}
	if !validCapabilityProfile(input.CapabilityProfile) {
		return Conversation{}, fmt.Errorf("%w: unknown capability profile", ErrInvalidInput)
	}
	contextReferences, err := normalizeContextReferences(input.Context)
	if err != nil {
		return Conversation{}, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return Conversation{}, err
	}
	if !settings.Enabled {
		return Conversation{}, ErrDisabled
	}
	autoApproval := settings.DefaultAutoApproval
	if input.AutoApproval != nil {
		autoApproval = *input.AutoApproval
	}
	id, err := randomID()
	if err != nil {
		return Conversation{}, fmt.Errorf("generate conversation ID: %w", err)
	}
	messageID := ""
	if input.InitialMessage != "" {
		messageID, err = randomID()
		if err != nil {
			return Conversation{}, fmt.Errorf("generate assistant message ID: %w", err)
		}
	}
	now := s.now().UTC().UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, fmt.Errorf("begin assistant conversation creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO assistant_conversations
		(id, owner_user_id, title, model_id, auto_approval, capability_profile, profile_version, status, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'idle', 1, ?, ?)`,
		id, actor.UserID, input.Title, input.ModelID, boolInt(autoApproval), input.CapabilityProfile, profileVersion(input.CapabilityProfile), now, now)
	if err != nil {
		return Conversation{}, fmt.Errorf("create assistant conversation: %w", err)
	}
	for position, reference := range contextReferences {
		if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_context_refs
			(conversation_id, kind, stable_id, label, position, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, reference.Kind, reference.StableID, reference.Label, position, now); err != nil {
			return Conversation{}, fmt.Errorf("create assistant context reference: %w", err)
		}
	}
	if messageID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_messages
			(id, conversation_id, sequence, role, body, status, created_at, finished_at)
			VALUES (?, ?, 1, 'user', ?, 'complete', ?, ?)`, messageID, id, input.InitialMessage, now, now); err != nil {
			return Conversation{}, fmt.Errorf("create initial assistant message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit assistant conversation creation: %w", err)
	}
	return s.Conversation(ctx, actor, id)
}

func normalizeContextReferences(input []ContextRef) ([]ContextRef, error) {
	if len(input) > 32 {
		return nil, fmt.Errorf("%w: too many context references", ErrInvalidInput)
	}
	allowedKinds := map[string]bool{
		"directory": true, "file": true, "application": true, "website": true,
		"run": true, "quick_run": true, "schedule": true,
	}
	result := make([]ContextRef, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, reference := range input {
		reference.Kind = strings.ToLower(strings.TrimSpace(reference.Kind))
		reference.StableID = strings.TrimSpace(reference.StableID)
		reference.Label = strings.TrimSpace(reference.Label)
		if !allowedKinds[reference.Kind] || !boundedText(reference.StableID, 1, 256) || !boundedText(reference.Label, 1, 160) {
			return nil, fmt.Errorf("%w: invalid context reference", ErrInvalidInput)
		}
		key := reference.Kind + "\x00" + reference.StableID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result, nil
}

func (s *Service) ContextReferences(ctx context.Context, actor Actor, conversationID string) ([]ContextRef, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, stable_id, label, created_at FROM assistant_context_refs
		WHERE conversation_id = ? ORDER BY position, kind, stable_id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list assistant context references: %w", err)
	}
	defer rows.Close()
	references := make([]ContextRef, 0)
	for rows.Next() {
		var reference ContextRef
		var createdAt int64
		if err := rows.Scan(&reference.Kind, &reference.StableID, &reference.Label, &createdAt); err != nil {
			return nil, fmt.Errorf("scan assistant context reference: %w", err)
		}
		reference.CreatedAt = time.Unix(0, createdAt).UTC()
		references = append(references, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assistant context references: %w", err)
	}
	return references, nil
}

func (s *Service) Messages(ctx context.Context, actor Actor, conversationID string) ([]Message, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, sequence, role, body, status, created_at, finished_at
		FROM assistant_messages WHERE conversation_id = ? ORDER BY sequence`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list assistant messages: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		var createdAt int64
		var finishedAt sql.NullInt64
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Sequence, &message.Role, &message.Body,
			&message.Status, &createdAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("scan assistant message: %w", err)
		}
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		if finishedAt.Valid {
			value := time.Unix(0, finishedAt.Int64).UTC()
			message.FinishedAt = &value
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assistant messages: %w", err)
	}
	return messages, nil
}

func (s *Service) BeginAssistantReply(ctx context.Context, actor Actor, conversationID string) (Message, error) {
	if err := s.ensureEnabled(ctx); err != nil {
		return Message{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin assistant reply: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	status, sequence, lastRole, err := conversationTurnState(ctx, tx, actor, conversationID)
	if err != nil {
		return Message{}, err
	}
	if status == "running" || status == "waiting_approval" {
		return Message{}, ErrConversationBusy
	}
	if sequence == 0 || lastRole != "user" {
		return Message{}, fmt.Errorf("%w: an unanswered user message is required", ErrInvalidInput)
	}
	message, err := s.insertAssistantMessage(ctx, tx, conversationID, sequence+1)
	if err != nil {
		return Message{}, err
	}
	if err := setConversationRunning(ctx, tx, actor, conversationID, s.now().UTC().UnixNano()); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit assistant reply: %w", err)
	}
	return message, nil
}

func (s *Service) BeginTurn(ctx context.Context, actor Actor, conversationID, body string) (Turn, error) {
	return s.beginTurn(ctx, actor, conversationID, body, nil)
}

// BeginTurnWithContext replaces the conversation's complete reference set in
// the same transaction that appends the user message and starts the reply.
// An empty slice intentionally removes all references; BeginTurn preserves the
// existing set for non-web callers that do not submit context.
func (s *Service) BeginTurnWithContext(ctx context.Context, actor Actor, conversationID, body string, references []ContextRef) (Turn, error) {
	normalized, err := normalizeContextReferences(references)
	if err != nil {
		return Turn{}, err
	}
	return s.beginTurn(ctx, actor, conversationID, body, &normalized)
}

func (s *Service) beginTurn(ctx context.Context, actor Actor, conversationID, body string, references *[]ContextRef) (Turn, error) {
	if err := s.ensureEnabled(ctx); err != nil {
		return Turn{}, err
	}
	body = strings.TrimSpace(body)
	if !boundedText(body, 1, maxMessageRunes) {
		return Turn{}, fmt.Errorf("%w: invalid assistant message", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Turn{}, fmt.Errorf("begin agent turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	status, sequence, _, err := conversationTurnState(ctx, tx, actor, conversationID)
	if err != nil {
		return Turn{}, err
	}
	if status == "running" || status == "waiting_approval" {
		return Turn{}, ErrConversationBusy
	}
	now := s.now().UTC()
	if references != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM assistant_context_refs WHERE conversation_id = ?", conversationID); err != nil {
			return Turn{}, fmt.Errorf("replace assistant context references: %w", err)
		}
		for position, reference := range *references {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_context_refs
				(conversation_id, kind, stable_id, label, position, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				conversationID, reference.Kind, reference.StableID, reference.Label, position, now.UnixNano()); err != nil {
				return Turn{}, fmt.Errorf("replace assistant context reference: %w", err)
			}
		}
	}
	userID, err := randomID()
	if err != nil {
		return Turn{}, fmt.Errorf("generate user message ID: %w", err)
	}
	user := Message{
		ID: userID, ConversationID: conversationID, Sequence: sequence + 1,
		Role: "user", Body: body, Status: "complete", CreatedAt: now,
	}
	finished := now
	user.FinishedAt = &finished
	if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_messages
		(id, conversation_id, sequence, role, body, status, created_at, finished_at)
		VALUES (?, ?, ?, 'user', ?, 'complete', ?, ?)`, user.ID, conversationID, user.Sequence, user.Body, now.UnixNano(), now.UnixNano()); err != nil {
		return Turn{}, fmt.Errorf("append user message: %w", err)
	}
	assistantMessage, err := s.insertAssistantMessage(ctx, tx, conversationID, sequence+2)
	if err != nil {
		return Turn{}, err
	}
	if err := setConversationRunning(ctx, tx, actor, conversationID, now.UnixNano()); err != nil {
		return Turn{}, err
	}
	if err := tx.Commit(); err != nil {
		return Turn{}, fmt.Errorf("commit agent turn: %w", err)
	}
	return Turn{User: user, Assistant: assistantMessage}, nil
}

func (s *Service) AppendAssistantText(ctx context.Context, actor Actor, conversationID, messageID, delta string) error {
	if delta == "" {
		return nil
	}
	if !utf8.ValidString(delta) || strings.ContainsRune(delta, 0) {
		return fmt.Errorf("%w: invalid assistant response text", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin assistant response append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var body string
	err = tx.QueryRowContext(ctx, `SELECT m.body FROM assistant_messages m
		JOIN assistant_conversations c ON c.id = m.conversation_id
		WHERE m.id = ? AND m.conversation_id = ? AND m.role = 'assistant' AND m.status = 'streaming' AND c.owner_user_id = ?`,
		messageID, conversationID, actor.UserID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read streaming assistant message: %w", err)
	}
	if utf8.RuneCountInString(body)+utf8.RuneCountInString(delta) > maxMessageRunes {
		return fmt.Errorf("%w: assistant response is too large", ErrInvalidInput)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE assistant_messages SET body = body || ? WHERE id = ?", delta, messageID); err != nil {
		return fmt.Errorf("append assistant response: %w", err)
	}
	return tx.Commit()
}

func (s *Service) FinishTurn(ctx context.Context, actor Actor, conversationID, messageID, messageStatus, runtimeVersion string) error {
	conversationStatus := ""
	switch messageStatus {
	case "complete":
		conversationStatus = "idle"
	case "interrupted":
		conversationStatus = "interrupted"
	case "error":
		conversationStatus = "failed"
	default:
		return fmt.Errorf("%w: invalid turn result", ErrInvalidInput)
	}
	if !boundedText(strings.TrimSpace(runtimeVersion), 1, 64) {
		return fmt.Errorf("%w: invalid runtime version", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent turn completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC().UnixNano()
	result, err := tx.ExecContext(ctx, `UPDATE assistant_messages SET status = ?, finished_at = ?
		WHERE id = ? AND conversation_id = ? AND role = 'assistant' AND status = 'streaming'
		AND EXISTS (SELECT 1 FROM assistant_conversations WHERE id = ? AND owner_user_id = ?)`,
		messageStatus, now, messageID, conversationID, conversationID, actor.UserID)
	if err != nil {
		return fmt.Errorf("finish assistant message: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	result, err = tx.ExecContext(ctx, `UPDATE assistant_conversations SET status = ?, runtime_version = ?,
		revision = revision + 1, updated_at = ? WHERE id = ? AND owner_user_id = ? AND status = 'running'`,
		conversationStatus, strings.TrimSpace(runtimeVersion), now, conversationID, actor.UserID)
	if err != nil {
		return fmt.Errorf("settle assistant conversation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConversationBusy
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent turn completion: %w", err)
	}
	return nil
}

func (s *Service) ensureEnabled(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return ErrDisabled
	}
	return nil
}

func conversationTurnState(ctx context.Context, tx *sql.Tx, actor Actor, conversationID string) (string, uint64, string, error) {
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(conversationID) == "" {
		return "", 0, "", ErrNotFound
	}
	var status string
	var sequence uint64
	var lastRole string
	err := tx.QueryRowContext(ctx, `SELECT c.status,
		COALESCE((SELECT MAX(sequence) FROM assistant_messages WHERE conversation_id = c.id), 0),
		COALESCE((SELECT role FROM assistant_messages WHERE conversation_id = c.id ORDER BY sequence DESC LIMIT 1), '')
		FROM assistant_conversations c WHERE c.id = ? AND c.owner_user_id = ? AND c.archived_at IS NULL`,
		conversationID, actor.UserID).Scan(&status, &sequence, &lastRole)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", ErrNotFound
	}
	if err != nil {
		return "", 0, "", fmt.Errorf("read assistant turn state: %w", err)
	}
	return status, sequence, lastRole, nil
}

func (s *Service) insertAssistantMessage(ctx context.Context, tx *sql.Tx, conversationID string, sequence uint64) (Message, error) {
	id, err := randomID()
	if err != nil {
		return Message{}, fmt.Errorf("generate assistant message ID: %w", err)
	}
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_messages
		(id, conversation_id, sequence, role, body, status, created_at)
		VALUES (?, ?, ?, 'assistant', '', 'streaming', ?)`, id, conversationID, sequence, now.UnixNano()); err != nil {
		return Message{}, fmt.Errorf("begin assistant message: %w", err)
	}
	return Message{ID: id, ConversationID: conversationID, Sequence: sequence, Role: "assistant", Status: "streaming", CreatedAt: now}, nil
}

func setConversationRunning(ctx context.Context, tx *sql.Tx, actor Actor, conversationID string, now int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND archived_at IS NULL AND status NOT IN ('running', 'waiting_approval')`,
		now, conversationID, actor.UserID)
	if err != nil {
		return fmt.Errorf("start assistant conversation turn: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConversationBusy
	}
	return nil
}

func (s *Service) ensureConversationOwner(ctx context.Context, actor Actor, conversationID string) error {
	if strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(conversationID) == "" {
		return ErrNotFound
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM assistant_conversations WHERE id = ? AND owner_user_id = ?`,
		conversationID, actor.UserID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("verify assistant conversation owner: %w", err)
	}
	return nil
}

func (s *Service) Conversation(ctx context.Context, actor Actor, id string) (Conversation, error) {
	var conversation Conversation
	var autoApproval int
	var createdAt, updatedAt, telemetryUpdatedAt int64
	var archivedAt sql.NullInt64
	var contextPercent sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.owner_user_id, c.title, c.model_id, m.name, m.provider, m.model,
		c.auto_approval, c.capability_profile, c.profile_version, c.thinking_level,
		c.stats_user_messages, c.stats_assistant_messages, c.stats_tool_calls, c.stats_tool_results, c.stats_total_messages,
		c.stats_input_tokens, c.stats_output_tokens, c.stats_cache_read_tokens, c.stats_cache_write_tokens, c.stats_total_tokens,
		c.stats_cost, c.stats_context_tokens, c.stats_context_window, c.stats_context_percent, c.stats_updated_at,
		c.status, c.revision, c.created_at, c.updated_at, c.archived_at
		FROM assistant_conversations c JOIN assistant_models m ON m.id = c.model_id
		WHERE c.id = ? AND c.owner_user_id = ?`, id, actor.UserID).Scan(
		&conversation.ID, &conversation.OwnerUserID, &conversation.Title, &conversation.ModelID,
		&conversation.ModelName, &conversation.Provider, &conversation.Model, &autoApproval, &conversation.CapabilityProfile, &conversation.ProfileVersion, &conversation.ThinkingLevel,
		&conversation.Telemetry.UserMessages, &conversation.Telemetry.AssistantMessages, &conversation.Telemetry.ToolCalls, &conversation.Telemetry.ToolResults, &conversation.Telemetry.TotalMessages,
		&conversation.Telemetry.InputTokens, &conversation.Telemetry.OutputTokens, &conversation.Telemetry.CacheReadTokens, &conversation.Telemetry.CacheWriteTokens, &conversation.Telemetry.TotalTokens,
		&conversation.Telemetry.Cost, &conversation.Telemetry.ContextTokens, &conversation.Telemetry.ContextWindow, &contextPercent, &telemetryUpdatedAt,
		&conversation.Status, &conversation.Revision, &createdAt, &updatedAt, &archivedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("read assistant conversation: %w", err)
	}
	conversation.AutoApproval = autoApproval == 1
	conversation.CreatedAt = time.Unix(0, createdAt).UTC()
	conversation.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if contextPercent.Valid {
		conversation.Telemetry.ContextPercent = &contextPercent.Float64
	}
	if telemetryUpdatedAt > 0 {
		conversation.Telemetry.UpdatedAt = time.Unix(0, telemetryUpdatedAt).UTC()
	}
	if archivedAt.Valid {
		value := time.Unix(0, archivedAt.Int64).UTC()
		conversation.ArchivedAt = &value
	}
	return conversation, nil
}

func (s *Service) ListConversations(ctx context.Context, actor Actor, filter ConversationFilter) ([]Conversation, error) {
	archivedPredicate := "c.archived_at IS NULL"
	if filter.Archived {
		archivedPredicate = "c.archived_at IS NOT NULL"
	}
	arguments := []any{actor.UserID}
	queryPredicate := ""
	if value := strings.TrimSpace(filter.Query); value != "" {
		queryPredicate = " AND c.title LIKE ? ESCAPE '\\'"
		arguments = append(arguments, "%"+escapeLike(value)+"%")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.owner_user_id, c.title, c.model_id, m.name, m.provider, m.model,
		c.auto_approval, c.capability_profile, c.profile_version, c.thinking_level,
		c.stats_user_messages, c.stats_assistant_messages, c.stats_tool_calls, c.stats_tool_results, c.stats_total_messages,
		c.stats_input_tokens, c.stats_output_tokens, c.stats_cache_read_tokens, c.stats_cache_write_tokens, c.stats_total_tokens,
		c.stats_cost, c.stats_context_tokens, c.stats_context_window, c.stats_context_percent, c.stats_updated_at,
		c.status, c.revision, c.created_at, c.updated_at, c.archived_at
		FROM assistant_conversations c JOIN assistant_models m ON m.id = c.model_id
		WHERE c.owner_user_id = ? AND `+archivedPredicate+queryPredicate+` ORDER BY c.updated_at DESC LIMIT 100`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list assistant conversations: %w", err)
	}
	defer rows.Close()
	conversations := make([]Conversation, 0)
	for rows.Next() {
		var conversation Conversation
		var autoApproval int
		var createdAt, updatedAt, telemetryUpdatedAt int64
		var archivedAt sql.NullInt64
		var contextPercent sql.NullFloat64
		if err := rows.Scan(&conversation.ID, &conversation.OwnerUserID, &conversation.Title, &conversation.ModelID,
			&conversation.ModelName, &conversation.Provider, &conversation.Model, &autoApproval, &conversation.CapabilityProfile, &conversation.ProfileVersion, &conversation.ThinkingLevel,
			&conversation.Telemetry.UserMessages, &conversation.Telemetry.AssistantMessages, &conversation.Telemetry.ToolCalls, &conversation.Telemetry.ToolResults, &conversation.Telemetry.TotalMessages,
			&conversation.Telemetry.InputTokens, &conversation.Telemetry.OutputTokens, &conversation.Telemetry.CacheReadTokens, &conversation.Telemetry.CacheWriteTokens, &conversation.Telemetry.TotalTokens,
			&conversation.Telemetry.Cost, &conversation.Telemetry.ContextTokens, &conversation.Telemetry.ContextWindow, &contextPercent, &telemetryUpdatedAt, &conversation.Status,
			&conversation.Revision, &createdAt, &updatedAt, &archivedAt); err != nil {
			return nil, fmt.Errorf("scan assistant conversation: %w", err)
		}
		conversation.AutoApproval = autoApproval == 1
		conversation.CreatedAt = time.Unix(0, createdAt).UTC()
		conversation.UpdatedAt = time.Unix(0, updatedAt).UTC()
		if contextPercent.Valid {
			conversation.Telemetry.ContextPercent = &contextPercent.Float64
		}
		if telemetryUpdatedAt > 0 {
			conversation.Telemetry.UpdatedAt = time.Unix(0, telemetryUpdatedAt).UTC()
		}
		if archivedAt.Valid {
			value := time.Unix(0, archivedAt.Int64).UTC()
			conversation.ArchivedAt = &value
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func (s *Service) SetConversationModel(ctx context.Context, actor Actor, id, modelID string) error {
	model, err := s.ModelForActor(ctx, actor, strings.TrimSpace(modelID))
	if errors.Is(err, ErrNotFound) || err == nil && !model.CredentialConfigured {
		return ErrModelUnavailable
	}
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET model_id = ?, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND status NOT IN ('running', 'waiting_approval')`,
		model.ID, s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("update conversation model: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var status string
		if readErr := s.db.QueryRowContext(ctx, "SELECT status FROM assistant_conversations WHERE id = ? AND owner_user_id = ?", id, actor.UserID).Scan(&status); errors.Is(readErr, sql.ErrNoRows) {
			return ErrNotFound
		} else if readErr != nil {
			return fmt.Errorf("read conversation model state: %w", readErr)
		}
		return ErrConversationBusy
	}
	return nil
}

func (s *Service) SetAutoApproval(ctx context.Context, actor Actor, id string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET auto_approval = ?, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ?`, boolInt(enabled), s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("update conversation approval mode: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetCapabilityProfile(ctx context.Context, actor Actor, id, profile string) error {
	profile = strings.TrimSpace(profile)
	if !validCapabilityProfile(profile) {
		return fmt.Errorf("%w: unknown capability profile", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET capability_profile = ?, profile_version = ?, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND status NOT IN ('running', 'waiting_approval')`,
		profile, profileVersion(profile), s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("update conversation capability profile: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return s.conversationMutationError(ctx, actor, id)
	}
	return nil
}

func (s *Service) SetThinkingLevel(ctx context.Context, actor Actor, id, level string) error {
	level = strings.TrimSpace(level)
	if !validThinkingLevel(level) {
		return fmt.Errorf("%w: unknown thinking level", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET thinking_level = ?, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND status NOT IN ('running', 'waiting_approval')`,
		level, s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("update conversation thinking level: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return s.conversationMutationError(ctx, actor, id)
	}
	return nil
}

func (s *Service) UpdateSessionTelemetry(ctx context.Context, actor Actor, id string, telemetry SessionTelemetry) error {
	if !validSessionTelemetry(telemetry) {
		return fmt.Errorf("%w: invalid session telemetry", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET
		stats_user_messages = ?, stats_assistant_messages = ?, stats_tool_calls = ?, stats_tool_results = ?, stats_total_messages = ?,
		stats_input_tokens = ?, stats_output_tokens = ?, stats_cache_read_tokens = ?, stats_cache_write_tokens = ?, stats_total_tokens = ?,
		stats_cost = ?, stats_context_tokens = ?, stats_context_window = ?, stats_context_percent = ?, stats_updated_at = ?
		WHERE id = ? AND owner_user_id = ?`,
		telemetry.UserMessages, telemetry.AssistantMessages, telemetry.ToolCalls, telemetry.ToolResults, telemetry.TotalMessages,
		telemetry.InputTokens, telemetry.OutputTokens, telemetry.CacheReadTokens, telemetry.CacheWriteTokens, telemetry.TotalTokens,
		telemetry.Cost, telemetry.ContextTokens, telemetry.ContextWindow, telemetry.ContextPercent, s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("update conversation session telemetry: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) conversationMutationError(ctx context.Context, actor Actor, id string) error {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM assistant_conversations WHERE id = ? AND owner_user_id = ?`, id, actor.UserID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read conversation mutation state: %w", err)
	}
	return ErrConversationBusy
}

func validCapabilityProfile(profile string) bool {
	switch profile {
	case ProfileGeneral, ProfileDiagnoseFailedRun, ProfileInvestigateWebsiteIncident, ProfileTriageHostPressure, ProfileReviewScriptSafety, ProfileDesignSchedule:
		return true
	default:
		return false
	}
}

func profileVersion(profile string) string {
	if profile == ProfileGeneral {
		return ""
	}
	return CapabilityBundleVersion
}

func validThinkingLevel(level string) bool {
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func validSessionTelemetry(telemetry SessionTelemetry) bool {
	values := []int64{
		telemetry.UserMessages, telemetry.AssistantMessages, telemetry.ToolCalls, telemetry.ToolResults, telemetry.TotalMessages,
		telemetry.InputTokens, telemetry.OutputTokens, telemetry.CacheReadTokens, telemetry.CacheWriteTokens, telemetry.TotalTokens,
		telemetry.ContextTokens, telemetry.ContextWindow,
	}
	for _, value := range values {
		if value < 0 {
			return false
		}
	}
	if telemetry.Cost < 0 || math.IsNaN(telemetry.Cost) || math.IsInf(telemetry.Cost, 0) {
		return false
	}
	return telemetry.ContextPercent == nil || !math.IsNaN(*telemetry.ContextPercent) && !math.IsInf(*telemetry.ContextPercent, 0) && *telemetry.ContextPercent >= 0 && *telemetry.ContextPercent <= 100
}

func (s *Service) ArchiveConversation(ctx context.Context, actor Actor, id string) error {
	now := s.now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET archived_at = ?, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND archived_at IS NULL`, now, now, id, actor.UserID)
	if err != nil {
		return fmt.Errorf("archive assistant conversation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) RestoreConversation(ctx context.Context, actor Actor, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_conversations SET archived_at = NULL, revision = revision + 1,
		updated_at = ? WHERE id = ? AND owner_user_id = ? AND archived_at IS NOT NULL`, s.now().UTC().UnixNano(), id, actor.UserID)
	if err != nil {
		return fmt.Errorf("restore assistant conversation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

// RecoverInterruptedTurns is called once during application startup. A process
// that disappeared with the service is never replayed implicitly; its durable
// streaming records become an explicit interrupted state that the owner may
// retry with a new message.
func (s *Service) RecoverInterruptedTurns(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin assistant turn recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC().UnixNano()
	nowSeconds := s.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'cancelled', decided_at = ?
		WHERE status IN ('pending', 'approved')`, nowSeconds); err != nil {
		return 0, fmt.Errorf("cancel unfinished assistant approvals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'interrupted',
		error_code = 'service_restarted', finished_at = ? WHERE status IN ('running', 'waiting_approval')`, nowSeconds); err != nil {
		return 0, fmt.Errorf("interrupt unfinished assistant tool calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE assistant_messages SET status = 'interrupted', finished_at = ?
		WHERE status = 'streaming' AND conversation_id IN
		(SELECT id FROM assistant_conversations WHERE status IN ('running', 'waiting_approval'))`, now); err != nil {
		return 0, fmt.Errorf("interrupt unfinished assistant messages: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'interrupted',
		revision = revision + 1, updated_at = ? WHERE status IN ('running', 'waiting_approval')`, now)
	if err != nil {
		return 0, fmt.Errorf("recover unfinished assistant conversations: %w", err)
	}
	recovered, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit assistant turn recovery: %w", err)
	}
	return recovered, nil
}

func (s *Service) PendingApprovalCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_approvals WHERE status = 'pending' AND expires_at > ?`, s.now().UTC().Unix()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending assistant approvals: %w", err)
	}
	return count, nil
}

func (s *Service) ReferencedRuntimeVersions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT runtime_version FROM assistant_conversations WHERE archived_at IS NULL AND runtime_version <> '' ORDER BY runtime_version`)
	if err != nil {
		return nil, fmt.Errorf("list referenced assistant runtimes: %w", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Service) loadCredentials() (map[string]string, error) {
	body, err := os.ReadFile(s.credentialPath)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read assistant provider credentials: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("assistant provider credential file is too large")
	}
	credentials := make(map[string]string)
	if err := json.Unmarshal(body, &credentials); err != nil {
		return nil, fmt.Errorf("decode assistant provider credentials: %w", err)
	}
	return credentials, nil
}

func (s *Service) writeCredentials(credentials map[string]string) error {
	directory := filepath.Dir(s.credentialPath)
	temporary, err := os.CreateTemp(directory, ".assistant-provider-*.tmp")
	if err != nil {
		return fmt.Errorf("create assistant credential staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect assistant credential staging file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(credentials); err != nil {
		return fmt.Errorf("encode assistant provider credentials: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync assistant provider credentials: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close assistant provider credentials: %w", err)
	}
	if err := os.Rename(temporaryPath, s.credentialPath); err != nil {
		return fmt.Errorf("commit assistant provider credentials: %w", err)
	}
	committed = true
	return nil
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func cloneCredentials(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}
