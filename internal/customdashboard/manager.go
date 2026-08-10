package customdashboard

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes = 2 << 20

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS custom_dashboards (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
		is_public INTEGER NOT NULL CHECK (is_public IN (0,1)), sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS custom_dashboard_cards (
		id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
		name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','quota','key_value','website')),
		source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
		value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
		config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
		sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
		last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS custom_dashboards_order_idx ON custom_dashboards(sort_order, created_at)`,
	`CREATE INDEX IF NOT EXISTS custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at)`,
}

type CardType string

const (
	CardNumber   CardType = "number"
	CardQuota    CardType = "quota"
	CardKeyValue CardType = "key_value"
	CardWebsite  CardType = "website"
)

type DashboardInput struct {
	Name, Slug string
	Public     bool
}
type CardInput struct {
	Name                              string
	Type                              CardType
	SourceURL                         string
	Headers                           map[string]string
	ValuePath, SecondaryPath, Formula string
	Config                            json.RawMessage
	RefreshSeconds                    int
}
type Snapshot struct {
	Value     any     `json:"value,omitempty"`
	Number    float64 `json:"number,omitempty"`
	Secondary any     `json:"secondary,omitempty"`
}
type Card struct {
	ID, DashboardID, Name             string
	Type                              CardType
	SourceURL                         string
	Headers                           map[string]string
	ValuePath, SecondaryPath, Formula string
	Config                            json.RawMessage
	RefreshSeconds, SortOrder         int
	Snapshot                          Snapshot
	LastError                         string
	LastSuccessAt, LastAttemptAt      time.Time
	Stale                             bool
}
type Dashboard struct {
	ID, Name, Slug       string
	Public               bool
	SortOrder            int
	Cards                []Card
	CreatedAt, UpdatedAt time.Time
}

type Options struct {
	DB     *sql.DB
	Client *http.Client
	Now    func() time.Time
	Tick   time.Duration
	Paused bool
}
type Manager struct {
	db     *sql.DB
	client *http.Client
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	tick   time.Duration
	start  sync.Once
}

func New(options Options) (*Manager, error) {
	if options.DB == nil {
		return nil, errors.New("custom dashboard database is required")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{db: options.DB, client: options.Client, now: options.Now, ctx: ctx, cancel: cancel}
	if options.Tick <= 0 {
		options.Tick = time.Minute
	}
	m.tick = options.Tick
	if !options.Paused {
		m.Start()
	}
	return m, nil
}
func (m *Manager) Start() { m.start.Do(func() { m.wg.Add(1); go m.loop(m.tick) }) }
func (m *Manager) Close() { m.cancel(); m.wg.Wait() }

func (m *Manager) loop(tick time.Duration) {
	defer m.wg.Done()
	timer := time.NewTicker(tick)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			m.refreshDue()
		}
	}
}
func (m *Manager) refreshDue() {
	rows, err := m.db.QueryContext(m.ctx, `SELECT id FROM custom_dashboard_cards WHERE type <> 'website' AND source_url <> '' AND (last_attempt_at=0 OR last_attempt_at + refresh_seconds*1000000000 <= ?)`, m.now().UnixNano())
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
	rows.Close()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
		_, _ = m.RefreshCard(ctx, id)
		cancel()
	}
}

func (m *Manager) CreateDashboard(ctx context.Context, input DashboardInput) (Dashboard, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(strings.ToLower(input.Slug))
	if input.Name == "" {
		return Dashboard{}, errors.New("面板名称不能为空")
	}
	if !validSlug(input.Slug) {
		return Dashboard{}, errors.New("公开地址标识只能包含小写字母、数字和连字符")
	}
	id, err := randomID()
	if err != nil {
		return Dashboard{}, err
	}
	now := m.now().UTC()
	var order int
	if err = m.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0)+1 FROM custom_dashboards`).Scan(&order); err != nil {
		return Dashboard{}, err
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO custom_dashboards(id,name,slug,is_public,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, input.Name, input.Slug, boolInt(input.Public), order, now.UnixNano(), now.UnixNano())
	if err != nil {
		return Dashboard{}, friendlyUnique(err)
	}
	return m.GetDashboard(ctx, id)
}
func (m *Manager) UpdateDashboard(ctx context.Context, id string, input DashboardInput) (Dashboard, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(strings.ToLower(input.Slug))
	if input.Name == "" || !validSlug(input.Slug) {
		return Dashboard{}, errors.New("面板名称或公开地址标识无效")
	}
	result, err := m.db.ExecContext(ctx, `UPDATE custom_dashboards SET name=?,slug=?,is_public=?,updated_at=? WHERE id=?`, input.Name, input.Slug, boolInt(input.Public), m.now().UnixNano(), id)
	if err != nil {
		return Dashboard{}, friendlyUnique(err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Dashboard{}, sql.ErrNoRows
	}
	return m.GetDashboard(ctx, id)
}
func (m *Manager) DeleteDashboard(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, `DELETE FROM custom_dashboards WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (m *Manager) ListDashboards(ctx context.Context) ([]Dashboard, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,name,slug,is_public,sort_order,created_at,updated_at FROM custom_dashboards ORDER BY sort_order,created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Dashboard
	for rows.Next() {
		d, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (m *Manager) GetDashboard(ctx context.Context, id string) (Dashboard, error) {
	row := m.db.QueryRowContext(ctx, `SELECT id,name,slug,is_public,sort_order,created_at,updated_at FROM custom_dashboards WHERE id=?`, id)
	d, err := scanDashboard(row)
	if err != nil {
		return Dashboard{}, err
	}
	d.Cards, err = m.listCards(ctx, d.ID)
	return d, err
}
func (m *Manager) GetPublicDashboard(ctx context.Context, slug string) (Dashboard, error) {
	row := m.db.QueryRowContext(ctx, `SELECT id,name,slug,is_public,sort_order,created_at,updated_at FROM custom_dashboards WHERE slug=? AND is_public=1`, slug)
	d, err := scanDashboard(row)
	if err != nil {
		return Dashboard{}, err
	}
	d.Cards, err = m.listCards(ctx, d.ID)
	for i := range d.Cards {
		d.Cards[i].SourceURL = ""
		d.Cards[i].Headers = nil
		d.Cards[i].ValuePath = ""
		d.Cards[i].SecondaryPath = ""
		d.Cards[i].Formula = ""
	}
	return d, err
}

type scanner interface{ Scan(...any) error }

func scanDashboard(row scanner) (Dashboard, error) {
	var d Dashboard
	var public int
	var created, updated int64
	err := row.Scan(&d.ID, &d.Name, &d.Slug, &public, &d.SortOrder, &created, &updated)
	d.Public = public == 1
	d.CreatedAt = time.Unix(0, created).UTC()
	d.UpdatedAt = time.Unix(0, updated).UTC()
	return d, err
}

func (m *Manager) CreateCard(ctx context.Context, dashboardID string, input CardInput) (Card, error) {
	if _, err := m.GetDashboard(ctx, dashboardID); err != nil {
		return Card{}, err
	}
	if err := validateCard(&input); err != nil {
		return Card{}, err
	}
	id, err := randomID()
	if err != nil {
		return Card{}, err
	}
	now := m.now().UTC()
	var order int
	if err = m.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0)+1 FROM custom_dashboard_cards WHERE dashboard_id=?`, dashboardID).Scan(&order); err != nil {
		return Card{}, err
	}
	headers, _ := json.Marshal(input.Headers)
	config := input.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, dashboardID, input.Name, input.Type, input.SourceURL, string(headers), input.ValuePath, input.SecondaryPath, input.Formula, string(config), input.RefreshSeconds, order, now.UnixNano(), now.UnixNano())
	if err != nil {
		return Card{}, err
	}
	return m.getCard(ctx, id)
}
func (m *Manager) UpdateCard(ctx context.Context, id string, input CardInput) (Card, error) {
	if err := validateCard(&input); err != nil {
		return Card{}, err
	}
	headers, _ := json.Marshal(input.Headers)
	config := input.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	result, err := m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET name=?,type=?,source_url=?,headers_json=?,value_path=?,secondary_path=?,formula=?,config_json=?,refresh_seconds=?,updated_at=? WHERE id=?`, input.Name, input.Type, input.SourceURL, string(headers), input.ValuePath, input.SecondaryPath, input.Formula, string(config), input.RefreshSeconds, m.now().UnixNano(), id)
	if err != nil {
		return Card{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Card{}, sql.ErrNoRows
	}
	return m.getCard(ctx, id)
}
func (m *Manager) DeleteCard(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, `DELETE FROM custom_dashboard_cards WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (m *Manager) listCards(ctx context.Context, dashboardID string) ([]Card, error) {
	rows, err := m.db.QueryContext(ctx, cardSelect+` WHERE dashboard_id=? ORDER BY sort_order,created_at`, dashboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cards []Card
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, rows.Err()
}
func (m *Manager) getCard(ctx context.Context, id string) (Card, error) {
	return scanCard(m.db.QueryRowContext(ctx, cardSelect+` WHERE id=?`, id))
}

const cardSelect = `SELECT id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,snapshot_json,last_error,last_success_at,last_attempt_at FROM custom_dashboard_cards`

func scanCard(row scanner) (Card, error) {
	var c Card
	var headers, config, snapshot string
	var success, attempt int64
	if err := row.Scan(&c.ID, &c.DashboardID, &c.Name, &c.Type, &c.SourceURL, &headers, &c.ValuePath, &c.SecondaryPath, &c.Formula, &config, &c.RefreshSeconds, &c.SortOrder, &snapshot, &c.LastError, &success, &attempt); err != nil {
		return Card{}, err
	}
	c.Config = json.RawMessage(config)
	_ = json.Unmarshal([]byte(headers), &c.Headers)
	_ = json.Unmarshal([]byte(snapshot), &c.Snapshot)
	if success > 0 {
		c.LastSuccessAt = time.Unix(0, success).UTC()
	}
	if attempt > 0 {
		c.LastAttemptAt = time.Unix(0, attempt).UTC()
	}
	c.Stale = c.LastError != "" && success > 0
	return c, nil
}

func (m *Manager) RefreshCard(ctx context.Context, id string) (Card, error) {
	card, err := m.getCard(ctx, id)
	if err != nil {
		return Card{}, err
	}
	if card.Type == CardWebsite {
		return card, errors.New("网站状态卡片引用现有网站监控结果")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, card.SourceURL, nil)
	if err != nil {
		return m.recordFailure(ctx, card, err)
	}
	for name, value := range card.Headers {
		req.Header.Set(name, value)
	}
	response, err := m.client.Do(req)
	if err != nil {
		return m.recordFailure(ctx, card, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return m.recordFailure(ctx, card, fmt.Errorf("数据地址返回 HTTP %d", response.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		if err == nil {
			err = errors.New("返回数据超过 2 MiB")
		}
		return m.recordFailure(ctx, card, err)
	}
	var document any
	if err = json.Unmarshal(raw, &document); err != nil {
		return m.recordFailure(ctx, card, errors.New("返回内容不是有效 JSON"))
	}
	snapshot := Snapshot{}
	if strings.TrimSpace(card.Formula) != "" {
		snapshot.Number, err = Evaluate(card.Formula, document)
		snapshot.Value = snapshot.Number
	} else {
		snapshot.Value, err = Extract(document, card.ValuePath)
		if number, ok := asNumber(snapshot.Value); ok {
			snapshot.Number = number
		}
	}
	if err == nil && card.SecondaryPath != "" {
		snapshot.Secondary, err = Extract(document, card.SecondaryPath)
	}
	if err != nil {
		return m.recordFailure(ctx, card, err)
	}
	encoded, _ := json.Marshal(snapshot)
	now := m.now().UTC()
	_, err = m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET snapshot_json=?,last_error='',last_success_at=?,last_attempt_at=?,updated_at=? WHERE id=?`, string(encoded), now.UnixNano(), now.UnixNano(), now.UnixNano(), id)
	if err != nil {
		return card, err
	}
	return m.getCard(ctx, id)
}
func (m *Manager) recordFailure(ctx context.Context, card Card, refreshErr error) (Card, error) {
	message := strings.TrimSpace(refreshErr.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	now := m.now().UTC()
	_, _ = m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET last_error=?,last_attempt_at=?,updated_at=? WHERE id=?`, message, now.UnixNano(), now.UnixNano(), card.ID)
	updated, _ := m.getCard(ctx, card.ID)
	return updated, refreshErr
}

func validateCard(input *CardInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.ValuePath = strings.TrimSpace(input.ValuePath)
	input.SecondaryPath = strings.TrimSpace(input.SecondaryPath)
	input.Formula = strings.TrimSpace(input.Formula)
	if input.Name == "" {
		return errors.New("卡片名称不能为空")
	}
	switch input.Type {
	case CardNumber, CardQuota, CardKeyValue:
		if input.SourceURL == "" {
			return errors.New("数据地址不能为空")
		}
		parsed, err := url.ParseRequestURI(input.SourceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("数据地址必须使用 HTTP 或 HTTPS")
		}
		if input.Formula == "" && input.ValuePath == "" {
			return errors.New("请填写数值路径或公式")
		}
	case CardWebsite:
		if len(input.Config) == 0 {
			input.Config = []byte(`{"monitorIds":[]}`)
		}
	default:
		return errors.New("不支持的卡片类型")
	}
	if input.RefreshSeconds <= 0 {
		input.RefreshSeconds = 60
	}
	if input.RefreshSeconds < 15 {
		input.RefreshSeconds = 15
	}
	if input.RefreshSeconds > 86400 {
		input.RefreshSeconds = 86400
	}
	if input.Headers == nil {
		input.Headers = map[string]string{}
	}
	return nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validSlug(value string) bool {
	return len(value) >= 1 && len(value) <= 80 && slugPattern.MatchString(value)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func friendlyUnique(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique") {
		return errors.New("公开地址标识已被使用")
	}
	return err
}
func randomID() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func Extract(value any, path string) (any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return value, nil
	}
	current := value
	for len(path) > 0 {
		if path[0] == '[' {
			end := strings.IndexByte(path, ']')
			if end < 2 {
				return nil, errors.New("字段路径无效")
			}
			index, err := strconv.Atoi(path[1:end])
			if err != nil {
				return nil, errors.New("数组索引无效")
			}
			items, ok := current.([]any)
			if !ok || index < 0 || index >= len(items) {
				return nil, fmt.Errorf("找不到字段 %s", path[:end+1])
			}
			current = items[index]
			path = path[end+1:]
		} else {
			end := len(path)
			for i, ch := range path {
				if ch == '.' || ch == '[' {
					end = i
					break
				}
			}
			key := path[:end]
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s 不是对象", key)
			}
			var exists bool
			current, exists = object[key]
			if !exists {
				return nil, fmt.Errorf("找不到字段 %s", key)
			}
			path = path[end:]
		}
		path = strings.TrimPrefix(path, ".")
	}
	return current, nil
}

func Evaluate(expression string, document any) (float64, error) {
	parser := exprParser{input: expression, document: document}
	value, err := parser.expression()
	if err != nil {
		return 0, err
	}
	parser.space()
	if parser.pos != len(parser.input) {
		return 0, errors.New("公式包含无法识别的内容")
	}
	return value, nil
}

type exprParser struct {
	input    string
	pos      int
	document any
}

func (p *exprParser) space() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}
func (p *exprParser) expression() (float64, error) {
	left, err := p.term()
	for err == nil {
		p.space()
		if p.pos >= len(p.input) || (p.input[p.pos] != '+' && p.input[p.pos] != '-') {
			break
		}
		op := p.input[p.pos]
		p.pos++
		var right float64
		right, err = p.term()
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, err
}
func (p *exprParser) term() (float64, error) {
	left, err := p.factor()
	for err == nil {
		p.space()
		if p.pos >= len(p.input) || (p.input[p.pos] != '*' && p.input[p.pos] != '/') {
			break
		}
		op := p.input[p.pos]
		p.pos++
		var right float64
		right, err = p.factor()
		if op == '*' {
			left *= right
		} else if right == 0 {
			return 0, errors.New("公式不能除以零")
		} else {
			left /= right
		}
	}
	return left, err
}
func (p *exprParser) factor() (float64, error) {
	p.space()
	if p.pos >= len(p.input) {
		return 0, errors.New("公式不完整")
	}
	if p.input[p.pos] == '+' || p.input[p.pos] == '-' {
		op := p.input[p.pos]
		p.pos++
		v, err := p.factor()
		if op == '-' {
			v = -v
		}
		return v, err
	}
	if p.input[p.pos] == '(' {
		p.pos++
		v, err := p.expression()
		p.space()
		if err != nil {
			return 0, err
		}
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, errors.New("公式缺少右括号")
		}
		p.pos++
		return v, nil
	}
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if strings.ContainsRune(" +-*/()", rune(ch)) {
			break
		}
		p.pos++
	}
	token := strings.TrimSpace(p.input[start:p.pos])
	if token == "" {
		return 0, errors.New("公式无效")
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number, nil
	}
	value, err := Extract(p.document, token)
	if err != nil {
		return 0, err
	}
	number, ok := asNumber(value)
	if !ok {
		return 0, fmt.Errorf("%s 不是数值", token)
	}
	return number, nil
}
func asNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		v, err := number.Float64()
		return v, err == nil
	default:
		return 0, false
	}
}
