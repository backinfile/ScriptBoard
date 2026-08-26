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
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"scriptboard/internal/secretstore"
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
	ErrInvalidKey         = errors.New("external trigger key is invalid")
	ErrEntryNotFound      = errors.New("external trigger entry does not exist")
	ErrEntryDisabled      = errors.New("external trigger entry is disabled")
	ErrKeyScopeBound      = errors.New("external trigger key is already bound to an entry")
	ErrEntryImmutable     = errors.New("external trigger entry scope is immutable")
	ErrInvalidInput       = errors.New("external trigger input is invalid")
	ErrKeyLabelExists     = errors.New("external trigger key name already exists")
	ErrGroupNameExists    = errors.New("external trigger group call name already exists")
	ErrApprovalNotPending = errors.New("external trigger approval is not pending")
	entryNamePattern      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

func normalizedCallName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastSeparator := false
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' {
			builder.WriteRune(character)
			lastSeparator = false
			continue
		}
		if builder.Len() > 0 && !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" || result[0] < 'a' || result[0] > 'z' {
		result = "group-" + result
	}
	if len(result) > 64 {
		result = strings.TrimRight(result[:64], "-")
	}
	return result
}

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS external_trigger_control (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		updated_at INTEGER NOT NULL DEFAULT 0
	)`,
	`INSERT OR IGNORE INTO external_trigger_control (id, enabled, updated_at) VALUES (1, 1, 0)`,
	`CREATE TABLE IF NOT EXISTS external_trigger_groups (
		id TEXT PRIMARY KEY,
		label TEXT NOT NULL COLLATE NOCASE UNIQUE,
		call_name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS external_trigger_keys (
		id TEXT PRIMARY KEY,
		group_id TEXT NOT NULL DEFAULT '',
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
		group_id TEXT NOT NULL DEFAULT '',
		key_id TEXT REFERENCES external_trigger_keys(id) ON DELETE SET NULL,
		name TEXT NOT NULL,
		label TEXT NOT NULL,
		action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable')),
		target TEXT NOT NULL DEFAULT '',
		config_json TEXT NOT NULL DEFAULT '{}',
		require_signature INTEGER NOT NULL DEFAULT 0 CHECK (require_signature IN (0, 1)),
		require_approval INTEGER NOT NULL DEFAULT 0 CHECK (require_approval IN (0, 1)),
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (group_id, name)
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
	`CREATE TABLE IF NOT EXISTS external_trigger_approvals (
		id TEXT PRIMARY KEY,
		occurred_at INTEGER NOT NULL,
		key_id TEXT NOT NULL,
		key_label TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		entry_name TEXT NOT NULL,
		action_type TEXT NOT NULL CHECK (action_type IN ('log', 'upload', 'quick_run', 'variable')),
		entry_updated_at INTEGER NOT NULL,
		payload_json TEXT NOT NULL DEFAULT '{}',
		upload_name TEXT NOT NULL DEFAULT '',
		upload_sha256 TEXT NOT NULL DEFAULT '',
		bytes_received INTEGER NOT NULL DEFAULT 0,
		source_address TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'approved', 'rejected', 'failed')),
		decided_at INTEGER,
		decided_by TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS external_trigger_nonces (
		key_id TEXT NOT NULL REFERENCES external_trigger_keys(id) ON DELETE CASCADE,
		nonce TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		PRIMARY KEY (key_id, nonce)
	)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_requests_time_idx ON external_trigger_requests(occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_requests_key_time_idx ON external_trigger_requests(key_id, occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_approvals_status_time_idx ON external_trigger_approvals(status, occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS external_trigger_nonces_expiry_idx ON external_trigger_nonces(expires_at)`,
}

type ActionType string
type VariableType string

type Group struct {
	ID, Label, CallName  string
	Enabled              bool
	CreatedAt, UpdatedAt time.Time
	Keys                 []Key
	Entries              []Entry
}

type Key struct {
	ID, GroupID, Label, TokenHint string
	Enabled                       bool
	ExpiresAt                     *time.Time
	CreatedAt, UpdatedAt          time.Time
	LastUsedAt                    *time.Time
	Entries                       []Entry
}

func (key Key) Active(now time.Time) bool {
	return key.Enabled && (key.ExpiresAt == nil || now.Before(*key.ExpiresAt))
}

func (key Key) Expired(now time.Time) bool {
	return key.ExpiresAt != nil && !now.Before(*key.ExpiresAt)
}

type Entry struct {
	ID, GroupID, KeyID, Name, Label, Target string
	Type                                    ActionType
	ConfigJSON                              string
	Enabled                                 bool
	RequireSignature                        bool
	RequireApproval                         bool
	CreatedAt, UpdatedAt                    time.Time
}

func (entry Entry) DecodeConfig(destination any) error {
	if err := json.Unmarshal([]byte(entry.ConfigJSON), destination); err != nil {
		return fmt.Errorf("decode external trigger config: %w", err)
	}
	return nil
}

type LogConfig struct {
	File            string `json:"file"`
	Managed         bool   `json:"managed,omitempty"`
	Category        string `json:"category"`
	MaxMessageBytes int    `json:"max_message_bytes"`
	Rotate          bool   `json:"rotate,omitempty"`
	MaxFileBytes    int64  `json:"max_file_bytes,omitempty"`
	MaxBackups      int    `json:"max_backups,omitempty"`
}

type UploadConfig struct {
	Directory      string   `json:"directory"`
	MaxBytes       int64    `json:"max_bytes"`
	Extensions     []string `json:"extensions,omitempty"`
	ConflictPolicy string   `json:"conflict_policy"`
}

type QuickRunConfig struct {
	QuickRunID   string `json:"quick_run_id"`
	Revision     int64  `json:"revision"`
	ScriptSHA256 string `json:"script_sha256"`
}

// RebindQuickRunEntries updates every external entry bound to a Quick Run inside
// the caller's publication transaction, so the new version is never visible
// without its explicitly synchronized bindings.
func RebindQuickRunEntries(ctx context.Context, transaction *sql.Tx, quickRunID string, revision int64, scriptSHA256 string, updatedAt time.Time) (int64, error) {
	rows, err := transaction.QueryContext(ctx, `SELECT id, config_json FROM external_trigger_entries WHERE action_type = 'quick_run'`)
	if err != nil {
		return 0, fmt.Errorf("list Quick Run external entries: %w", err)
	}
	type binding struct{ id, configJSON string }
	var bindings []binding
	for rows.Next() {
		var candidate binding
		if err := rows.Scan(&candidate.id, &candidate.configJSON); err != nil {
			_ = rows.Close()
			return 0, err
		}
		bindings = append(bindings, candidate)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var rebound int64
	for _, binding := range bindings {
		var config QuickRunConfig
		if err := json.Unmarshal([]byte(binding.configJSON), &config); err != nil || config.QuickRunID != quickRunID {
			continue
		}
		config.Revision, config.ScriptSHA256 = revision, scriptSHA256
		encoded, err := json.Marshal(config)
		if err != nil {
			return 0, err
		}
		result, err := transaction.ExecContext(ctx, `UPDATE external_trigger_entries SET config_json = ?, updated_at = ? WHERE id = ?`, string(encoded), updatedAt.UTC().Unix(), binding.id)
		if err != nil {
			return 0, fmt.Errorf("rebind Quick Run external entry: %w", err)
		}
		changed, _ := result.RowsAffected()
		rebound += changed
	}
	return rebound, nil
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
	GroupID   string
	Label     string
	Enabled   bool
	ExpiresAt *time.Time
}

type CreateEntryInput struct {
	GroupID, KeyID, Name, Label string
	Type                        ActionType
	Enabled                     bool
	RequireSignature            bool
	RequireApproval             bool
	Config                      any
}

type UpdateEntryInput struct {
	ID, Name, Label  string
	Type             ActionType
	Enabled          bool
	RequireSignature bool
	RequireApproval  bool
	Config           any
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

type ApprovalStatus string

const (
	ApprovalPending    ApprovalStatus = "pending"
	ApprovalProcessing ApprovalStatus = "processing"
	ApprovalApproved   ApprovalStatus = "approved"
	ApprovalRejected   ApprovalStatus = "rejected"
	ApprovalFailed     ApprovalStatus = "failed"
)

type Approval struct {
	ID, KeyID, KeyLabel, EntryID, EntryName string
	ActionType                              ActionType
	EntryUpdatedAt, OccurredAt              time.Time
	PayloadJSON, UploadName, UploadSHA256   string
	BytesReceived                           int64
	Source                                  string
	Status                                  ApprovalStatus
	DecidedAt                               *time.Time
	DecidedBy, Result, RunID, Message       string
}

func (manager *Manager) CreateApproval(ctx context.Context, approval Approval) error {
	if approval.ID == "" || approval.KeyID == "" || approval.EntryID == "" || approval.EntryUpdatedAt.IsZero() || len([]byte(approval.PayloadJSON)) > 16<<10 {
		return fmt.Errorf("%w: approval", ErrInvalidInput)
	}
	if approval.OccurredAt.IsZero() {
		approval.OccurredAt = manager.now().UTC()
	}
	if approval.PayloadJSON == "" {
		approval.PayloadJSON = "{}"
	}
	_, err := manager.db.ExecContext(ctx, `INSERT INTO external_trigger_approvals
		(id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, entry_updated_at, payload_json,
		 upload_name, upload_sha256, bytes_received, source_address, status, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`, approval.ID, approval.OccurredAt.UTC().Unix(), approval.KeyID,
		approval.KeyLabel, approval.EntryID, approval.EntryName, approval.ActionType, approval.EntryUpdatedAt.UTC().Unix(), approval.PayloadJSON,
		approval.UploadName, approval.UploadSHA256, approval.BytesReceived, approval.Source, approval.Message)
	return err
}

func (manager *Manager) DeleteApproval(ctx context.Context, id string) error {
	_, err := manager.db.ExecContext(ctx, `DELETE FROM external_trigger_approvals WHERE id = ? AND status = 'pending'`, id)
	return err
}

func scanApproval(scanner interface{ Scan(...any) error }) (Approval, error) {
	var approval Approval
	var occurredAt, entryUpdatedAt int64
	var decidedAt sql.NullInt64
	err := scanner.Scan(&approval.ID, &occurredAt, &approval.KeyID, &approval.KeyLabel, &approval.EntryID, &approval.EntryName,
		&approval.ActionType, &entryUpdatedAt, &approval.PayloadJSON, &approval.UploadName, &approval.UploadSHA256, &approval.BytesReceived,
		&approval.Source, &approval.Status, &decidedAt, &approval.DecidedBy, &approval.Result, &approval.RunID, &approval.Message)
	if err != nil {
		return Approval{}, err
	}
	approval.OccurredAt = time.Unix(occurredAt, 0).UTC()
	approval.EntryUpdatedAt = time.Unix(entryUpdatedAt, 0).UTC()
	if decidedAt.Valid {
		value := time.Unix(decidedAt.Int64, 0).UTC()
		approval.DecidedAt = &value
	}
	return approval, nil
}

const approvalSelect = `SELECT id, occurred_at, key_id, key_label, entry_id, entry_name, action_type, entry_updated_at,
	payload_json, upload_name, upload_sha256, bytes_received, source_address, status, decided_at, decided_by, result, run_id, message
	FROM external_trigger_approvals`

func (manager *Manager) Approval(ctx context.Context, id string) (Approval, error) {
	return scanApproval(manager.db.QueryRowContext(ctx, approvalSelect+` WHERE id = ?`, id))
}

func (manager *Manager) ListApprovals(ctx context.Context, status ApprovalStatus, limit int) ([]Approval, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := manager.db.QueryContext(ctx, approvalSelect+` WHERE status = ? ORDER BY occurred_at DESC, id DESC LIMIT ?`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var approvals []Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}

func (manager *Manager) ClaimApproval(ctx context.Context, id, decidedBy string) (Approval, error) {
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return Approval{}, err
	}
	defer transaction.Rollback()
	now := manager.now().UTC()
	result, err := transaction.ExecContext(ctx, `UPDATE external_trigger_approvals SET status = 'processing', decided_at = ?, decided_by = ? WHERE id = ? AND status = 'pending'`, now.Unix(), decidedBy, id)
	if err != nil {
		return Approval{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Approval{}, ErrApprovalNotPending
	}
	approval, err := scanApproval(transaction.QueryRowContext(ctx, approvalSelect+` WHERE id = ?`, id))
	if err != nil {
		return Approval{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Approval{}, err
	}
	return approval, nil
}

func (manager *Manager) CompleteApproval(ctx context.Context, id string, status ApprovalStatus, resultText, runID, message string) error {
	if status != ApprovalApproved && status != ApprovalRejected && status != ApprovalFailed {
		return fmt.Errorf("%w: approval completion", ErrInvalidInput)
	}
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_approvals SET status = ?, result = ?, run_id = ?, message = ? WHERE id = ? AND status = 'processing'`, status, resultText, runID, message, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrApprovalNotPending
	}
	return nil
}

func (manager *Manager) FinalizeApprovalInvocation(ctx context.Context, id string, status ApprovalStatus, resultText string, httpStatus int, bytesReceived int64, runID, message string) error {
	if status != ApprovalApproved && status != ApprovalRejected && status != ApprovalFailed || httpStatus < 100 || httpStatus > 599 {
		return fmt.Errorf("%w: approval finalization", ErrInvalidInput)
	}
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE external_trigger_approvals SET status = ?, result = ?, run_id = ?, message = ? WHERE id = ? AND status = 'processing'`, status, resultText, runID, message, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrApprovalNotPending
	}
	result, err = transaction.ExecContext(ctx, `UPDATE external_trigger_requests SET result = ?, http_status = ?, bytes_received = ?, run_id = ?, message = ? WHERE id = ? AND result = 'pending_approval'`, resultText, httpStatus, bytesReceived, runID, message, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrApprovalNotPending
	}
	return transaction.Commit()
}

func (manager *Manager) RecoverApprovals(ctx context.Context) error {
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	message := "service restarted while approval was executing"
	if _, err := transaction.ExecContext(ctx, `UPDATE external_trigger_requests
		SET result = 'unknown', http_status = 500, message = ?
		WHERE result = 'pending_approval' AND id IN (SELECT id FROM external_trigger_approvals WHERE status = 'processing')`, message); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE external_trigger_approvals
		SET status = 'failed', result = 'unknown', message = 'service restarted while approval was executing'
		WHERE status = 'processing'`); err != nil {
		return err
	}
	return transaction.Commit()
}

func (manager *Manager) PendingApprovalIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id FROM external_trigger_approvals WHERE status = 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
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
	SecretStore      *secretstore.Store
}

type Manager struct {
	db                      *sql.DB
	now                     func() time.Time
	random                  func([]byte) (int, error)
	secretStore             *encryptedSecretStore
	reconciliationDirectory string
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
	return &Manager{
		db: db, now: now, random: random,
		secretStore:             &encryptedSecretStore{directory: options.SecretsDirectory, vault: options.SecretStore},
		reconciliationDirectory: filepath.Join(filepath.Dir(options.SecretsDirectory), "operations", "external-trigger-completions"),
	}
}

// GlobalEnabled returns the persistent emergency control for every External
// Interface. A missing or unreadable control row is an error so callers can
// fail closed rather than accidentally accepting external work.
func (manager *Manager) GlobalEnabled(ctx context.Context) (bool, time.Time, error) {
	var enabled int
	var updatedAtUnix int64
	if err := manager.db.QueryRowContext(ctx, `SELECT enabled, updated_at FROM external_trigger_control WHERE id = 1`).Scan(&enabled, &updatedAtUnix); err != nil {
		return false, time.Time{}, err
	}
	updatedAt := time.Time{}
	if updatedAtUnix != 0 {
		updatedAt = time.Unix(updatedAtUnix, 0).UTC()
	}
	return enabled == 1, updatedAt, nil
}

func (manager *Manager) SetGlobalEnabled(ctx context.Context, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_control SET enabled = ?, updated_at = ? WHERE id = 1`, value, manager.now().UTC().Unix())
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) StoreSecret(id, secret string) error {
	return manager.secretStore.set(id, secret)
}

func (manager *Manager) MigrateSecrets() error {
	manager.secretStore.mu.Lock()
	defer manager.secretStore.mu.Unlock()
	return manager.secretStore.ensureMigrated()
}

func (manager *Manager) Secret(id string) (string, error) {
	return manager.secretStore.get(id)
}

func (manager *Manager) DeleteSecret(id string) error {
	return manager.secretStore.delete(id)
}

// PurgeLegacyKeySecrets removes complete External Interface keys persisted by
// earlier releases. Trigger authentication needs only the verifier in SQLite;
// unrelated recoverable secrets continue to use the encrypted secret store.
func (manager *Manager) PurgeLegacyKeySecrets(ctx context.Context) error {
	rows, err := manager.db.QueryContext(ctx, "SELECT id FROM external_trigger_keys")
	if err != nil {
		return fmt.Errorf("list legacy external trigger key secrets: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := manager.secretStore.delete(id); err != nil && !errors.Is(err, ErrSecretUnavailable) {
			return fmt.Errorf("purge legacy external trigger key secret %q: %w", id, err)
		}
	}
	return nil
}

func (manager *Manager) CreateGroup(ctx context.Context, label, callName string) (Group, error) {
	label = strings.TrimSpace(label)
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return Group{}, fmt.Errorf("%w: group label", ErrInvalidInput)
	}
	callName = strings.TrimSpace(callName)
	if !entryNamePattern.MatchString(callName) {
		return Group{}, fmt.Errorf("%w: group call name", ErrInvalidInput)
	}
	id, err := manager.randomToken(12)
	if err != nil {
		return Group{}, err
	}
	now := manager.now().UTC()
	result, err := manager.db.ExecContext(ctx, `INSERT INTO external_trigger_groups (id, label, call_name, enabled, created_at, updated_at)
		SELECT ?, ?, ?, 1, ?, ? WHERE NOT EXISTS (
			SELECT 1 FROM external_trigger_groups WHERE label = ? COLLATE NOCASE OR call_name = ? COLLATE NOCASE
		)`, id, label, callName, now.Unix(), now.Unix(), label, callName)
	if err != nil {
		return Group{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Group{}, ErrGroupNameExists
	}
	return Group{ID: id, Label: label, CallName: callName, Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

func (manager *Manager) UpdateGroup(ctx context.Context, id, label, callName string) (Group, error) {
	label, callName = strings.TrimSpace(label), strings.TrimSpace(callName)
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) || !entryNamePattern.MatchString(callName) {
		return Group{}, fmt.Errorf("%w: group", ErrInvalidInput)
	}
	now := manager.now().UTC()
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_groups SET label = ?, call_name = ?, updated_at = ?
		WHERE id = ? AND NOT EXISTS (
			SELECT 1 FROM external_trigger_groups duplicate WHERE duplicate.id <> ?
			AND (duplicate.label = ? COLLATE NOCASE OR duplicate.call_name = ? COLLATE NOCASE)
		)`, label, callName, now.Unix(), id, id, label, callName)
	if err != nil {
		return Group{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		if _, err := manager.Group(ctx, id); err != nil {
			return Group{}, err
		}
		return Group{}, ErrGroupNameExists
	}
	return manager.Group(ctx, id)
}

func (manager *Manager) Group(ctx context.Context, id string) (Group, error) {
	var group Group
	var enabled int
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, label, call_name, enabled, created_at, updated_at FROM external_trigger_groups WHERE id = ?`, id).Scan(&group.ID, &group.Label, &group.CallName, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return Group{}, err
	}
	group.Enabled = enabled != 0
	group.CreatedAt, group.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	group.Keys, err = manager.keysForGroup(ctx, id)
	if err != nil {
		return Group{}, err
	}
	group.Entries, err = manager.entriesForGroup(ctx, id)
	return group, err
}

func (manager *Manager) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id FROM external_trigger_groups ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(ids))
	for _, id := range ids {
		group, err := manager.Group(ctx, id)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (manager *Manager) DeleteGroup(ctx context.Context, id string) error {
	result, err := manager.db.ExecContext(ctx, `DELETE FROM external_trigger_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (manager *Manager) CreateKey(ctx context.Context, input CreateKeyInput) (Key, string, error) {
	label := strings.TrimSpace(input.Label)
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return Key{}, "", fmt.Errorf("%w: key label", ErrInvalidInput)
	}
	now := manager.now().UTC()
	groupID := strings.TrimSpace(input.GroupID)
	if groupID == "" {
		group, err := manager.CreateGroup(ctx, label, normalizedCallName(label))
		if err != nil {
			return Key{}, "", err
		}
		groupID = group.ID
	} else if _, err := manager.Group(ctx, groupID); err != nil {
		return Key{}, "", fmt.Errorf("%w: key group", ErrInvalidInput)
	}
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
		(id, group_id, label, token_hash, token_hint, enabled, expires_at, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM external_trigger_keys WHERE group_id = ? AND label = ? COLLATE NOCASE
		)`, id, groupID, label, hashToken(secret), hint, input.Enabled, expiresAt, now.Unix(), now.Unix(), groupID, label)
	if err != nil {
		return Key{}, "", fmt.Errorf("create external trigger key: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Key{}, "", ErrKeyLabelExists
	}
	key := Key{ID: id, GroupID: groupID, Label: label, TokenHint: hint, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
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
	if _, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_keys SET token_hash = ?, token_hint = ?, updated_at = ? WHERE id = ?`, hashToken(secret), hint, now.Unix(), id); err != nil {
		return Key{}, "", fmt.Errorf("rotate external trigger key: %w", err)
	}
	key.TokenHint, key.UpdatedAt = hint, now
	return key, secret, nil
}

func (manager *Manager) CreateEntry(ctx context.Context, input CreateEntryInput) (Entry, string, error) {
	configJSON, target, err := validateEntry(input.Name, input.Label, input.Type, input.Config)
	if err != nil {
		return Entry{}, "", err
	}
	id, err := manager.randomToken(18)
	if err != nil {
		return Entry{}, "", err
	}
	groupID := strings.TrimSpace(input.GroupID)
	if groupID == "" && input.KeyID != "" {
		key, keyErr := manager.Key(ctx, input.KeyID)
		if keyErr != nil {
			return Entry{}, "", keyErr
		}
		groupID = key.GroupID
	}
	if _, err := manager.Group(ctx, groupID); err != nil {
		return Entry{}, "", fmt.Errorf("%w: entry group", ErrInvalidInput)
	}
	secretPart, err := manager.randomToken(32)
	if err != nil {
		return Entry{}, "", err
	}
	secret := "sbk_" + input.KeyID + "." + secretPart
	hint := "sbk_" + input.KeyID + ".••••" + secretPart[len(secretPart)-4:]
	now := manager.now().UTC()
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, "", err
	}
	defer transaction.Rollback()
	var storedKeyID any
	if input.KeyID != "" {
		storedKeyID = input.KeyID
	}
	result, err := transaction.ExecContext(ctx, `INSERT INTO external_trigger_entries
		(id, group_id, key_id, name, label, action_type, target, config_json, require_signature, require_approval, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, groupID, storedKeyID, input.Name, strings.TrimSpace(input.Label), input.Type, target, configJSON, input.RequireSignature, input.RequireApproval, input.Enabled, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "external_trigger_entries.key_id") {
			return Entry{}, "", ErrKeyScopeBound
		}
		return Entry{}, "", fmt.Errorf("create external trigger entry: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Entry{}, "", ErrKeyScopeBound
	}
	if input.KeyID != "" {
		result, err = transaction.ExecContext(ctx, `UPDATE external_trigger_keys SET token_hash = ?, token_hint = ?, updated_at = ? WHERE id = ?`, hashToken(secret), hint, now.Unix(), input.KeyID)
		if err != nil {
			return Entry{}, "", err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return Entry{}, "", ErrInvalidKey
		}
	} else {
		secret = ""
	}
	if err := transaction.Commit(); err != nil {
		return Entry{}, "", err
	}
	return Entry{ID: id, GroupID: groupID, KeyID: input.KeyID, Name: input.Name, Label: strings.TrimSpace(input.Label), Type: input.Type, Target: target, ConfigJSON: configJSON, RequireSignature: input.RequireSignature, RequireApproval: input.RequireApproval, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}, secret, nil
}

func (manager *Manager) UpdateEntry(ctx context.Context, input UpdateEntryInput) (Entry, error) {
	configJSON, target, err := validateEntry(input.Name, input.Label, input.Type, input.Config)
	if err != nil {
		return Entry{}, err
	}
	if _, err := manager.Entry(ctx, input.ID); err != nil {
		return Entry{}, ErrEntryNotFound
	}
	now := manager.now().UTC()
	result, err := manager.db.ExecContext(ctx, `UPDATE external_trigger_entries SET
		name = ?, label = ?, action_type = ?, target = ?, config_json = ?, require_signature = ?, require_approval = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		input.Name, strings.TrimSpace(input.Label), input.Type, target, configJSON, input.RequireSignature, input.RequireApproval, input.Enabled, now.Unix(), input.ID)
	if err != nil {
		return Entry{}, fmt.Errorf("update external trigger entry: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Entry{}, ErrEntryNotFound
	}
	return manager.Entry(ctx, input.ID)
}

func validateEntry(name, label string, actionType ActionType, config any) (string, string, error) {
	label = strings.TrimSpace(label)
	if !entryNamePattern.MatchString(name) || label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		return "", "", fmt.Errorf("%w: entry identity", ErrInvalidInput)
	}
	var normalized any
	var target string
	switch actionType {
	case ActionLog:
		value, ok := config.(LogConfig)
		value.File = strings.TrimSpace(value.File)
		value.Category = strings.TrimSpace(value.Category)
		if !ok || value.File == "" || len([]byte(value.File)) > 4096 || !utf8.ValidString(value.File) ||
			len([]byte(value.Category)) > 64 || !utf8.ValidString(value.Category) || strings.IndexFunc(value.Category, unicode.IsControl) >= 0 ||
			value.MaxMessageBytes < 0 || value.MaxMessageBytes > 4<<10 ||
			(value.Rotate && (value.MaxFileBytes < 1<<20 || value.MaxFileBytes > 1<<30 || value.MaxBackups < 1 || value.MaxBackups > 100)) {
			return "", "", fmt.Errorf("%w: log config", ErrInvalidInput)
		}
		if value.MaxMessageBytes == 0 {
			value.MaxMessageBytes = 1024
		}
		if !value.Rotate {
			value.MaxFileBytes, value.MaxBackups = 0, 0
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
		digest, digestErr := hex.DecodeString(value.ScriptSHA256)
		if !ok || strings.TrimSpace(value.QuickRunID) == "" || value.Revision <= 0 || len(digest) != sha256.Size || digestErr != nil {
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
	err := manager.db.QueryRowContext(ctx, `SELECT id, group_id, label, token_hash, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
		FROM external_trigger_keys WHERE id = ?`, id).Scan(&key.ID, &key.GroupID, &key.Label, &tokenHash, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt)
	if err != nil || subtle.ConstantTimeCompare([]byte(tokenHash), []byte(hashToken(token))) != 1 {
		return Key{}, Entry{}, ErrInvalidKey
	}
	key.Enabled = enabled != 0
	key.CreatedAt, key.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	setOptionalTimes(&key, expiresAt, lastUsedAt)
	if !key.Active(manager.now().UTC()) {
		return Key{}, Entry{}, ErrInvalidKey
	}
	var groupEnabled int
	if err := manager.db.QueryRowContext(ctx, `SELECT enabled FROM external_trigger_groups WHERE id = ?`, key.GroupID).Scan(&groupEnabled); err != nil || groupEnabled == 0 {
		return Key{}, Entry{}, ErrInvalidKey
	}
	var entry Entry
	var entryEnabled, requireSignature, requireApproval int
	var entryCreatedAt, entryUpdatedAt int64
	err = manager.db.QueryRowContext(ctx, `SELECT id, group_id, COALESCE(key_id, ''), name, label, action_type, target, config_json, require_signature, require_approval, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE group_id = ? AND name = ? AND action_type <> 'website_monitor'`, key.GroupID, name).Scan(
		&entry.ID, &entry.GroupID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &requireSignature, &requireApproval, &entryEnabled, &entryCreatedAt, &entryUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return key, Entry{}, ErrEntryNotFound
	}
	if err != nil {
		return Key{}, Entry{}, err
	}
	entry.Enabled = entryEnabled != 0
	entry.RequireSignature = requireSignature != 0
	entry.RequireApproval = requireApproval != 0
	entry.CreatedAt, entry.UpdatedAt = time.Unix(entryCreatedAt, 0).UTC(), time.Unix(entryUpdatedAt, 0).UTC()
	if !entry.Enabled {
		return key, entry, ErrEntryDisabled
	}
	return key, entry, nil
}

func (manager *Manager) ResolveScoped(ctx context.Context, token, groupLabel, name string) (Key, Entry, error) {
	key, entry, err := manager.Resolve(ctx, token, name)
	if err != nil {
		return key, entry, err
	}
	var storedCallName string
	if err := manager.db.QueryRowContext(ctx, `SELECT call_name FROM external_trigger_groups WHERE id = ?`, key.GroupID).Scan(&storedCallName); err != nil {
		return Key{}, Entry{}, ErrInvalidKey
	}
	if !strings.EqualFold(storedCallName, groupLabel) {
		return key, Entry{}, ErrEntryNotFound
	}
	return key, entry, nil
}

func (manager *Manager) Key(ctx context.Context, id string) (Key, error) {
	var key Key
	var enabled int
	var expiresAt, lastUsedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, group_id, label, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
		FROM external_trigger_keys WHERE id = ?`, id).Scan(&key.ID, &key.GroupID, &key.Label, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt)
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
	var enabled, requireSignature, requireApproval int
	var createdAt, updatedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT id, group_id, COALESCE(key_id, ''), name, label, action_type, target, config_json, require_signature, require_approval, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE id = ? AND action_type <> 'website_monitor'`, id).Scan(&entry.ID, &entry.GroupID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &requireSignature, &requireApproval, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return Entry{}, err
	}
	entry.Enabled = enabled != 0
	entry.RequireSignature = requireSignature != 0
	entry.RequireApproval = requireApproval != 0
	entry.CreatedAt, entry.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return entry, nil
}

func (manager *Manager) List(ctx context.Context) ([]Key, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, group_id, label, token_hint, enabled, expires_at, created_at, updated_at, last_used_at
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
		if err := rows.Scan(&key.ID, &key.GroupID, &key.Label, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt); err != nil {
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
	key, err := manager.Key(ctx, keyID)
	if err != nil {
		return nil, err
	}
	return manager.entriesForGroup(ctx, key.GroupID)
}

func (manager *Manager) entriesForGroup(ctx context.Context, groupID string) ([]Entry, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, group_id, COALESCE(key_id, ''), name, label, action_type, target, config_json, require_signature, require_approval, enabled, created_at, updated_at
		FROM external_trigger_entries WHERE group_id = ? AND action_type <> 'website_monitor' ORDER BY created_at, id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var entry Entry
		var enabled, requireSignature, requireApproval int
		var createdAt, updatedAt int64
		if err := rows.Scan(&entry.ID, &entry.GroupID, &entry.KeyID, &entry.Name, &entry.Label, &entry.Type, &entry.Target, &entry.ConfigJSON, &requireSignature, &requireApproval, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		entry.Enabled = enabled != 0
		entry.RequireSignature = requireSignature != 0
		entry.RequireApproval = requireApproval != 0
		entry.CreatedAt, entry.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (manager *Manager) EntriesForGroup(ctx context.Context, groupID string) ([]Entry, error) {
	return manager.entriesForGroup(ctx, groupID)
}

func (manager *Manager) keysForGroup(ctx context.Context, groupID string) ([]Key, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT id, group_id, label, token_hint, enabled, expires_at, created_at, updated_at, last_used_at FROM external_trigger_keys WHERE group_id = ? ORDER BY created_at, id`, groupID)
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
		if err := rows.Scan(&key.ID, &key.GroupID, &key.Label, &key.TokenHint, &enabled, &expiresAt, &createdAt, &updatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		key.Enabled = enabled != 0
		key.CreatedAt, key.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		setOptionalTimes(&key, expiresAt, lastUsedAt)
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (manager *Manager) EntriesForKey(ctx context.Context, keyID string) ([]Entry, error) {
	return manager.entriesForKey(ctx, keyID)
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
			WHERE duplicate.id <> ? AND duplicate.group_id = external_trigger_keys.group_id AND duplicate.label = ? COLLATE NOCASE
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
	err := manager.completeInvocation(ctx, invocation)
	if err == nil {
		_ = manager.removeQueuedCompletion(invocation.ID)
		return nil
	}
	if queueErr := manager.queueCompletion(invocation); queueErr != nil {
		return errors.Join(err, fmt.Errorf("persist invocation completion for retry: %w", queueErr))
	}
	return fmt.Errorf("invocation completion queued for retry: %w", err)
}

func (manager *Manager) completeInvocation(ctx context.Context, invocation Invocation) error {
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
