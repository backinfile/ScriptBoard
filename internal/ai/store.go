package ai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	db        *sql.DB
	stateRoot string
	now       func() time.Time
}

type Settings struct {
	DefaultProfileID   string
	MaxConcurrentTurns int
	KillSwitch         bool
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var result Settings
	err := s.db.QueryRowContext(ctx, `SELECT default_profile_id,max_concurrent_turns,kill_switch
		FROM ai_settings WHERE id=1`).Scan(&result.DefaultProfileID, &result.MaxConcurrentTurns, &result.KillSwitch)
	return result, err
}

func (s *Store) SaveSettings(ctx context.Context, settings Settings) error {
	if settings.MaxConcurrentTurns < 1 || settings.MaxConcurrentTurns > 16 {
		return errors.New("AI concurrency must be between 1 and 16")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ai_settings SET default_profile_id=?,max_concurrent_turns=?,kill_switch=?,updated_at=? WHERE id=1`,
		settings.DefaultProfileID, settings.MaxConcurrentTurns, settings.KillSwitch, s.now().UTC().UnixNano())
	return err
}

func NewStore(db *sql.DB, stateRoot string) *Store {
	return &Store{db: db, stateRoot: stateRoot, now: time.Now}
}

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS ai_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		default_profile_id TEXT NOT NULL DEFAULT '',
		max_concurrent_turns INTEGER NOT NULL DEFAULT 4 CHECK (max_concurrent_turns BETWEEN 1 AND 16),
		kill_switch INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS ai_profiles (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		protocol TEXT NOT NULL,
		base_url TEXT NOT NULL,
		model TEXT NOT NULL,
		auth_mode TEXT NOT NULL,
		api_key_ref TEXT NOT NULL DEFAULT '',
		extra_header_refs_json TEXT NOT NULL DEFAULT '{}',
		context_window INTEGER NOT NULL DEFAULT 128000,
		max_output_tokens INTEGER NOT NULL DEFAULT 4096,
		permission_json TEXT NOT NULL,
		allow_sensitive_reads INTEGER NOT NULL DEFAULT 0,
		auto_approve INTEGER NOT NULL DEFAULT 1,
		default_run_timeout_seconds INTEGER NOT NULL DEFAULT 300,
		disabled INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		last_error_at INTEGER,
		last_test_at INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS ai_conversations (
		id TEXT PRIMARY KEY,
		profile_id TEXT NOT NULL REFERENCES ai_profiles(id),
		title TEXT NOT NULL,
		permission_json TEXT NOT NULL,
		context_type TEXT NOT NULL DEFAULT '',
		context_id TEXT NOT NULL DEFAULT '',
		context_summary TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS ai_conversations_updated_idx ON ai_conversations(updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS ai_messages (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		sequence INTEGER NOT NULL,
		message_json TEXT NOT NULL,
		usage_json TEXT NOT NULL DEFAULT '{}',
		model_snapshot TEXT NOT NULL DEFAULT '',
		skill_snapshot_json TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		UNIQUE(conversation_id, sequence)
	)`,
	`CREATE TABLE IF NOT EXISTS ai_turns (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		profile_id TEXT NOT NULL,
		profile_snapshot TEXT NOT NULL,
		status TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ai_one_active_turn ON ai_turns(conversation_id) WHERE status IN ('queued','running')`,
	`CREATE TABLE IF NOT EXISTS ai_batches (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		turn_id TEXT NOT NULL REFERENCES ai_turns(id),
		status TEXT NOT NULL,
		digest TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ai_one_open_batch ON ai_batches(conversation_id) WHERE status IN ('pending','running')`,
	`CREATE TABLE IF NOT EXISTS ai_batch_actions (
		batch_id TEXT NOT NULL REFERENCES ai_batches(id) ON DELETE CASCADE,
		sequence INTEGER NOT NULL,
		action_json TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		result_json TEXT NOT NULL DEFAULT '{}',
		error TEXT NOT NULL DEFAULT '',
		started_at INTEGER,
		finished_at INTEGER,
		PRIMARY KEY(batch_id, sequence)
	)`,
	`CREATE TABLE IF NOT EXISTS ai_attachments (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		stored_path TEXT NOT NULL,
		media_type TEXT NOT NULL,
		size INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		imported INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS ai_skill_usage (
		turn_id TEXT NOT NULL REFERENCES ai_turns(id) ON DELETE CASCADE,
		skill_id TEXT NOT NULL,
		version TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		content TEXT NOT NULL,
		used_at INTEGER NOT NULL,
		PRIMARY KEY(turn_id,skill_id,content_hash)
	)`,
	`CREATE TABLE IF NOT EXISTS ai_history_summaries (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		covered_through_sequence INTEGER NOT NULL,
		model TEXT NOT NULL,
		summary TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS ai_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
		turn_id TEXT NOT NULL DEFAULT '',
		batch_id TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		occurred_at INTEGER NOT NULL
	)`,
}

func (s *Store) RecordSkillUsage(ctx context.Context, turnID string, skill Skill) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_skill_usage
		(turn_id,skill_id,version,content_hash,content,used_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(turn_id,skill_id,content_hash) DO NOTHING`,
		turnID, skill.ID, skill.Version, skill.Hash, skill.Content, s.now().UTC().UnixNano())
	return err
}

func (s *Store) Initialize(ctx context.Context) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, statement := range SchemaStatements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize AI storage: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO ai_settings (id, updated_at) VALUES (1, ?)
		ON CONFLICT(id) DO NOTHING`, s.now().UTC().UnixNano()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (s *Store) SaveProfile(ctx context.Context, profile ModelProfile) error {
	if profile.ID == "" {
		return errors.New("profile ID is required")
	}
	if strings.TrimSpace(profile.Name) == "" {
		return errors.New("profile name is required")
	}
	if err := ValidateEndpoint(profile.BaseURL); err != nil {
		return err
	}
	if profile.ContextWindow <= 0 {
		profile.ContextWindow = 128000
	}
	if profile.MaxOutputTokens <= 0 {
		profile.MaxOutputTokens = 4096
	}
	if profile.DefaultRunTimeoutSec < 0 {
		return errors.New("default Run timeout cannot be negative")
	}
	if profile.DefaultRunTimeoutSec == 0 {
		profile.DefaultRunTimeoutSec = 300
	}
	profile.Permission = profile.Permission.normalized()
	permissionJSON, _ := json.Marshal(profile.Permission)
	headersJSON, _ := json.Marshal(profile.ExtraHeaderRefs)
	now := s.now().UTC().UnixNano()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_profiles
		(id,name,protocol,base_url,model,auth_mode,api_key_ref,extra_header_refs_json,context_window,max_output_tokens,permission_json,allow_sensitive_reads,auto_approve,default_run_timeout_seconds,disabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,protocol=excluded.protocol,base_url=excluded.base_url,
		model=excluded.model,auth_mode=excluded.auth_mode,api_key_ref=excluded.api_key_ref,
		extra_header_refs_json=excluded.extra_header_refs_json,context_window=excluded.context_window,
		max_output_tokens=excluded.max_output_tokens,permission_json=excluded.permission_json,
		allow_sensitive_reads=excluded.allow_sensitive_reads,auto_approve=excluded.auto_approve,
		default_run_timeout_seconds=excluded.default_run_timeout_seconds,disabled=excluded.disabled,updated_at=excluded.updated_at`,
		profile.ID, profile.Name, profile.Protocol, profile.BaseURL, profile.Model, profile.AuthMode,
		profile.APIKeyRef, string(headersJSON), profile.ContextWindow, profile.MaxOutputTokens, string(permissionJSON),
		profile.AllowSensitiveReads, profile.AutoApprove, profile.DefaultRunTimeoutSec, profile.Disabled, now, now)
	return err
}

func (s *Store) GetProfile(ctx context.Context, id string) (ModelProfile, error) {
	var profile ModelProfile
	var permissionJSON, headersJSON string
	var lastErrorAt, lastTestAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,name,protocol,base_url,model,auth_mode,api_key_ref,
		extra_header_refs_json,context_window,max_output_tokens,permission_json,allow_sensitive_reads,
		auto_approve,default_run_timeout_seconds,disabled,last_error,last_error_at,last_test_at FROM ai_profiles WHERE id=?`, id).Scan(
		&profile.ID, &profile.Name, &profile.Protocol, &profile.BaseURL, &profile.Model, &profile.AuthMode,
		&profile.APIKeyRef, &headersJSON, &profile.ContextWindow, &profile.MaxOutputTokens, &permissionJSON,
		&profile.AllowSensitiveReads, &profile.AutoApprove, &profile.DefaultRunTimeoutSec, &profile.Disabled,
		&profile.LastError, &lastErrorAt, &lastTestAt,
	)
	if err != nil {
		return ModelProfile{}, err
	}
	if err := json.Unmarshal([]byte(permissionJSON), &profile.Permission); err != nil {
		return ModelProfile{}, err
	}
	if err := json.Unmarshal([]byte(headersJSON), &profile.ExtraHeaderRefs); err != nil {
		return ModelProfile{}, err
	}
	if lastErrorAt.Valid {
		value := time.Unix(0, lastErrorAt.Int64)
		profile.LastErrorAt = &value
	}
	if lastTestAt.Valid {
		value := time.Unix(0, lastTestAt.Int64)
		profile.LastTestAt = &value
	}
	return profile, nil
}

func (s *Store) ListProfiles(ctx context.Context, includeDisabled bool) ([]ModelProfile, error) {
	query := "SELECT id FROM ai_profiles"
	if !includeDisabled {
		query += " WHERE disabled=0"
	}
	query += " ORDER BY name,id"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]ModelProfile, 0, len(ids))
	for _, id := range ids {
		profile, err := s.GetProfile(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, profile)
	}
	return result, nil
}

func (s *Store) DisableProfile(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ai_profiles SET disabled=1,api_key_ref='',extra_header_refs_json='{}',updated_at=? WHERE id=?`,
		s.now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RecordProfileDiagnostic(ctx context.Context, id string, diagnostic error) error {
	now := s.now().UTC().UnixNano()
	if diagnostic == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE ai_profiles
			SET last_error='',last_error_at=NULL,last_test_at=? WHERE id=?`, now, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ai_profiles
		SET last_error=?,last_error_at=?,last_test_at=? WHERE id=?`, diagnostic.Error(), now, now, id)
	return err
}

type Conversation struct {
	ID             string
	ProfileID      string
	Title          string
	Permission     Permission
	ContextType    string
	ContextID      string
	ContextSummary string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s *Store) CreateConversation(ctx context.Context, profileID string, permission Permission, firstMessage string) (Conversation, error) {
	profile, err := s.GetProfile(ctx, profileID)
	if err != nil {
		return Conversation{}, err
	}
	permission = EffectivePermission(profile.Permission, permission, Permission{Query: true, Execute: true, Modify: true})
	id, err := randomAIID()
	if err != nil {
		return Conversation{}, err
	}
	title := strings.TrimSpace(firstMessage)
	runes := []rune(title)
	if len(runes) > 60 {
		title = string(runes[:60]) + "…"
	}
	if title == "" {
		title = "新会话"
	}
	permissionJSON, _ := json.Marshal(permission)
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_conversations
		(id,profile_id,title,permission_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		id, profileID, title, string(permissionJSON), now.UnixNano(), now.UnixNano())
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{ID: id, ProfileID: profileID, Title: title, Permission: permission, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	var permissionJSON string
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,profile_id,title,permission_json,context_type,context_id,context_summary,created_at,updated_at
		FROM ai_conversations WHERE id=?`, id).Scan(
		&result.ID, &result.ProfileID, &result.Title, &permissionJSON, &result.ContextType,
		&result.ContextID, &result.ContextSummary, &created, &updated,
	)
	if err != nil {
		return Conversation{}, err
	}
	if err := json.Unmarshal([]byte(permissionJSON), &result.Permission); err != nil {
		return Conversation{}, err
	}
	result.Permission = result.Permission.normalized()
	result.CreatedAt, result.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
	return result, nil
}

func (s *Store) ListConversations(ctx context.Context, search string, limit, offset int) ([]Conversation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	pattern := "%" + strings.ReplaceAll(strings.TrimSpace(search), "%", `\%`) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT c.id FROM ai_conversations c
		LEFT JOIN ai_messages m ON m.conversation_id=c.id
		WHERE ?='' OR c.title LIKE ? ESCAPE '\' OR m.message_json LIKE ? ESCAPE '\'
		ORDER BY c.updated_at DESC LIMIT ? OFFSET ?`, search, pattern, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]Conversation, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetConversation(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) UpdateConversation(ctx context.Context, id, title, contextType, contextID, contextSummary string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("conversation title is required")
	}
	runes := []rune(title)
	if len(runes) > 120 {
		return errors.New("conversation title exceeds 120 characters")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE ai_conversations
		SET title=?,context_type=?,context_id=?,context_summary=?,updated_at=? WHERE id=?`,
		title, contextType, contextID, contextSummary, s.now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateConversationTitle(ctx context.Context, id, title string) error {
	conversation, err := s.GetConversation(ctx, id)
	if err != nil {
		return err
	}
	return s.UpdateConversation(ctx, id, title, conversation.ContextType, conversation.ContextID, conversation.ContextSummary)
}

func (s *Store) SwitchConversationProfile(ctx context.Context, id, profileID string) error {
	profile, err := s.GetProfile(ctx, profileID)
	if err != nil {
		return err
	}
	if profile.Disabled {
		return errors.New("model profile is disabled")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE ai_conversations SET profile_id=?,updated_at=? WHERE id=?",
		profileID, s.now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	rows, err := s.db.QueryContext(ctx, "SELECT stored_path FROM ai_attachments WHERE conversation_id=?", id)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, stored)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM ai_conversations WHERE id=?", id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	attachmentRoot := filepath.Join(s.stateRoot, "ai", "attachments")
	for _, stored := range paths {
		path := filepath.Clean(stored)
		relative, relErr := filepath.Rel(attachmentRoot, path)
		if relErr == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".." {
			_ = os.Remove(path)
		}
	}
	return nil
}

type StoredMessage struct {
	ID        string
	Sequence  int
	Message   ModelMessage
	Usage     Usage
	CreatedAt time.Time
}

type HistorySummary struct {
	ID, ConversationID, Model, Summary string
	CoveredThrough                     int
	CreatedAt                          time.Time
}

func (s *Store) SaveHistorySummary(ctx context.Context, conversationID string, coveredThrough int, model, summary string) (HistorySummary, error) {
	id, err := randomAIID()
	if err != nil {
		return HistorySummary{}, err
	}
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_history_summaries
		(id,conversation_id,covered_through_sequence,model,summary,created_at) VALUES(?,?,?,?,?,?)`,
		id, conversationID, coveredThrough, model, summary, now.UnixNano())
	if err != nil {
		return HistorySummary{}, err
	}
	return HistorySummary{ID: id, ConversationID: conversationID, CoveredThrough: coveredThrough, Model: model, Summary: summary, CreatedAt: now}, nil
}

func (s *Store) LatestHistorySummary(ctx context.Context, conversationID string) (HistorySummary, error) {
	var result HistorySummary
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id,conversation_id,covered_through_sequence,model,summary,created_at
		FROM ai_history_summaries WHERE conversation_id=? ORDER BY covered_through_sequence DESC,created_at DESC LIMIT 1`,
		conversationID).Scan(&result.ID, &result.ConversationID, &result.CoveredThrough, &result.Model, &result.Summary, &created)
	if err != nil {
		return HistorySummary{}, err
	}
	result.CreatedAt = time.Unix(0, created)
	return result, nil
}

type Attachment struct {
	ID, ConversationID, Name, StoredPath, MediaType, SHA256 string
	Size                                                    int64
	Imported                                                bool
	CreatedAt, ExpiresAt                                    time.Time
}

func (s *Store) CreateAttachment(ctx context.Context, conversationID, name, mediaType string, source io.Reader) (Attachment, error) {
	if _, err := s.GetConversation(ctx, conversationID); err != nil {
		return Attachment{}, err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || strings.ContainsAny(name, `/\`) {
		return Attachment{}, errors.New("invalid attachment name")
	}
	var currentSize int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(size),0) FROM ai_attachments WHERE conversation_id=? AND imported=0",
		conversationID).Scan(&currentSize); err != nil {
		return Attachment{}, err
	}
	const singleLimit = int64(100 << 20)
	if currentSize >= 500<<20 {
		return Attachment{}, errors.New("conversation attachment limit is 500 MiB")
	}
	id, err := randomAIID()
	if err != nil {
		return Attachment{}, err
	}
	root := filepath.Join(s.stateRoot, "ai", "attachments", conversationID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Attachment{}, err
	}
	stored := filepath.Join(root, id)
	file, err := os.OpenFile(stored, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Attachment{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, singleLimit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > singleLimit || currentSize+written > 500<<20 {
		_ = os.Remove(stored)
		switch {
		case copyErr != nil:
			return Attachment{}, copyErr
		case closeErr != nil:
			return Attachment{}, closeErr
		case written > singleLimit:
			return Attachment{}, errors.New("attachment exceeds 100 MiB")
		default:
			return Attachment{}, errors.New("conversation attachment limit is 500 MiB")
		}
	}
	now := s.now().UTC()
	item := Attachment{
		ID: id, ConversationID: conversationID, Name: name, StoredPath: stored, MediaType: mediaType,
		Size: written, SHA256: hex.EncodeToString(hash.Sum(nil)), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_attachments
		(id,conversation_id,name,stored_path,media_type,size,sha256,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.ConversationID, item.Name, item.StoredPath, item.MediaType, item.Size, item.SHA256,
		item.CreatedAt.UnixNano(), item.ExpiresAt.UnixNano())
	if err != nil {
		_ = os.Remove(stored)
		return Attachment{}, err
	}
	return item, nil
}

func (s *Store) GetAttachment(ctx context.Context, conversationID, id string) (Attachment, error) {
	var item Attachment
	var created, expires int64
	err := s.db.QueryRowContext(ctx, `SELECT id,conversation_id,name,stored_path,media_type,size,sha256,imported,created_at,expires_at
		FROM ai_attachments WHERE id=? AND conversation_id=?`, id, conversationID).Scan(
		&item.ID, &item.ConversationID, &item.Name, &item.StoredPath, &item.MediaType, &item.Size,
		&item.SHA256, &item.Imported, &created, &expires)
	if err != nil {
		return Attachment{}, err
	}
	item.CreatedAt, item.ExpiresAt = time.Unix(0, created), time.Unix(0, expires)
	if !item.Imported && s.now().After(item.ExpiresAt) {
		return Attachment{}, errors.New("attachment expired")
	}
	return item, nil
}

func (s *Store) ListAttachments(ctx context.Context, conversationID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM ai_attachments WHERE conversation_id=? AND imported=0 ORDER BY created_at", conversationID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetAttachment(ctx, conversationID, id)
		if err == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *Store) ConsumeAttachment(ctx context.Context, conversationID, id string) error {
	item, err := s.GetAttachment(ctx, conversationID, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM ai_attachments WHERE id=? AND conversation_id=?", id, conversationID); err != nil {
		return err
	}
	return os.Remove(item.StoredPath)
}

func (s *Store) CleanupExpiredAttachments(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,stored_path FROM ai_attachments
		WHERE imported=0 AND expires_at<=?`, s.now().UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	type expired struct{ id, path string }
	var items []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.path); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, item := range items {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM ai_attachments WHERE id=?", item.id); err != nil {
			return 0, err
		}
		_ = os.Remove(item.path)
	}
	return len(items), nil
}

func (s *Store) AppendMessage(ctx context.Context, conversationID string, message ModelMessage, usage *Usage) (StoredMessage, error) {
	id, err := randomAIID()
	if err != nil {
		return StoredMessage{}, err
	}
	messageJSON, err := json.Marshal(message)
	if err != nil {
		return StoredMessage{}, err
	}
	usageValue := Usage{}
	if usage != nil {
		usageValue = *usage
	}
	usageJSON, _ := json.Marshal(usageValue)
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StoredMessage{}, err
	}
	defer transaction.Rollback()
	var sequence int
	if err := transaction.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM ai_messages WHERE conversation_id=?", conversationID).Scan(&sequence); err != nil {
		return StoredMessage{}, err
	}
	now := s.now().UTC()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO ai_messages
		(id,conversation_id,sequence,message_json,usage_json,created_at) VALUES(?,?,?,?,?,?)`,
		id, conversationID, sequence, string(messageJSON), string(usageJSON), now.UnixNano()); err != nil {
		return StoredMessage{}, err
	}
	if _, err := transaction.ExecContext(ctx, "UPDATE ai_conversations SET updated_at=? WHERE id=?", now.UnixNano(), conversationID); err != nil {
		return StoredMessage{}, err
	}
	if err := transaction.Commit(); err != nil {
		return StoredMessage{}, err
	}
	return StoredMessage{ID: id, Sequence: sequence, Message: message, Usage: usageValue, CreatedAt: now}, nil
}

func (s *Store) ListMessages(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,sequence,message_json,usage_json,created_at
		FROM ai_messages WHERE conversation_id=? ORDER BY sequence`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []StoredMessage
	for rows.Next() {
		var item StoredMessage
		var messageJSON, usageJSON string
		var created int64
		if err := rows.Scan(&item.ID, &item.Sequence, &messageJSON, &usageJSON, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(messageJSON), &item.Message); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(usageJSON), &item.Usage); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(0, created)
		result = append(result, item)
	}
	return result, rows.Err()
}

type TurnStatus string

const (
	TurnQueued      TurnStatus = "queued"
	TurnRunning     TurnStatus = "running"
	TurnCompleted   TurnStatus = "completed"
	TurnFailed      TurnStatus = "failed"
	TurnCancelled   TurnStatus = "cancelled"
	TurnInterrupted TurnStatus = "interrupted"
)

type Turn struct {
	ID, ConversationID, ProfileID, ProfileSnapshot, Error string
	Status                                                TurnStatus
	StartedAt                                             time.Time
	FinishedAt                                            *time.Time
}

type Event struct {
	ID                              int64
	ConversationID, TurnID, BatchID string
	Type                            string
	Payload                         json.RawMessage
	OccurredAt                      time.Time
}

func (s *Store) AddEvent(ctx context.Context, conversationID, turnID, batchID, eventType string, payload json.RawMessage) (Event, error) {
	if !json.Valid(payload) {
		return Event{}, errors.New("event payload must be valid JSON")
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO ai_events
		(conversation_id,turn_id,batch_id,event_type,payload_json,occurred_at) VALUES(?,?,?,?,?,?)`,
		conversationID, turnID, batchID, eventType, string(payload), now.UnixNano())
	if err != nil {
		return Event{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Event{}, err
	}
	return Event{ID: id, ConversationID: conversationID, TurnID: turnID, BatchID: batchID, Type: eventType, Payload: append(json.RawMessage(nil), payload...), OccurredAt: now}, nil
}

func (s *Store) ListEvents(ctx context.Context, conversationID string, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,conversation_id,turn_id,batch_id,event_type,payload_json,occurred_at
		FROM ai_events WHERE conversation_id=? AND id>? ORDER BY id LIMIT ?`, conversationID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var payload string
		var occurred int64
		if err := rows.Scan(&event.ID, &event.ConversationID, &event.TurnID, &event.BatchID, &event.Type, &payload, &occurred); err != nil {
			return nil, err
		}
		event.Payload = json.RawMessage(payload)
		event.OccurredAt = time.Unix(0, occurred)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) LatestTerminalEventID(ctx context.Context, conversationID string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM ai_events
		WHERE conversation_id=? AND event_type IN ('turn_finished','batch_finished')`, conversationID).Scan(&id)
	return id, err
}

func (s *Store) LatestEventID(ctx context.Context, conversationID string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM ai_events WHERE conversation_id=?", conversationID).Scan(&id)
	return id, err
}

func (s *Store) StartTurn(ctx context.Context, conversationID, profileID, snapshot string) (Turn, error) {
	id, err := randomAIID()
	if err != nil {
		return Turn{}, err
	}
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_turns
		(id,conversation_id,profile_id,profile_snapshot,status,started_at) VALUES(?,?,?,?,?,?)`,
		id, conversationID, profileID, snapshot, TurnRunning, now.UnixNano())
	if err != nil {
		return Turn{}, err
	}
	return Turn{ID: id, ConversationID: conversationID, ProfileID: profileID, ProfileSnapshot: snapshot, Status: TurnRunning, StartedAt: now}, nil
}

func (s *Store) GetTurn(ctx context.Context, id string) (Turn, error) {
	var result Turn
	var started int64
	var finished sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,conversation_id,profile_id,profile_snapshot,status,error,started_at,finished_at
		FROM ai_turns WHERE id=?`, id).Scan(&result.ID, &result.ConversationID, &result.ProfileID,
		&result.ProfileSnapshot, &result.Status, &result.Error, &started, &finished)
	if err != nil {
		return Turn{}, err
	}
	result.StartedAt = time.Unix(0, started)
	if finished.Valid {
		value := time.Unix(0, finished.Int64)
		result.FinishedAt = &value
	}
	return result, nil
}

func (s *Store) LatestTurn(ctx context.Context, conversationID string) (Turn, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM ai_turns WHERE conversation_id=?
		ORDER BY started_at DESC LIMIT 1`, conversationID).Scan(&id); err != nil {
		return Turn{}, err
	}
	return s.GetTurn(ctx, id)
}

func (s *Store) TurnHasBatch(ctx context.Context, turnID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ai_batches WHERE turn_id=?)", turnID).Scan(&exists)
	return exists, err
}

func (s *Store) FinishTurn(ctx context.Context, id string, status TurnStatus, errorText string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE ai_turns SET status=?,error=?,finished_at=? WHERE id=?",
		status, errorText, s.now().UTC().UnixNano(), id)
	return err
}

type Risk string

const (
	RiskQuery   Risk = "query"
	RiskExecute Risk = "execute"
	RiskModify  Risk = "modify"
)

type Action struct {
	Kind            string          `json:"kind"`
	Risk            Risk            `json:"risk"`
	Summary         string          `json:"summary"`
	Input           json.RawMessage `json:"input"`
	ExpectedVersion string          `json:"expected_version,omitempty"`
	Sensitive       bool            `json:"sensitive,omitempty"`
}

type BatchStatus string

const (
	BatchPending     BatchStatus = "pending"
	BatchRunning     BatchStatus = "running"
	BatchCompleted   BatchStatus = "completed"
	BatchRejected    BatchStatus = "rejected"
	BatchCancelled   BatchStatus = "cancelled"
	BatchExpired     BatchStatus = "expired"
	BatchInterrupted BatchStatus = "interrupted"
	BatchFailed      BatchStatus = "failed"
)

type Batch struct {
	ID, ConversationID, TurnID, Digest, Error string
	Status                                    BatchStatus
	Actions                                   []Action
	ExpiresAt, CreatedAt, UpdatedAt           time.Time
}

type BatchAction struct {
	Sequence              int
	Action                Action
	Status                string
	Result                json.RawMessage
	Error                 string
	StartedAt, FinishedAt *time.Time
}

func (s *Store) SubmitBatch(ctx context.Context, conversationID, turnID string, actions []Action, expiresAt time.Time) (Batch, error) {
	if len(actions) == 0 || len(actions) > 20 {
		return Batch{}, errors.New("an action batch must contain 1 to 20 actions")
	}
	encoded, err := json.Marshal(actions)
	if err != nil {
		return Batch{}, err
	}
	if len(encoded) > 1<<20 {
		return Batch{}, errors.New("action batch exceeds 1 MiB")
	}
	sum := sha256.Sum256(encoded)
	id, err := randomAIID()
	if err != nil {
		return Batch{}, err
	}
	now := s.now().UTC()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO ai_batches
		(id,conversation_id,turn_id,status,digest,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, conversationID, turnID, BatchPending, hex.EncodeToString(sum[:]), expiresAt.UTC().UnixNano(), now.UnixNano(), now.UnixNano()); err != nil {
		return Batch{}, err
	}
	for index, action := range actions {
		value, _ := json.Marshal(action)
		if _, err := transaction.ExecContext(ctx, "INSERT INTO ai_batch_actions(batch_id,sequence,action_json) VALUES(?,?,?)", id, index, string(value)); err != nil {
			return Batch{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return Batch{}, err
	}
	return Batch{ID: id, ConversationID: conversationID, TurnID: turnID, Status: BatchPending, Digest: hex.EncodeToString(sum[:]), Actions: actions, ExpiresAt: expiresAt.UTC(), CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) SetBatchStatus(ctx context.Context, id string, status BatchStatus, errorText string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE ai_batches SET status=?,error=?,updated_at=? WHERE id=?",
		status, errorText, s.now().UTC().UnixNano(), id)
	return err
}

func (s *Store) GetBatch(ctx context.Context, id string) (Batch, error) {
	var result Batch
	var expires, created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,conversation_id,turn_id,status,digest,error,expires_at,created_at,updated_at
		FROM ai_batches WHERE id=?`, id).Scan(&result.ID, &result.ConversationID, &result.TurnID,
		&result.Status, &result.Digest, &result.Error, &expires, &created, &updated)
	if err != nil {
		return Batch{}, err
	}
	result.ExpiresAt, result.CreatedAt, result.UpdatedAt = time.Unix(0, expires), time.Unix(0, created), time.Unix(0, updated)
	rows, err := s.db.QueryContext(ctx, "SELECT action_json FROM ai_batch_actions WHERE batch_id=? ORDER BY sequence", id)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		var action Action
		if err := rows.Scan(&value); err != nil {
			return Batch{}, err
		}
		if err := json.Unmarshal([]byte(value), &action); err != nil {
			return Batch{}, err
		}
		result.Actions = append(result.Actions, action)
	}
	return result, rows.Err()
}

func (s *Store) ListBatches(ctx context.Context, conversationID string, limit int) ([]Batch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if _, err := s.HasOpenBatch(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM ai_batches
		WHERE conversation_id=? ORDER BY created_at DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	batches := make([]Batch, 0, len(ids))
	for _, id := range ids {
		batch, err := s.GetBatch(ctx, id)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func (s *Store) ListBatchActions(ctx context.Context, batchID string) ([]BatchAction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,action_json,status,result_json,error,started_at,finished_at
		FROM ai_batch_actions WHERE batch_id=? ORDER BY sequence`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var actions []BatchAction
	for rows.Next() {
		var item BatchAction
		var actionJSON, resultJSON string
		var started, finished sql.NullInt64
		if err := rows.Scan(&item.Sequence, &actionJSON, &item.Status, &resultJSON, &item.Error, &started, &finished); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(actionJSON), &item.Action); err != nil {
			return nil, err
		}
		item.Result = json.RawMessage(resultJSON)
		if started.Valid {
			value := time.Unix(0, started.Int64)
			item.StartedAt = &value
		}
		if finished.Valid {
			value := time.Unix(0, finished.Int64)
			item.FinishedAt = &value
		}
		actions = append(actions, item)
	}
	return actions, rows.Err()
}

func (s *Store) HasOpenBatch(ctx context.Context, conversationID string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE ai_batches SET status=?,error='approval expired',updated_at=?
		WHERE conversation_id=? AND status='pending' AND expires_at<?`,
		BatchExpired, s.now().UTC().UnixNano(), conversationID, s.now().UTC().UnixNano()); err != nil {
		return false, err
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_batches
		WHERE conversation_id=? AND status IN ('pending','running'))`, conversationID).Scan(&exists)
	return exists, err
}

func (s *Store) StartBatchAction(ctx context.Context, batchID string, sequence int) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ai_batch_actions SET status='running',started_at=?
		WHERE batch_id=? AND sequence=? AND status='pending'`, s.now().UTC().UnixNano(), batchID, sequence)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return errors.New("batch action is not pending")
	}
	return nil
}

func (s *Store) FinishBatchAction(ctx context.Context, batchID string, sequence int, resultJSON json.RawMessage, actionErr error) error {
	status, errorText := "completed", ""
	if actionErr != nil {
		status, errorText = "failed", actionErr.Error()
	}
	if len(resultJSON) == 0 {
		resultJSON = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ai_batch_actions SET status=?,result_json=?,error=?,finished_at=?
		WHERE batch_id=? AND sequence=?`, status, string(resultJSON), errorText, s.now().UTC().UnixNano(), batchID, sequence)
	return err
}

func (s *Store) CancelOpenBatch(ctx context.Context, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_batches SET status=?,error='cancelled by administrator',updated_at=?
		WHERE conversation_id=? AND status IN ('pending','running')`, BatchCancelled, s.now().UTC().UnixNano(), conversationID)
	return err
}

func (s *Store) RecoverInterrupted(ctx context.Context) error {
	now := s.now().UTC().UnixNano()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `UPDATE ai_turns SET status=?,error='service restarted',finished_at=?
		WHERE status IN ('queued','running')`, TurnInterrupted, now); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE ai_batches SET status=?,error='service restarted',updated_at=?
		WHERE status='running'`, BatchInterrupted, now); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE ai_batch_actions SET status='interrupted',error='service restarted',finished_at=?
		WHERE status='running'`, now); err != nil {
		return err
	}
	return transaction.Commit()
}

func randomAIID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
