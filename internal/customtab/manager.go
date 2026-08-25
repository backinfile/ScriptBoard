// Package customtab owns instance-defined browser page references and their
// optional target credentials.
package customtab

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/secretstore"
)

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS custom_tabs (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		target_url TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
		credential_mode TEXT NOT NULL DEFAULT 'isolated' CHECK (credential_mode IN ('isolated','target_state','key')),
		visibility_roles TEXT NOT NULL DEFAULT 'administrator,maintainer,operator,viewer',
		key_name TEXT NOT NULL DEFAULT '',
		key_ciphertext BLOB NOT NULL DEFAULT X'',
		sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS custom_tabs_order_idx ON custom_tabs(sort_order, created_at)`,
}

type CredentialMode string

const (
	ModeIsolated    CredentialMode = "isolated"
	ModeTargetState CredentialMode = "target_state"
	ModeKey         CredentialMode = "key"
)

type Tab struct {
	ID, Name, TargetURL, Origin, KeyName string
	CredentialMode                       CredentialMode
	VisibilityRoles                      []string
	Enabled, KeyConfigured               bool
	SortOrder                            int
	CreatedAt, UpdatedAt                 time.Time
}

type Input struct {
	Name, TargetURL, KeyName, Key string
	CredentialMode                CredentialMode
	VisibilityRoles               []string
	Enabled, PreserveKey          bool
}

type Options struct {
	DB          *sql.DB
	SecretStore *secretstore.Store
	Now         func() time.Time
}

type Manager struct {
	db    *sql.DB
	vault *secretstore.Store
	now   func() time.Time
}

func New(options Options) (*Manager, error) {
	if options.DB == nil || options.SecretStore == nil {
		return nil, errors.New("custom tabs require a database and credential store")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Manager{db: options.DB, vault: options.SecretStore, now: options.Now}, nil
}

func (m *Manager) Create(ctx context.Context, input Input) (Tab, error) {
	input, origin, err := validateInput(input, false)
	if err != nil {
		return Tab{}, err
	}
	id, err := randomID(18)
	if err != nil {
		return Tab{}, err
	}
	ciphertext, err := m.sealKey(id, origin, input)
	if err != nil {
		return Tab{}, err
	}
	var order int
	if err := m.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0)+1 FROM custom_tabs`).Scan(&order); err != nil {
		return Tab{}, err
	}
	now := m.now().UTC().UnixNano()
	_, err = m.db.ExecContext(ctx, `INSERT INTO custom_tabs(id,name,target_url,enabled,credential_mode,visibility_roles,key_name,key_ciphertext,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, input.Name, input.TargetURL, boolInt(input.Enabled), input.CredentialMode, strings.Join(input.VisibilityRoles, ","), input.KeyName, ciphertext, order, now, now)
	if err != nil {
		return Tab{}, err
	}
	return Tab{ID: id, Name: input.Name, TargetURL: input.TargetURL, Origin: origin, CredentialMode: input.CredentialMode, VisibilityRoles: input.VisibilityRoles, KeyName: input.KeyName, Enabled: input.Enabled, KeyConfigured: len(ciphertext) > 0, SortOrder: order, CreatedAt: time.Unix(0, now).UTC(), UpdatedAt: time.Unix(0, now).UTC()}, nil
}

func (m *Manager) Update(ctx context.Context, id string, input Input) (Tab, error) {
	id = strings.TrimSpace(id)
	current, existingCiphertext, err := m.get(ctx, id)
	if err != nil {
		return Tab{}, err
	}
	input, origin, err := validateInput(input, input.PreserveKey && current.KeyConfigured)
	if err != nil {
		return Tab{}, err
	}
	if input.CredentialMode == ModeKey && input.PreserveKey && current.Origin != origin {
		return Tab{}, errors.New("更改目标 Origin 时必须重新输入 Key")
	}
	ciphertext := []byte(nil)
	if input.CredentialMode == ModeKey && input.PreserveKey {
		ciphertext = existingCiphertext
	} else {
		ciphertext, err = m.sealKey(id, origin, input)
		if err != nil {
			return Tab{}, err
		}
	}
	now := m.now().UTC().UnixNano()
	result, err := m.db.ExecContext(ctx, `UPDATE custom_tabs SET name=?,target_url=?,enabled=?,credential_mode=?,visibility_roles=?,key_name=?,key_ciphertext=?,updated_at=? WHERE id=?`, input.Name, input.TargetURL, boolInt(input.Enabled), input.CredentialMode, strings.Join(input.VisibilityRoles, ","), input.KeyName, ciphertext, now, id)
	if err != nil {
		return Tab{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Tab{}, sql.ErrNoRows
	}
	return m.Get(ctx, id)
}

func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) (Tab, error) {
	result, err := m.db.ExecContext(ctx, `UPDATE custom_tabs SET enabled=?,updated_at=? WHERE id=?`, boolInt(enabled), m.now().UTC().UnixNano(), strings.TrimSpace(id))
	if err != nil {
		return Tab{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Tab{}, sql.ErrNoRows
	}
	return m.Get(ctx, id)
}

func (m *Manager) List(ctx context.Context) ([]Tab, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,name,target_url,enabled,credential_mode,visibility_roles,key_name,LENGTH(key_ciphertext),sort_order,created_at,updated_at FROM custom_tabs ORDER BY sort_order,created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tabs []Tab
	for rows.Next() {
		var tab Tab
		var enabled, keyLength int
		var created, updated int64
		var visibility string
		if err := rows.Scan(&tab.ID, &tab.Name, &tab.TargetURL, &enabled, &tab.CredentialMode, &visibility, &tab.KeyName, &keyLength, &tab.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		tab.Enabled, tab.KeyConfigured = enabled == 1, keyLength > 0
		tab.VisibilityRoles = strings.Split(visibility, ",")
		tab.Origin, _ = targetOrigin(tab.TargetURL)
		tab.CreatedAt, tab.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
		tabs = append(tabs, tab)
	}
	return tabs, rows.Err()
}

func (m *Manager) Get(ctx context.Context, id string) (Tab, error) {
	tab, _, err := m.get(ctx, strings.TrimSpace(id))
	return tab, err
}

func (m *Manager) get(ctx context.Context, id string) (Tab, []byte, error) {
	var tab Tab
	var enabled int
	var ciphertext []byte
	var created, updated int64
	var visibility string
	err := m.db.QueryRowContext(ctx, `SELECT id,name,target_url,enabled,credential_mode,visibility_roles,key_name,key_ciphertext,sort_order,created_at,updated_at FROM custom_tabs WHERE id=?`, id).Scan(&tab.ID, &tab.Name, &tab.TargetURL, &enabled, &tab.CredentialMode, &visibility, &tab.KeyName, &ciphertext, &tab.SortOrder, &created, &updated)
	if err != nil {
		return Tab{}, nil, err
	}
	tab.Enabled, tab.KeyConfigured = enabled == 1, len(ciphertext) > 0
	tab.VisibilityRoles = strings.Split(visibility, ",")
	tab.Origin, _ = targetOrigin(tab.TargetURL)
	tab.CreatedAt, tab.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
	return tab, ciphertext, nil
}

func (m *Manager) Credential(ctx context.Context, id string) (string, string, error) {
	tab, ciphertext, err := m.get(ctx, strings.TrimSpace(id))
	if err != nil {
		return "", "", err
	}
	if tab.CredentialMode != ModeKey || len(ciphertext) == 0 {
		return "", "", errors.New("页签 Key 未配置")
	}
	plain, err := m.vault.Unseal(keyPurpose(tab.ID, tab.Origin), ciphertext)
	if err != nil {
		return "", "", err
	}
	defer clear(plain)
	return tab.KeyName, string(plain), nil
}

func (m *Manager) Move(ctx context.Context, id string, direction int) (Tab, error) {
	if direction != -1 && direction != 1 {
		return Tab{}, errors.New("无效的排序方向")
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Tab{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var order int
	if err := tx.QueryRowContext(ctx, `SELECT sort_order FROM custom_tabs WHERE id=?`, strings.TrimSpace(id)).Scan(&order); err != nil {
		return Tab{}, err
	}
	query := `SELECT id,sort_order FROM custom_tabs WHERE sort_order < ? ORDER BY sort_order DESC,created_at DESC LIMIT 1`
	if direction > 0 {
		query = `SELECT id,sort_order FROM custom_tabs WHERE sort_order > ? ORDER BY sort_order,created_at LIMIT 1`
	}
	var otherID string
	var otherOrder int
	if err := tx.QueryRowContext(ctx, query, order).Scan(&otherID, &otherOrder); errors.Is(err, sql.ErrNoRows) {
		return m.Get(ctx, id)
	} else if err != nil {
		return Tab{}, err
	}
	now := m.now().UTC().UnixNano()
	if _, err := tx.ExecContext(ctx, `UPDATE custom_tabs SET sort_order=?,updated_at=? WHERE id=?`, otherOrder, now, id); err != nil {
		return Tab{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE custom_tabs SET sort_order=?,updated_at=? WHERE id=?`, order, now, otherID); err != nil {
		return Tab{}, err
	}
	if err := tx.Commit(); err != nil {
		return Tab{}, err
	}
	return m.Get(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, `DELETE FROM custom_tabs WHERE id=?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (tab Tab) VisibleTo(role string) bool {
	for _, allowed := range tab.VisibilityRoles {
		if allowed == role {
			return true
		}
	}
	return false
}

func (m *Manager) sealKey(id, origin string, input Input) ([]byte, error) {
	if input.CredentialMode != ModeKey {
		return []byte{}, nil
	}
	return m.vault.Seal(keyPurpose(id, origin), []byte(input.Key))
}

func validateInput(input Input, existingKey bool) (Input, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.TargetURL = strings.TrimSpace(input.TargetURL)
	input.KeyName = strings.TrimSpace(input.KeyName)
	roles, err := normalizeVisibilityRoles(input.VisibilityRoles)
	if err != nil {
		return Input{}, "", err
	}
	input.VisibilityRoles = roles
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || len(input.TargetURL) > 2048 {
		return Input{}, "", errors.New("页签名称或地址无效")
	}
	origin, err := targetOrigin(input.TargetURL)
	if err != nil {
		return Input{}, "", err
	}
	switch input.CredentialMode {
	case ModeIsolated, ModeTargetState:
		input.KeyName, input.Key, input.PreserveKey = "", "", false
	case ModeKey:
		if input.KeyName == "" || len(input.KeyName) > 64 || strings.IndexFunc(input.KeyName, func(r rune) bool {
			return !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
		}) >= 0 {
			return Input{}, "", errors.New("Key 名称无效")
		}
		if !input.PreserveKey || !existingKey {
			if input.Key == "" || len(input.Key) > 4096 || !utf8.ValidString(input.Key) {
				return Input{}, "", errors.New("Key 无效")
			}
		}
	default:
		return Input{}, "", errors.New("凭据模式无效")
	}
	return input, origin, nil
}

func normalizeVisibilityRoles(roles []string) ([]string, error) {
	canonical := []string{"administrator", "maintainer", "operator", "viewer"}
	if roles == nil {
		return append([]string(nil), canonical...), nil
	}
	selected := make(map[string]bool, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		switch role {
		case "administrator", "maintainer", "operator", "viewer":
			selected[role] = true
		default:
			return nil, errors.New("可见权限无效")
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("至少选择一种可见权限")
	}
	result := make([]string, 0, len(selected))
	for _, role := range canonical {
		if selected[role] {
			result = append(result, role)
		}
	}
	return result, nil
}

func targetOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New("只支持不含用户信息和片段的绝对 HTTP/HTTPS 地址")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func keyPurpose(id, origin string) string { return "custom-tab-key-v1:" + id + ":" + origin }
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func randomID(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
