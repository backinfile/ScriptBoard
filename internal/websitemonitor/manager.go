package websitemonitor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	db           *sql.DB
	options      Options
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	closed       bool
	inFlight     map[string]int64
	queued       map[string]int64
	slots        chan struct{}
	wake         chan struct{}
	maintainedAt time.Time
}

func New(db *sql.DB, options Options) (*Manager, error) {
	if db == nil {
		return nil, errors.New("website monitor database is required")
	}
	if options.Probe == nil {
		options.Probe = NetworkProbe{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = 10 * time.Second
	}
	if options.MaxConcurrency <= 0 {
		options.MaxConcurrency = 10
	}
	if options.Tick <= 0 {
		options.Tick = time.Second
	}
	if options.NginxProcesses == nil {
		options.NginxProcesses = systemNginxProcessSource{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		db: db, options: options, ctx: ctx, cancel: cancel,
		inFlight: make(map[string]int64),
		queued:   make(map[string]int64),
		slots:    make(chan struct{}, options.MaxConcurrency),
		wake:     make(chan struct{}, 1),
		// Preserve the previous contract: the first retention pass happens
		// after one Tick rather than racing Manager construction and shutdown.
		maintainedAt: options.Now().UTC().Add(-time.Hour).Add(options.Tick),
	}
	manager.wg.Add(1)
	go manager.loop()
	return manager, nil
}

func (m *Manager) Create(ctx context.Context, config Config) (Monitor, error) {
	created, err := m.createMany(ctx, []Config{config})
	if err != nil {
		return Monitor{}, err
	}
	return created[0], nil
}

// CreateMany validates and creates a group of monitor configurations in one
// transaction. It is used by bounded import flows so a failed item never
// leaves a partially imported group behind.
func (m *Manager) CreateMany(ctx context.Context, configs []Config) ([]Monitor, error) {
	return m.createMany(ctx, configs)
}

// Update replaces a monitor configuration as one atomic generation change.
// Results already in flight for an older generation are intentionally ignored.
func (m *Manager) Update(ctx context.Context, id string, config Config) (Monitor, error) {
	current, err := m.Get(ctx, id)
	if err != nil {
		return Monitor{}, err
	}
	if current.DeletedAt != nil {
		return Monitor{}, sql.ErrNoRows
	}
	if config.Source == "" {
		config.Source = current.Config.Source
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Monitor{}, err
	}
	configJSON, err := json.Marshal(normalized)
	if err != nil {
		return Monitor{}, err
	}
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Monitor{}, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE website_monitors SET
		name = ?, scope = ?, kind = ?, url = ?, config_json = ?,
		frequency_seconds = ?, timeout_seconds = ?,
		state = CASE WHEN state = 'paused' THEN 'paused' ELSE 'pending' END,
		failure_count = 0, generation = generation + 1,
		next_check_at = CASE WHEN state = 'paused' THEN 0 ELSE ? END,
		last_success = 0, last_status_code = 0, last_latency_ms = 0, last_checked_at = 0,
		last_error_category = '', last_summary = '', last_technical_error = '',
		last_certificate_json = '{}', updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
		normalized.Name, normalized.Scope, normalized.Kind, normalized.URL, string(configJSON),
		int(normalized.Frequency/time.Second), int(normalized.Timeout/time.Second),
		now.UnixNano(), now.UnixNano(), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Monitor{}, errors.New("已有同名的网站监控")
		}
		return Monitor{}, fmt.Errorf("更新网站监控: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Monitor{}, sql.ErrNoRows
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE website_incidents
		SET ended_at = ?, close_reason = 'reconfigured'
		WHERE monitor_id = ? AND ended_at IS NULL`, now.UnixNano(), id); err != nil {
		return Monitor{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Monitor{}, err
	}
	updated, err := m.Get(ctx, id)
	if err != nil {
		return Monitor{}, err
	}
	m.signalWake()
	return updated, nil
}

func (m *Manager) createMany(ctx context.Context, configs []Config) ([]Monitor, error) {
	if len(configs) == 0 {
		return nil, errors.New("没有选择要加入的网站")
	}
	normalized := make([]Config, len(configs))
	ids := make([]string, len(configs))
	configJSON := make([][]byte, len(configs))
	for index, config := range configs {
		value, err := normalizeConfig(config)
		if err != nil {
			return nil, err
		}
		normalized[index] = value
		ids[index], err = randomID()
		if err != nil {
			return nil, err
		}
		configJSON[index], err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	var enabled, sortOrder int
	if err := transaction.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE deleted_at IS NULL AND state <> 'paused'),
		COALESCE(MAX(sort_order), 0) + 1 FROM website_monitors`).Scan(&enabled, &sortOrder); err != nil {
		return nil, err
	}
	if enabled+len(configs) > 100 {
		return nil, errors.New("加入所选网站后会超过 100 个启用监控")
	}
	for index, config := range normalized {
		_, err = transaction.ExecContext(ctx, `INSERT INTO website_monitors
			(id, name, scope, kind, url, config_json, frequency_seconds, timeout_seconds, sort_order, state,
			 generation, next_check_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1, ?, ?, ?)`,
			ids[index], config.Name, config.Scope, config.Kind, config.URL, string(configJSON[index]),
			int(config.Frequency/time.Second), int(config.Timeout/time.Second), sortOrder+index,
			now.UnixNano(), now.UnixNano(), now.UnixNano())
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return nil, errors.New("已有同名的网站监控")
			}
			return nil, fmt.Errorf("保存网站监控: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	created := make([]Monitor, 0, len(ids))
	for _, id := range ids {
		monitor, err := m.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		created = append(created, monitor)
	}
	m.signalWake()
	return created, nil
}

func normalizeConfig(config Config) (Config, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" || len([]rune(config.Name)) > 80 {
		return Config{}, errors.New("名称必须是 1 到 80 个字符")
	}
	if config.Scope == "" {
		config.Scope = ScopeExternal
	}
	if config.Scope != ScopeLocal && config.Scope != ScopeExternal {
		return Config{}, errors.New("服务位置无效")
	}
	if config.Kind == "" {
		config.Kind = KindHTTP
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return Config{}, errors.New("网站 URL 无效")
	}
	config.RequestHeaders, err = normalizeRequestHeaders(config.RequestHeaders)
	if err != nil {
		return Config{}, err
	}
	if config.Kind == KindHTTP {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Config{}, errors.New("HTTP 检查必须使用 http:// 或 https:// URL")
		}
		if config.HTTPMethod == "" {
			config.HTTPMethod = http.MethodGet
		}
		if config.HTTPMethod != http.MethodGet && config.HTTPMethod != http.MethodPost {
			return Config{}, errors.New("HTTP 请求方法无效")
		}
		if config.HTTPSuccessMode == "" {
			config.HTTPSuccessMode = HTTPSuccessRange
		}
		if config.HTTPSuccessMode != HTTPSuccessRange && config.HTTPSuccessMode != HTTPSuccessExact &&
			config.HTTPSuccessMode != HTTPSuccessAnyResponse {
			return Config{}, errors.New("HTTP 成功条件无效")
		}
		if config.HTTPSuccessMode == HTTPSuccessExact {
			ranges := ExpectedHTTPStatusRanges(config)
			if len(ranges) == 0 {
				return Config{}, errors.New("指定状态码模式至少需要一个状态码")
			}
			for _, statusRange := range ranges {
				if statusRange.Start < 100 || statusRange.End > 599 || statusRange.End < statusRange.Start {
					return Config{}, errors.New("HTTP 状态码必须介于 100 和 599")
				}
			}
			if len(config.ExpectedStatusRanges) > 0 {
				config.ExpectedStatuses = nil
			}
		} else {
			config.ExpectedStatuses = nil
			config.ExpectedStatusRanges = nil
		}
		if len(config.HTTPBody) > 1024*1024 {
			return Config{}, errors.New("HTTP 请求内容不能超过 1 MiB")
		}
		if config.HTTPMethod == http.MethodPost && config.HTTPContentType == "" {
			config.HTTPContentType = "application/json"
		}
		if config.Frequency == 0 {
			config.Frequency = time.Minute
		}
	} else if config.Kind == KindWebSocket {
		if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
			return Config{}, errors.New("WebSocket 检查必须使用 ws:// 或 wss:// URL")
		}
		if config.Frequency == 0 {
			config.Frequency = 5 * time.Minute
		}
		if config.WebSocketSuccess == "" {
			config.WebSocketSuccess = WebSocketHandshake
		}
		switch config.WebSocketSuccess {
		case WebSocketHandshake:
		case WebSocketAnyMessage, WebSocketMatchingMessage:
			if config.SendType == "" {
				config.SendType = MessageNone
			}
			if config.SendType != MessageNone && config.SendType != MessageText && config.SendType != MessageBinary {
				return Config{}, errors.New("WebSocket 应用消息发送类型无效")
			}
			if config.SendType == MessageBinary {
				if _, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(config.SendPayload)); decodeErr != nil {
					return Config{}, errors.New("WebSocket 二进制发送内容必须是有效的 Base64")
				}
			}
			if config.WebSocketSuccess == WebSocketMatchingMessage {
				if config.ReceiveType != MessageText && config.ReceiveType != MessageBinary {
					return Config{}, errors.New("匹配应用消息需要选择文本帧或二进制帧")
				}
				if config.ExpectedMessage == "" {
					return Config{}, errors.New("匹配应用消息需要填写期望内容")
				}
				if config.ReceiveType == MessageBinary {
					if _, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(config.ExpectedMessage)); decodeErr != nil {
						return Config{}, errors.New("WebSocket 二进制期望内容必须是有效的 Base64")
					}
				}
			}
		case WebSocketPingPong:
			payload, decodeErr := decodePayload(config.PingPayloadFormat, config.PingPayload)
			if decodeErr != nil {
				return Config{}, errors.New("Ping 载荷与所选输入格式不匹配")
			}
			if len(payload) > 125 {
				return Config{}, errors.New("Ping 载荷解码后不能超过 125 字节")
			}
			if config.SendType != "" && config.SendType != MessageNone ||
				config.ReceiveType != "" && config.ReceiveType != MessageNone ||
				config.ExpectedMessage != "" {
				return Config{}, errors.New("Ping/Pong 控制帧不能配置应用层消息规则")
			}
		default:
			return Config{}, errors.New("WebSocket 成功条件无效")
		}
	} else {
		return Config{}, errors.New("检查方式无效")
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	return config, nil
}

// ValidateConfig applies the same defaults and validation used when a monitor
// is created, without writing anything to storage.
func ValidateConfig(config Config) (Config, error) {
	return normalizeConfig(config)
}

func (m *Manager) Get(ctx context.Context, id string) (Monitor, error) {
	var value Monitor
	var configJSON, certificateJSON string
	var lastSuccess bool
	var lastLatencyMS, createdAt, updatedAt, checkedAt, nextCheckAt int64
	var deletedAt sql.NullInt64
	err := m.db.QueryRowContext(ctx, `SELECT id, config_json, state, failure_count, sort_order, generation,
		next_check_at, last_success, last_status_code, last_latency_ms, last_checked_at, last_error_category,
		last_summary, last_technical_error, last_certificate_json, created_at, updated_at, deleted_at
		FROM website_monitors WHERE id = ?`, id).Scan(
		&value.ID, &configJSON, &value.State, &value.FailureCount, &value.SortOrder, &value.generation,
		&nextCheckAt, &lastSuccess, &value.Latest.StatusCode, &lastLatencyMS, &checkedAt, &value.Latest.ErrorCategory,
		&value.Latest.Summary, &value.Latest.TechnicalError, &certificateJSON, &createdAt, &updatedAt, &deletedAt,
	)
	if err != nil {
		return Monitor{}, err
	}
	if err := json.Unmarshal([]byte(configJSON), &value.Config); err != nil {
		return Monitor{}, fmt.Errorf("decode website monitor config: %w", err)
	}
	_ = json.Unmarshal([]byte(certificateJSON), &value.Latest.Certificate)
	value.Latest.Success = lastSuccess
	value.Latest.Latency = time.Duration(lastLatencyMS) * time.Millisecond
	if nextCheckAt != 0 {
		value.NextCheckAt = time.Unix(0, nextCheckAt).UTC()
	}
	if checkedAt != 0 {
		value.Latest.CheckedAt = time.Unix(0, checkedAt).UTC()
	}
	value.CreatedAt = time.Unix(0, createdAt).UTC()
	value.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if deletedAt.Valid {
		stamp := time.Unix(0, deletedAt.Int64).UTC()
		value.DeletedAt = &stamp
	}
	return value, nil
}

func (m *Manager) List(ctx context.Context, filter Filter) ([]Monitor, error) {
	deletedClause := "deleted_at IS NULL"
	if filter.IncludeDeleted {
		deletedClause = "deleted_at IS NOT NULL"
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM website_monitors WHERE `+deletedClause+`
		AND (? = '' OR state = ?) AND (? = '' OR scope = ?) ORDER BY sort_order, created_at`,
		filter.State, filter.State, filter.Scope, filter.Scope)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]Monitor, 0, len(ids))
	for _, id := range ids {
		value, err := m.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (m *Manager) Move(ctx context.Context, id string, direction int) error {
	if direction != -1 && direction != 1 {
		return errors.New("网站顺序移动方向无效")
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, `SELECT id, sort_order FROM website_monitors
		WHERE deleted_at IS NULL ORDER BY sort_order, created_at`)
	if err != nil {
		return err
	}
	type ordered struct {
		id    string
		order int
	}
	var values []ordered
	for rows.Next() {
		var value ordered
		if err := rows.Scan(&value.id, &value.order); err != nil {
			_ = rows.Close()
			return err
		}
		values = append(values, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	index := -1
	for candidate := range values {
		if values[candidate].id == id {
			index = candidate
			break
		}
	}
	destination := index + direction
	if index < 0 {
		return sql.ErrNoRows
	}
	if destination < 0 || destination >= len(values) {
		return nil
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE website_monitors SET sort_order = ?, updated_at = ?
		WHERE id = ?`, values[destination].order, m.options.Now().UTC().UnixNano(), values[index].id); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE website_monitors SET sort_order = ?, updated_at = ?
		WHERE id = ?`, values[index].order, m.options.Now().UTC().UnixNano(), values[destination].id); err != nil {
		return err
	}
	return transaction.Commit()
}

// Reorder persists a complete administrator-defined order. Requiring the exact
// active set prevents a stale browser from dropping or duplicating monitors.
func (m *Manager) Reorder(ctx context.Context, ids []string) error {
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, `SELECT id FROM website_monitors
		WHERE deleted_at IS NULL ORDER BY sort_order, created_at`)
	if err != nil {
		return err
	}
	active := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		active[id] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) != len(active) {
		return errors.New("网站列表已变化，请刷新后重试")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !active[id] || seen[id] {
			return errors.New("网站顺序包含无效或重复项目")
		}
		seen[id] = true
	}
	now := m.options.Now().UTC().UnixNano()
	for index, id := range ids {
		if _, err := transaction.ExecContext(ctx, `UPDATE website_monitors
			SET sort_order = ?, updated_at = ? WHERE id = ?`, index+1, now, id); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (m *Manager) Pause(ctx context.Context, id string) error {
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE website_monitors
		SET state = 'paused', failure_count = 0, generation = generation + 1,
			next_check_at = 0, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL AND state <> 'paused'`, now.UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE website_incidents SET ended_at = ?, close_reason = 'paused'
		WHERE monitor_id = ? AND ended_at IS NULL`, now.UnixNano(), id); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	m.signalWake()
	return nil
}

func (m *Manager) Resume(ctx context.Context, id string) error {
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var enabled int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM website_monitors
		WHERE deleted_at IS NULL AND state <> 'paused'`).Scan(&enabled); err != nil {
		return err
	}
	if enabled >= 100 {
		return errors.New("最多只能启用 100 个网站监控")
	}
	result, err := transaction.ExecContext(ctx, `UPDATE website_monitors
		SET state = 'pending', failure_count = 0, generation = generation + 1,
			next_check_at = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL AND state = 'paused'`,
		now.UnixNano(), now.UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	m.signalWake()
	return nil
}

func (m *Manager) CheckNow(ctx context.Context, id string) error {
	monitor, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if monitor.DeletedAt != nil {
		return sql.ErrNoRows
	}
	if monitor.State == StatePaused {
		return errors.New("监控已暂停，请先恢复后再检查")
	}
	m.startCheck(id, monitor.generation)
	return nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE website_monitors
		SET generation = generation + 1, next_check_at = 0, deleted_at = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`, now.UnixNano(), now.UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE website_incidents SET ended_at = ?, close_reason = 'deleted'
		WHERE monitor_id = ? AND ended_at IS NULL`, now.UnixNano(), id); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	m.signalWake()
	return nil
}

func (m *Manager) startCheck(id string, generation int64) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if currentGeneration, running := m.inFlight[id]; running {
		if currentGeneration != generation {
			m.queued[id] = generation
		}
		m.mu.Unlock()
		return
	}
	m.inFlight[id] = generation
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.inFlight, id)
			queuedGeneration, queued := m.queued[id]
			delete(m.queued, id)
			m.mu.Unlock()
			if queued {
				m.startCheck(id, queuedGeneration)
			}
			m.signalWake()
			m.wg.Done()
		}()
		select {
		case m.slots <- struct{}{}:
			defer func() { <-m.slots }()
		case <-m.ctx.Done():
			return
		}
		monitor, err := m.Get(m.ctx, id)
		if err != nil || monitor.generation != generation || monitor.State == StatePaused || monitor.DeletedAt != nil {
			return
		}
		ctx := m.ctx
		cancel := func() {}
		if monitor.Config.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, monitor.Config.Timeout)
		}
		result := m.options.Probe.Check(ctx, monitor.Config)
		cancel()
		m.recordResult(id, generation, result)
	}()
}

func (m *Manager) recordResult(id string, generation int64, evidence Evidence) {
	if evidence.CheckedAt.IsZero() {
		evidence.CheckedAt = m.options.Now().UTC()
	}
	certificateJSON, _ := json.Marshal(evidence.Certificate)
	transaction, err := m.db.Begin()
	if err != nil {
		return
	}
	defer transaction.Rollback()
	var current State
	var failures int
	var configJSON string
	if err := transaction.QueryRow(`SELECT state, failure_count, config_json FROM website_monitors
		WHERE id = ? AND generation = ? AND deleted_at IS NULL`, id, generation).Scan(&current, &failures, &configJSON); err != nil {
		return
	}
	var config Config
	if json.Unmarshal([]byte(configJSON), &config) != nil {
		return
	}
	nextState := StateUp
	nextFailures := 0
	nextAt := evidence.CheckedAt.Add(config.Frequency)
	if !evidence.Success {
		nextFailures = failures + 1
		nextState = StateVerifying
		nextAt = evidence.CheckedAt.Add(m.options.RetryDelay)
		if failures >= 1 || current == StateDown {
			nextState = StateDown
		}
	}
	_, err = transaction.Exec(`UPDATE website_monitors SET state = ?, failure_count = ?, next_check_at = ?,
		last_success = ?, last_status_code = ?, last_latency_ms = ?, last_checked_at = ?,
		last_error_category = ?, last_summary = ?, last_technical_error = ?, last_certificate_json = ?,
		updated_at = ? WHERE id = ? AND generation = ? AND deleted_at IS NULL`,
		nextState, nextFailures, nextAt.UnixNano(), evidence.Success, evidence.StatusCode,
		evidence.Latency.Milliseconds(), evidence.CheckedAt.UnixNano(), evidence.ErrorCategory,
		evidence.Summary, evidence.TechnicalError, string(certificateJSON), evidence.CheckedAt.UnixNano(), id, generation)
	if err != nil {
		return
	}
	if nextState == StateDown && current != StateDown {
		incidentID, randomErr := randomID()
		if randomErr != nil {
			return
		}
		incidentStartedAt := evidence.CheckedAt.UnixNano()
		var firstFailureAt int64
		firstFailureErr := transaction.QueryRow(`SELECT checked_at
			FROM website_check_results
			WHERE monitor_id = ? AND success = 0
			ORDER BY checked_at DESC LIMIT 1`, id).Scan(&firstFailureAt)
		if firstFailureErr == nil {
			incidentStartedAt = firstFailureAt
		} else if !errors.Is(firstFailureErr, sql.ErrNoRows) {
			return
		}
		if _, err := transaction.Exec(`INSERT INTO website_incidents
			(id, monitor_id, started_at, start_category, start_summary)
			VALUES (?, ?, ?, ?, ?)`,
			incidentID, id, incidentStartedAt, evidence.ErrorCategory, evidence.Summary); err != nil {
			return
		}
	}
	if evidence.Success && current == StateDown {
		if _, err := transaction.Exec(`UPDATE website_incidents SET ended_at = ?, close_reason = 'recovered'
			WHERE id = (SELECT id FROM website_incidents WHERE monitor_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1)`,
			evidence.CheckedAt.UnixNano(), id); err != nil {
			return
		}
	}
	_, err = transaction.Exec(`INSERT INTO website_check_results
		(monitor_id, checked_at, success, status_code, latency_ms, error_category, summary, technical_error, certificate_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, evidence.CheckedAt.UnixNano(), evidence.Success, evidence.StatusCode, evidence.Latency.Milliseconds(),
		evidence.ErrorCategory, evidence.Summary, evidence.TechnicalError, string(certificateJSON))
	if err != nil {
		return
	}
	bucketAt := evidence.CheckedAt.Truncate(time.Hour).UnixNano()
	successful, failed := 0, 1
	if evidence.Success {
		successful, failed = 1, 0
	}
	_, err = transaction.Exec(`INSERT INTO website_hourly_aggregates
		(monitor_id, bucket_at, total_checks, successful_checks, failed_checks,
		 average_latency_ms, maximum_latency_ms, error_counts_json)
		VALUES (?, ?, 1, ?, ?, ?, ?, '{}')
		ON CONFLICT(monitor_id, bucket_at) DO UPDATE SET
			average_latency_ms =
				((website_hourly_aggregates.average_latency_ms * website_hourly_aggregates.total_checks)
				 + excluded.average_latency_ms) / (website_hourly_aggregates.total_checks + 1),
			maximum_latency_ms = max(website_hourly_aggregates.maximum_latency_ms, excluded.maximum_latency_ms),
			total_checks = website_hourly_aggregates.total_checks + 1,
			successful_checks = website_hourly_aggregates.successful_checks + excluded.successful_checks,
			failed_checks = website_hourly_aggregates.failed_checks + excluded.failed_checks`,
		id, bucketAt, successful, failed, evidence.Latency.Milliseconds(), evidence.Latency.Milliseconds())
	if err != nil {
		return
	}
	if !evidence.Success && evidence.ErrorCategory != "" {
		var raw string
		if err := transaction.QueryRow(`SELECT error_counts_json FROM website_hourly_aggregates
			WHERE monitor_id = ? AND bucket_at = ?`, id, bucketAt).Scan(&raw); err != nil {
			return
		}
		counts := make(map[string]int)
		_ = json.Unmarshal([]byte(raw), &counts)
		counts[evidence.ErrorCategory]++
		encoded, encodeErr := json.Marshal(counts)
		if encodeErr != nil {
			return
		}
		if _, err := transaction.Exec(`UPDATE website_hourly_aggregates SET error_counts_json = ?
			WHERE monitor_id = ? AND bucket_at = ?`, string(encoded), id, bucketAt); err != nil {
			return
		}
	}
	if transaction.Commit() == nil {
		m.signalWake()
	}
}

func (m *Manager) loop() {
	defer m.wg.Done()
	initial := time.NewTimer(m.options.Tick)
	select {
	case <-m.ctx.Done():
		initial.Stop()
		return
	case <-m.wake:
		if !initial.Stop() {
			select {
			case <-initial.C:
			default:
			}
		}
	case <-initial.C:
	}
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		m.startDue()
		m.maybeMaintain()
		delay, scheduled := m.nextWakeDelay()
		var timer *time.Timer
		var timerChannel <-chan time.Time
		if scheduled {
			timer = time.NewTimer(delay)
			timerChannel = timer.C
		}
		select {
		case <-m.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-m.wake:
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timerChannel:
		}
	}
}

func (m *Manager) nextWakeDelay() (time.Duration, bool) {
	now := m.options.Now().UTC()
	m.mu.Lock()
	maintainedAt := m.maintainedAt
	inFlight := make(map[string]struct{}, len(m.inFlight))
	for id := range m.inFlight {
		inFlight[id] = struct{}{}
	}
	m.mu.Unlock()

	maintenanceDelay := time.Duration(0)
	if !maintainedAt.IsZero() {
		maintenanceDelay = maintainedAt.Add(time.Hour).Sub(now)
		if maintenanceDelay < 0 {
			maintenanceDelay = 0
		}
	}

	query := `SELECT MIN(next_check_at) FROM website_monitors
		WHERE deleted_at IS NULL AND state <> 'paused'`
	arguments := make([]any, 0, len(inFlight))
	if len(inFlight) > 0 {
		query += " AND id NOT IN (?" + strings.Repeat(",?", len(inFlight)-1) + ")"
		for id := range inFlight {
			arguments = append(arguments, id)
		}
	}
	var nextCheckAt sql.NullInt64
	// Close waits for the loop, so let this bounded local query finish. Cancelling
	// an in-flight modernc SQLite query can otherwise retain the file handle on Windows.
	if err := m.db.QueryRow(query, arguments...).Scan(&nextCheckAt); err != nil {
		return m.options.Tick, true
	}
	checkDelay := time.Duration(0)
	if nextCheckAt.Valid {
		checkDelay = time.Unix(0, nextCheckAt.Int64).UTC().Sub(now)
		if checkDelay < 0 {
			checkDelay = 0
		}
	}
	if !nextCheckAt.Valid || maintenanceDelay <= checkDelay {
		return maintenanceDelay, true
	}
	return checkDelay, true
}

func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) maybeMaintain() {
	now := m.options.Now().UTC()
	m.mu.Lock()
	if !m.maintainedAt.IsZero() && now.Sub(m.maintainedAt) < time.Hour {
		m.mu.Unlock()
		return
	}
	m.maintainedAt = now
	m.mu.Unlock()
	_ = m.Maintain(m.ctx)
}

// Availability24h returns 48 half-hour buckets backed by persisted check
// results. A failed check wins within a bucket; historical absent checks remain
// gaps. The current empty bucket may provisionally continue a fresh latest
// state without fabricating a check.
func (m *Manager) Availability24h(ctx context.Context, monitorID string) ([]AvailabilityBucket, error) {
	monitor, err := m.Get(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	if monitor.DeletedAt != nil {
		return nil, sql.ErrNoRows
	}

	const bucketSize = 30 * time.Minute
	now := m.options.Now().UTC()
	start := now.Truncate(bucketSize).Add(-47 * bucketSize)
	result := make([]AvailabilityBucket, 48)
	for index := range result {
		result[index] = AvailabilityBucket{
			StartedAt: start.Add(time.Duration(index) * bucketSize),
			State:     AvailabilityGap,
		}
	}
	rows, err := m.db.QueryContext(ctx, `SELECT checked_at, success FROM website_check_results
		WHERE monitor_id = ? AND checked_at >= ? AND checked_at <= ? ORDER BY checked_at`,
		monitorID, start.UnixNano(), now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var checkedAt int64
		var success bool
		if err := rows.Scan(&checkedAt, &success); err != nil {
			return nil, err
		}
		index := int(time.Unix(0, checkedAt).UTC().Sub(start) / bucketSize)
		if index < 0 || index >= len(result) {
			continue
		}
		bucket := &result[index]
		bucket.TotalChecks++
		if !success {
			bucket.FailedChecks++
			bucket.State = AvailabilityDown
		} else {
			bucket.SuccessfulChecks++
			if bucket.State == AvailabilityGap {
				bucket.State = AvailabilityUp
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	m.applyProvisionalAvailability(&result[len(result)-1], monitor, now)
	return result, nil
}

func (m *Manager) applyProvisionalAvailability(
	bucket *AvailabilityBucket,
	monitor Monitor,
	now time.Time,
) {
	if bucket == nil || bucket.TotalChecks != 0 ||
		monitor.State == StatePending || monitor.State == StatePaused ||
		monitor.Latest.CheckedAt.IsZero() {
		return
	}
	freshFor := monitor.Config.Frequency + monitor.Config.Timeout + m.options.Tick
	age := now.Sub(monitor.Latest.CheckedAt)
	if freshFor <= 0 || age < 0 || age > freshFor {
		return
	}
	bucket.State = AvailabilityDown
	if monitor.Latest.Success {
		bucket.State = AvailabilityUp
	}
	bucket.Provisional = true
}

// DetailSnapshot returns the complete 24-hour read model for one monitor.
// Detail bands use 72 twenty-minute buckets while the compact list continues
// to use Availability24h's 48 half-hour buckets.
func (m *Manager) DetailSnapshot(ctx context.Context, monitorID string) (DetailSnapshot, error) {
	monitor, err := m.Get(ctx, monitorID)
	if err != nil {
		return DetailSnapshot{}, err
	}
	if monitor.DeletedAt != nil {
		return DetailSnapshot{}, sql.ErrNoRows
	}

	const bucketSize = 20 * time.Minute
	now := m.options.Now().UTC()
	start := now.Truncate(bucketSize).Add(-71 * bucketSize)
	snapshot := DetailSnapshot{
		Monitor:      monitor,
		Availability: make([]AvailabilityBucket, 72),
	}
	for index := range snapshot.Availability {
		snapshot.Availability[index] = AvailabilityBucket{
			StartedAt: start.Add(time.Duration(index) * bucketSize),
			State:     AvailabilityGap,
		}
	}

	rows, err := m.db.QueryContext(ctx, `SELECT checked_at, success, status_code, latency_ms,
		error_category, summary, technical_error, certificate_json
		FROM website_check_results
		WHERE monitor_id = ? AND checked_at >= ? AND checked_at <= ?
		ORDER BY checked_at DESC`,
		monitorID, start.UnixNano(), now.UnixNano())
	if err != nil {
		return DetailSnapshot{}, err
	}
	var latencyValues []int64
	var latencyTotal int64
	for rows.Next() {
		var (
			checkedAt       int64
			latencyMS       int64
			certificateJSON string
			evidence        Evidence
		)
		if err := rows.Scan(
			&checkedAt, &evidence.Success, &evidence.StatusCode, &latencyMS,
			&evidence.ErrorCategory, &evidence.Summary, &evidence.TechnicalError,
			&certificateJSON,
		); err != nil {
			_ = rows.Close()
			return DetailSnapshot{}, err
		}
		evidence.CheckedAt = time.Unix(0, checkedAt).UTC()
		evidence.Latency = time.Duration(latencyMS) * time.Millisecond
		_ = json.Unmarshal([]byte(certificateJSON), &evidence.Certificate)
		if len(snapshot.RecentChecks) < 5 {
			snapshot.RecentChecks = append(snapshot.RecentChecks, evidence)
		}

		snapshot.TotalChecks++
		if evidence.Success {
			snapshot.SuccessfulChecks++
		} else {
			snapshot.FailedChecks++
		}
		latencyValues = append(latencyValues, latencyMS)
		latencyTotal += latencyMS

		bucketIndex := int(evidence.CheckedAt.Sub(start) / bucketSize)
		if bucketIndex < 0 || bucketIndex >= len(snapshot.Availability) {
			continue
		}
		bucket := &snapshot.Availability[bucketIndex]
		bucket.TotalChecks++
		if evidence.Success {
			bucket.SuccessfulChecks++
			if bucket.State == AvailabilityGap {
				bucket.State = AvailabilityUp
			}
		} else {
			bucket.FailedChecks++
			bucket.State = AvailabilityDown
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return DetailSnapshot{}, err
	}
	if err := rows.Close(); err != nil {
		return DetailSnapshot{}, err
	}
	m.applyProvisionalAvailability(
		&snapshot.Availability[len(snapshot.Availability)-1],
		monitor,
		now,
	)

	if snapshot.TotalChecks > 0 {
		snapshot.AvailabilityPercent =
			float64(snapshot.SuccessfulChecks) * 100 / float64(snapshot.TotalChecks)
		snapshot.AverageLatency =
			time.Duration(latencyTotal/int64(snapshot.TotalChecks)) * time.Millisecond
		slices.Sort(latencyValues)
		p95Index := (95*len(latencyValues)+99)/100 - 1
		snapshot.P95Latency = time.Duration(latencyValues[p95Index]) * time.Millisecond
	}

	snapshot.Incidents, err = m.Incidents(ctx, monitorID)
	if err != nil {
		return DetailSnapshot{}, err
	}
	for index := range snapshot.Incidents {
		incident := snapshot.Incidents[index]
		if !incident.StartedAt.Before(start) {
			snapshot.IncidentCount++
		}
		if snapshot.CurrentIncident == nil && incident.EndedAt.IsZero() {
			duration := now.Sub(incident.StartedAt)
			if duration < 0 {
				duration = 0
			}
			snapshot.CurrentIncident = &IncidentSnapshot{
				Incident:     incident,
				FailureCount: monitor.FailureCount,
				Duration:     duration,
				NextCheckAt:  monitor.NextCheckAt,
			}
		}
	}
	return snapshot, nil
}

// Maintain enforces the bounded history windows owned by this module.
func (m *Manager) Maintain(ctx context.Context) error {
	now := m.options.Now().UTC()
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	statements := []struct {
		query  string
		cutoff int64
	}{
		{`DELETE FROM website_check_results WHERE checked_at < ?`, now.Add(-24 * time.Hour).UnixNano()},
		{`DELETE FROM website_hourly_aggregates WHERE bucket_at < ?`, now.Add(-30 * 24 * time.Hour).Truncate(time.Hour).UnixNano()},
		{`DELETE FROM website_incidents WHERE ended_at IS NOT NULL AND ended_at < ?`, now.AddDate(-1, 0, 0).UnixNano()},
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement.query, statement.cutoff); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM website_monitors
		WHERE deleted_at IS NOT NULL AND deleted_at < ?
		AND NOT EXISTS (SELECT 1 FROM website_check_results WHERE monitor_id = website_monitors.id)
		AND NOT EXISTS (SELECT 1 FROM website_hourly_aggregates WHERE monitor_id = website_monitors.id)
		AND NOT EXISTS (SELECT 1 FROM website_incidents WHERE monitor_id = website_monitors.id)`,
		now.AddDate(-1, 0, 0).UnixNano()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (m *Manager) startDue() {
	// See nextWakeDelay: complete the bounded query before loop shutdown instead
	// of cancelling it while the SQLite driver is finalizing its statement.
	rows, err := m.db.Query(`SELECT id, generation FROM website_monitors
		WHERE deleted_at IS NULL AND state <> 'paused' AND next_check_at <= ?
		ORDER BY next_check_at LIMIT 100`, m.options.Now().UTC().UnixNano())
	if err != nil {
		return
	}
	type due struct {
		id         string
		generation int64
	}
	var values []due
	for rows.Next() {
		var value due
		if rows.Scan(&value.id, &value.generation) == nil {
			values = append(values, value)
		}
	}
	_ = rows.Close()
	for _, value := range values {
		m.startCheck(value.id, value.generation)
	}
}

func (m *Manager) Incidents(ctx context.Context, monitorID string) ([]Incident, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, monitor_id, started_at, ended_at,
		start_category, start_summary, close_reason FROM website_incidents
		WHERE monitor_id = ? ORDER BY started_at DESC`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Incident
	for rows.Next() {
		var value Incident
		var startedAt int64
		var endedAt sql.NullInt64
		if err := rows.Scan(&value.ID, &value.MonitorID, &startedAt, &endedAt,
			&value.Category, &value.Summary, &value.CloseReason); err != nil {
			return nil, err
		}
		value.StartedAt = time.Unix(0, startedAt).UTC()
		if endedAt.Valid {
			value.EndedAt = time.Unix(0, endedAt.Int64).UTC()
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// CurrentIncident returns live evidence for the open confirmed incident. A
// monitor in the one-failure verifying state intentionally has no incident yet.
func (m *Manager) CurrentIncident(ctx context.Context, monitorID string) (*IncidentSnapshot, error) {
	monitor, err := m.Get(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	var (
		incident  Incident
		startedAt int64
	)
	err = m.db.QueryRowContext(ctx, `SELECT id, monitor_id, started_at,
		start_category, start_summary, close_reason
		FROM website_incidents
		WHERE monitor_id = ? AND ended_at IS NULL
		ORDER BY started_at DESC LIMIT 1`, monitorID).Scan(
		&incident.ID, &incident.MonitorID, &startedAt,
		&incident.Category, &incident.Summary, &incident.CloseReason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	incident.StartedAt = time.Unix(0, startedAt).UTC()
	duration := m.options.Now().UTC().Sub(incident.StartedAt)
	if duration < 0 {
		duration = 0
	}
	return &IncidentSnapshot{
		Incident:     incident,
		FailureCount: monitor.FailureCount,
		Duration:     duration,
		NextCheckAt:  monitor.NextCheckAt,
	}, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
