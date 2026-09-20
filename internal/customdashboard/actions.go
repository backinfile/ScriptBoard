package customdashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Visibility 是面板四档可见性，取代旧的 is_public 开关。
type Visibility string

const (
	VisibilityPrivate          Visibility = "private"
	VisibilityPublicRead       Visibility = "public_read"
	VisibilityPublicOperate    Visibility = "public_operate"
	VisibilityAnonymousOperate Visibility = "anonymous_operate"
)

func (v Visibility) valid() bool {
	switch v {
	case VisibilityPrivate, VisibilityPublicRead, VisibilityPublicOperate, VisibilityAnonymousOperate:
		return true
	}
	return false
}

// published 报告该档位是否允许匿名 slug 访问。
func (v Visibility) published() bool {
	return v == VisibilityPublicRead || v == VisibilityPublicOperate || v == VisibilityAnonymousOperate
}

type ActionKind string

const (
	ActionQuickRun    ActionKind = "quick_run"
	ActionBrowserHTTP ActionKind = "browser_http"
)

type ActionStyle string

const (
	ActionStyleDefault ActionStyle = "default"
	ActionStylePrimary ActionStyle = "primary"
	ActionStyleDanger  ActionStyle = "danger"
)

const (
	maxCardActions          = 8
	maxActionLabelRunes     = 40
	maxActionConfirmRunes   = 200
	maxVisitorCredentialLen = 100
)

// CardAction 是附着在卡片 config_json "actions" 数组中的操作按钮定义。
// browser_http 只允许声明访客凭据头名，禁止配置任何静态秘密值。
type CardAction struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	QuickRunID    string `json:"quickRunId,omitempty"`
	Confirm       bool   `json:"confirm"`
	ConfirmText   string `json:"confirmText,omitempty"`
	Style         string `json:"style"`
	PublicAllowed bool   `json:"publicAllowed"`
	// browser_http 专用字段。
	Method            string `json:"method,omitempty"`
	URL               string `json:"url,omitempty"`
	VisitorCredential string `json:"visitorCredential,omitempty"`
}

// validateCard 在既有卡片校验之外附加操作配置校验；actions 先从 config 中
// 取出并在校验完成后写回，避免 Registry 等类型规范化 config 时丢失。
func (m *Manager) validateCard(ctx context.Context, input *CardInput) error {
	actions, present, err := m.extractCardActions(ctx, input)
	if err != nil {
		return err
	}
	if err := validateCard(input); err != nil {
		return err
	}
	if present && len(actions) > 0 {
		var config map[string]json.RawMessage
		if err := json.Unmarshal(input.Config, &config); err != nil {
			return errors.New("卡片配置不是有效的 JSON 对象")
		}
		encoded, err := json.Marshal(actions)
		if err != nil {
			return err
		}
		config["actions"] = encoded
		input.Config, err = json.Marshal(config)
		if err != nil {
			return err
		}
	}
	// flow 卡片：对规范化配置做完整校验（DAG、限额、runId 存在性）。
	if input.Type == CardFlow {
		config, err := m.validateFlowConfig(ctx, input.Config)
		if err != nil {
			return err
		}
		input.Config = config
	}
	return nil
}

func (m *Manager) extractCardActions(ctx context.Context, input *CardInput) ([]CardAction, bool, error) {
	if len(input.Config) == 0 {
		return nil, false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(input.Config, &config); err != nil {
		return nil, false, errors.New("卡片配置不是有效的 JSON 对象")
	}
	raw, present := config["actions"]
	if !present {
		return nil, false, nil
	}
	delete(config, "actions")
	if string(raw) != "null" {
		var raws []json.RawMessage
		if err := json.Unmarshal(raw, &raws); err != nil {
			return nil, false, errors.New("卡片操作配置无效")
		}
		if len(raws) > maxCardActions {
			return nil, false, fmt.Errorf("每张卡片最多 %d 个操作", maxCardActions)
		}
		seen := map[string]bool{}
		actions := make([]CardAction, 0, len(raws))
		for _, rawAction := range raws {
			action, err := m.normalizeAction(ctx, rawAction)
			if err != nil {
				return nil, false, err
			}
			if seen[action.ID] {
				return nil, false, errors.New("卡片操作 ID 重复")
			}
			seen[action.ID] = true
			actions = append(actions, action)
		}
		if len(actions) > 0 {
			encoded, err := json.Marshal(config)
			if err != nil {
				return nil, false, err
			}
			input.Config = encoded
			return actions, true, nil
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, false, err
	}
	input.Config = encoded
	return nil, true, nil
}

func (m *Manager) normalizeAction(ctx context.Context, raw json.RawMessage) (CardAction, error) {
	var action CardAction
	// 拒绝未定义字段：browser_http 不允许配置静态请求头等秘密值。
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return CardAction{}, errors.New("卡片操作配置无效")
	}
	action.Label = strings.TrimSpace(action.Label)
	if count := utf8.RuneCountInString(action.Label); count < 1 || count > maxActionLabelRunes {
		return CardAction{}, fmt.Errorf("操作名称需为 1-%d 个字符", maxActionLabelRunes)
	}
	if action.Style == "" {
		action.Style = string(ActionStyleDefault)
	}
	switch ActionStyle(action.Style) {
	case ActionStyleDefault, ActionStylePrimary, ActionStyleDanger:
	default:
		return CardAction{}, errors.New("操作样式无效")
	}
	// 危险样式强制确认。
	if action.Style == string(ActionStyleDanger) && !action.Confirm {
		return CardAction{}, errors.New("危险操作必须开启确认")
	}
	if action.Confirm {
		action.ConfirmText = strings.TrimSpace(action.ConfirmText)
		if count := utf8.RuneCountInString(action.ConfirmText); count < 1 || count > maxActionConfirmRunes {
			return CardAction{}, fmt.Errorf("确认文案需为 1-%d 个字符", maxActionConfirmRunes)
		}
	} else {
		action.ConfirmText = ""
	}
	switch ActionKind(action.Kind) {
	case ActionQuickRun:
		action.QuickRunID = strings.TrimSpace(action.QuickRunID)
		if action.QuickRunID == "" {
			return CardAction{}, errors.New("快捷执行操作必须绑定快捷执行项")
		}
		if m.quickRunExists == nil {
			return CardAction{}, errors.New("快捷执行项存在性校验未配置")
		}
		exists, err := m.quickRunExists(ctx, action.QuickRunID)
		if err != nil {
			return CardAction{}, err
		}
		if !exists {
			return CardAction{}, errors.New("快捷执行项不存在")
		}
		action.Method, action.URL, action.VisitorCredential = "", "", ""
	case ActionBrowserHTTP:
		method := strings.ToUpper(strings.TrimSpace(action.Method))
		if method != http.MethodGet && method != http.MethodPost {
			return CardAction{}, errors.New("浏览器操作仅支持 GET 或 POST")
		}
		action.Method = method
		parsed, err := url.Parse(strings.TrimSpace(action.URL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return CardAction{}, errors.New("浏览器操作地址必须是绝对的 HTTP 或 HTTPS 地址")
		}
		action.URL = parsed.String()
		action.VisitorCredential = strings.TrimSpace(action.VisitorCredential)
		if len(action.VisitorCredential) > maxVisitorCredentialLen || strings.IndexFunc(action.VisitorCredential, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return CardAction{}, errors.New("访客凭据声明无效")
		}
		action.QuickRunID = ""
	default:
		return CardAction{}, errors.New("操作类型无效")
	}
	if action.ID == "" {
		id, err := randomID()
		if err != nil {
			return CardAction{}, err
		}
		action.ID = "act_" + id
	}
	return action, nil
}

// CardActions 读取卡片配置中的完整操作列表（管理与触发路径使用）。
func CardActions(config json.RawMessage) []CardAction {
	if len(config) == 0 {
		return nil
	}
	var parsed struct {
		Actions []CardAction `json:"actions"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return nil
	}
	return parsed.Actions
}

// FindCardAction 按 ID 返回卡片上的操作。
func FindCardAction(card Card, actionID string) (CardAction, bool) {
	for _, action := range CardActions(card.Config) {
		if action.ID == actionID {
			return action, true
		}
	}
	return CardAction{}, false
}

// ResolvePublicAction 校验公开触发的全部前提并返回可执行的服务端操作。
// 任一前提不满足都返回 sql.ErrNoRows，由路由层统一映射为 404 防枚举。
func (m *Manager) ResolvePublicAction(ctx context.Context, slug, cardID, actionID string) (Dashboard, CardAction, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	var dashboardID string
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM custom_dashboards WHERE slug=? AND visibility=?`, slug, string(VisibilityPublicOperate)).Scan(&dashboardID); err != nil {
		return Dashboard{}, CardAction{}, err
	}
	card, err := m.getCard(ctx, cardID)
	if err != nil || card.DashboardID != dashboardID {
		return Dashboard{}, CardAction{}, sql.ErrNoRows
	}
	action, ok := FindCardAction(card, actionID)
	if !ok || !action.PublicAllowed || action.Kind != string(ActionQuickRun) {
		return Dashboard{}, CardAction{}, sql.ErrNoRows
	}
	dashboard, err := m.GetDashboard(ctx, dashboardID)
	if err != nil {
		return Dashboard{}, CardAction{}, err
	}
	return dashboard, action, nil
}

// sanitizePublicActions 按可见性档位重写卡片 config 中的 actions：
// 公开只读不下发任何操作；可操作档只保留标记公开的操作的安全字段，
// quick_run 的 quickRunId 永不下发（公开触发端点按 action id 解析）。
func sanitizePublicActions(config json.RawMessage, visibility Visibility) json.RawMessage {
	if visibility == VisibilityPublicRead {
		return stripActionsKey(config)
	}
	if len(config) == 0 {
		return config
	}
	if visibility != VisibilityPublicOperate && visibility != VisibilityAnonymousOperate {
		return config
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return json.RawMessage(`{}`)
	}
	raw, present := object["actions"]
	if !present {
		return config
	}
	delete(object, "actions")
	var actions []CardAction
	if err := json.Unmarshal(raw, &actions); err == nil {
		public := make([]CardAction, 0, len(actions))
		for _, action := range actions {
			if !action.PublicAllowed {
				continue
			}
			if visibility == VisibilityAnonymousOperate && action.Kind != string(ActionBrowserHTTP) {
				continue
			}
			projected := CardAction{
				ID: action.ID, Label: action.Label, Kind: action.Kind,
				Confirm: action.Confirm, ConfirmText: action.ConfirmText, Style: action.Style,
				PublicAllowed: true,
			}
			if action.Kind == string(ActionBrowserHTTP) {
				projected.Method = action.Method
				projected.URL = action.URL
				projected.VisitorCredential = action.VisitorCredential
			}
			public = append(public, projected)
		}
		if len(public) > 0 {
			encoded, err := json.Marshal(public)
			if err == nil {
				object["actions"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func stripActionsKey(config json.RawMessage) json.RawMessage {
	if len(config) == 0 {
		return config
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return json.RawMessage(`{}`)
	}
	if _, present := object["actions"]; !present {
		return config
	}
	delete(object, "actions")
	encoded, err := json.Marshal(object)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// SetVisibility 切换面板四档可见性。升为公开可操作且尚无访问 Key 时在同一
// 事务内生成并返回明文 Key（仅此一次可见）；降为其他档位时撤销访问 Key。
func (m *Manager) SetVisibility(ctx context.Context, id string, visibility Visibility) (Dashboard, string, error) {
	if !visibility.valid() {
		return Dashboard{}, "", errors.New("面板可见性无效")
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Dashboard{}, "", err
	}
	defer transaction.Rollback()
	var current string
	var ciphertext []byte
	var hint string
	if err := transaction.QueryRowContext(ctx, `SELECT visibility, access_key_ciphertext, access_key_hint FROM custom_dashboards WHERE id=?`, id).Scan(&current, &ciphertext, &hint); err != nil {
		return Dashboard{}, "", err
	}
	plaintext := ""
	if visibility == VisibilityPublicOperate && len(ciphertext) == 0 {
		key, keyHint, sealed, err := m.sealAccessKey(id)
		if err != nil {
			return Dashboard{}, "", err
		}
		plaintext, hint, ciphertext = key, keyHint, sealed
	}
	if visibility != VisibilityPublicOperate {
		ciphertext, hint = []byte{}, ""
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE custom_dashboards SET visibility=?, is_public=?, access_key_ciphertext=?, access_key_hint=?, updated_at=? WHERE id=?`,
		string(visibility), boolInt(visibility.published()), ciphertext, hint, m.now().UnixNano(), id); err != nil {
		return Dashboard{}, "", err
	}
	if err := transaction.Commit(); err != nil {
		return Dashboard{}, "", err
	}
	if plaintext != "" || visibility != VisibilityPublicOperate {
		m.resetAccessKeyFailures(id)
	}
	dashboard, err := m.GetDashboard(ctx, id)
	return dashboard, plaintext, err
}

const (
	accessKeyBytes         = 32
	accessKeyMaxFailures   = 10
	accessKeyLockoutWindow = 15 * time.Minute
)

var ErrAccessKeyLocked = errors.New("面板访问 Key 已连续失败锁定")

type accessKeyAttempt struct {
	failures    int
	lockedUntil time.Time
}

func accessKeyPurpose(dashboardID string) string { return "dashboard-access-key-v1:" + dashboardID }

func (m *Manager) sealAccessKey(dashboardID string) (key, hint string, sealed []byte, err error) {
	raw := make([]byte, accessKeyBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	defer clear(raw)
	key = base64.RawURLEncoding.EncodeToString(raw)
	var hintBytes [3]byte
	if _, err = rand.Read(hintBytes[:]); err != nil {
		return "", "", nil, err
	}
	// 标识是独立随机值，不是 Key 片段，可安全展示与归属审计。
	hint = "dk_" + hex.EncodeToString(hintBytes[:])
	sealed, err = m.vault.Seal(accessKeyPurpose(dashboardID), raw)
	if err != nil {
		return "", "", nil, err
	}
	return key, hint, sealed, nil
}

// GenerateAccessKey 为面板生成访问 Key 并覆盖旧 Key；明文 Key 仅此一次返回。
func (m *Manager) GenerateAccessKey(ctx context.Context, id string) (key, hint string, err error) {
	key, hint, sealed, err := m.sealAccessKey(id)
	if err != nil {
		return "", "", err
	}
	result, err := m.db.ExecContext(ctx, `UPDATE custom_dashboards SET access_key_ciphertext=?, access_key_hint=?, updated_at=? WHERE id=?`, sealed, hint, m.now().UnixNano(), id)
	if err != nil {
		return "", "", err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return "", "", sql.ErrNoRows
	}
	m.resetAccessKeyFailures(id)
	return key, hint, nil
}

// RotateAccessKey 轮换访问 Key：旧 Key 立即失效。
func (m *Manager) RotateAccessKey(ctx context.Context, id string) (key, hint string, err error) {
	return m.GenerateAccessKey(ctx, id)
}

// RevokeAccessKey 撤销访问 Key 并清空密封密文。
func (m *Manager) RevokeAccessKey(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, `UPDATE custom_dashboards SET access_key_ciphertext=X'', access_key_hint='', updated_at=? WHERE id=?`, m.now().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	m.resetAccessKeyFailures(id)
	return nil
}

// VerifyAccessKey 恒定时间校验面板访问 Key；连续失败达到上限后锁定一段时间
// （内存计数，重启清零）。解封失败一律视为校验失败。
func (m *Manager) VerifyAccessKey(ctx context.Context, id, key string) (bool, error) {
	if locked, until := m.accessKeyLocked(id); locked {
		return false, fmt.Errorf("%w（%s 后重试）", ErrAccessKeyLocked, until.UTC().Format(time.RFC3339))
	}
	var ciphertext []byte
	if err := m.db.QueryRowContext(ctx, `SELECT access_key_ciphertext FROM custom_dashboards WHERE id=?`, id).Scan(&ciphertext); err != nil {
		return false, err
	}
	matched := false
	if len(ciphertext) > 0 {
		if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key)); err == nil && len(decoded) == accessKeyBytes {
			if plain, err := m.vault.Unseal(accessKeyPurpose(id), ciphertext); err == nil {
				matched = subtle.ConstantTimeCompare(decoded, plain) == 1
				clear(plain)
			}
		}
	}
	if matched {
		m.resetAccessKeyFailures(id)
		return true, nil
	}
	m.recordAccessKeyFailure(id)
	return false, nil
}

func (m *Manager) accessKeyLocked(id string) (bool, time.Time) {
	m.accessKeyMu.Lock()
	defer m.accessKeyMu.Unlock()
	attempt, ok := m.accessKeyFailures[id]
	if !ok || attempt.lockedUntil.IsZero() {
		return false, time.Time{}
	}
	if !m.now().Before(attempt.lockedUntil) {
		delete(m.accessKeyFailures, id)
		return false, time.Time{}
	}
	return true, attempt.lockedUntil
}

func (m *Manager) recordAccessKeyFailure(id string) {
	m.accessKeyMu.Lock()
	defer m.accessKeyMu.Unlock()
	attempt := m.accessKeyFailures[id]
	if attempt == nil {
		attempt = &accessKeyAttempt{}
		m.accessKeyFailures[id] = attempt
	}
	attempt.failures++
	if attempt.failures >= accessKeyMaxFailures {
		attempt.failures = 0
		attempt.lockedUntil = m.now().Add(accessKeyLockoutWindow)
	}
}

func (m *Manager) resetAccessKeyFailures(id string) {
	m.accessKeyMu.Lock()
	defer m.accessKeyMu.Unlock()
	delete(m.accessKeyFailures, id)
}
