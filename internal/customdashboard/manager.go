package customdashboard

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/outboundpolicy"
	"scriptboard/internal/registrymonitor"
	"scriptboard/internal/secretredaction"
)

const (
	maxResponseBytes     = 2 << 20
	maxSourceHeaders     = 32
	maxSourceHeaderBytes = 16 << 10
)

var ErrCredentialUnavailable = errors.New("Registry 凭据未配置")

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS custom_dashboards (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
		is_public INTEGER NOT NULL CHECK (is_public IN (0,1)),
		show_as_tab INTEGER NOT NULL DEFAULT 0 CHECK (show_as_tab IN (0,1)), sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS custom_dashboard_cards (
		id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
		name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website','registry')),
		source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
		value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
		config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
		sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
		last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS custom_dashboards_order_idx ON custom_dashboards(sort_order, created_at)`,
	`CREATE INDEX IF NOT EXISTS custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at)`,
	`CREATE TABLE IF NOT EXISTS custom_dashboard_registry_operations (
		operation_id TEXT PRIMARY KEY, card_id TEXT NOT NULL,
		phase TEXT NOT NULL DEFAULT 'prepared' CHECK(phase IN ('prepared','committed')),
		created_at INTEGER NOT NULL
	)`,
}

type CardType string

const (
	CardNumber     CardType = "number"
	CardPercentage CardType = "percentage"
	CardQuota      CardType = "quota"
	CardKeyValue   CardType = "key_value"
	CardWebsite    CardType = "website"
	CardRegistry   CardType = "registry"
)

type DashboardInput struct {
	Name, Slug        string
	Public, ShowAsTab bool
}
type CardInput struct {
	Name                              string
	Type                              CardType
	SourceURL                         string
	Headers                           map[string]string
	ValuePath, SecondaryPath, Formula string
	Config                            json.RawMessage
	RefreshSeconds                    int
	RegistryPassword                  string
	PreserveRegistryPassword          bool
}
type Snapshot struct {
	Value      any                           `json:"value,omitempty"`
	Number     float64                       `json:"number,omitempty"`
	Secondary  any                           `json:"secondary,omitempty"`
	Images     []registrymonitor.ImageResult `json:"images,omitempty"`
	Diagnostic *RequestDiagnostic            `json:"diagnostic,omitempty"`
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
	CredentialConfigured              bool
}
type Dashboard struct {
	ID, Name, Slug       string
	Public, ShowAsTab    bool
	SortOrder            int
	Cards                []Card
	CreatedAt, UpdatedAt time.Time
}

type Options struct {
	DB                  *sql.DB
	Client              *http.Client
	RegistryConnections RegistryConnections
	Now                 func() time.Time
	Tick                time.Duration
	Paused              bool
}

type RegistryConnections interface {
	Prepare(context.Context, string, string, registrymonitor.Config, string, bool) error
	PrepareDelete(context.Context, string, string) error
	Commit(context.Context, string) error
	Acknowledge(context.Context, string) error
	Abort(context.Context, string) error
	Configured(context.Context, string) (bool, error)
	Inspect(context.Context, string) ([]registrymonitor.ImageResult, error)
	Test(context.Context, string, registrymonitor.Config, string, bool) ([]registrymonitor.ImageResult, error)
	InsecureConfigured(context.Context, string) (bool, error)
	RegisterInsecure(context.Context, string) (bool, error)
}

type Manager struct {
	db                 *sql.DB
	client             *http.Client
	registry           RegistryConnections
	now                func() time.Time
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	tick               time.Duration
	start              sync.Once
	registryMutationMu sync.Mutex
}

func New(options Options) (*Manager, error) {
	if options.DB == nil {
		return nil, errors.New("custom dashboard database is required")
	}
	if options.RegistryConnections == nil {
		return nil, errors.New("custom dashboard Registry connection module is required")
	}
	if options.Client == nil {
		options.Client = &http.Client{
			Timeout:   15 * time.Second,
			Transport: outboundpolicy.Policy{}.Transport(),
		}
	}
	client := *options.Client
	if client.Timeout <= 0 {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("dashboard source redirects are not allowed")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{db: options.DB, client: &client, registry: options.RegistryConnections, now: options.Now, ctx: ctx, cancel: cancel}
	if options.Tick <= 0 {
		options.Tick = time.Minute
	}
	m.tick = options.Tick
	if err := m.ReconcileRegistryOperations(context.Background()); err != nil {
		cancel()
		return nil, fmt.Errorf("reconcile Registry connections: %w", err)
	}
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
	rows, err := m.db.QueryContext(m.ctx, `SELECT id FROM custom_dashboard_cards WHERE type <> 'website' AND (type='registry' OR source_url <> '') AND (last_attempt_at=0 OR last_attempt_at + refresh_seconds*1000000000 <= ?)`, m.now().UnixNano())
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
	_, err = m.db.ExecContext(ctx, `INSERT INTO custom_dashboards(id,name,slug,is_public,show_as_tab,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, input.Name, input.Slug, boolInt(input.Public), boolInt(input.ShowAsTab), order, now.UnixNano(), now.UnixNano())
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
	result, err := m.db.ExecContext(ctx, `UPDATE custom_dashboards SET name=?,slug=?,is_public=?,show_as_tab=?,updated_at=? WHERE id=?`, input.Name, input.Slug, boolInt(input.Public), boolInt(input.ShowAsTab), m.now().UnixNano(), id)
	if err != nil {
		return Dashboard{}, friendlyUnique(err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Dashboard{}, sql.ErrNoRows
	}
	return m.GetDashboard(ctx, id)
}
func (m *Manager) DeleteDashboard(ctx context.Context, id string) error {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM custom_dashboard_cards WHERE dashboard_id=? AND type='registry'`, id)
	if err != nil {
		return err
	}
	var operations []registryOperation
	for rows.Next() {
		var cardID string
		if rows.Scan(&cardID) == nil {
			operation, prepareErr := m.prepareRegistryDelete(ctx, cardID)
			if prepareErr != nil {
				_ = rows.Close()
				m.abortRegistryOperations(operations)
				return prepareErr
			}
			operations = append(operations, operation)
		}
	}
	if err := rows.Close(); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `DELETE FROM custom_dashboards WHERE id=?`, id)
	if err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		m.abortRegistryOperations(operations)
		return sql.ErrNoRows
	}
	if err := m.recordRegistryOperations(ctx, transaction, operations); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	if err := transaction.Commit(); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	return m.completeRegistryOperations(ctx, operations)
}
func (m *Manager) ListDashboards(ctx context.Context) ([]Dashboard, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,name,slug,is_public,show_as_tab,sort_order,created_at,updated_at FROM custom_dashboards ORDER BY sort_order,created_at`)
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
	row := m.db.QueryRowContext(ctx, `SELECT id,name,slug,is_public,show_as_tab,sort_order,created_at,updated_at FROM custom_dashboards WHERE id=?`, id)
	d, err := scanDashboard(row)
	if err != nil {
		return Dashboard{}, err
	}
	d.Cards, err = m.listCards(ctx, d.ID)
	return d, err
}
func (m *Manager) GetPublicDashboard(ctx context.Context, slug string) (Dashboard, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	row := m.db.QueryRowContext(ctx, `SELECT id,name,slug,is_public,show_as_tab,sort_order,created_at,updated_at FROM custom_dashboards WHERE slug=? AND is_public=1`, slug)
	d, err := scanDashboard(row)
	if err != nil {
		return Dashboard{}, err
	}
	d.Cards, err = m.listCards(ctx, d.ID)
	for i := range d.Cards {
		d.Cards[i].Snapshot.Diagnostic = nil
		d.Cards[i].SourceURL = ""
		d.Cards[i].Headers = nil
		d.Cards[i].ValuePath = ""
		d.Cards[i].SecondaryPath = ""
		d.Cards[i].Formula = ""
		if d.Cards[i].Type == CardRegistry {
			d.Cards[i].Config = json.RawMessage(`{}`)
			d.Cards[i].CredentialConfigured = false
		}
	}
	return d, err
}

type scanner interface{ Scan(...any) error }

func scanDashboard(row scanner) (Dashboard, error) {
	var d Dashboard
	var public, showAsTab int
	var created, updated int64
	err := row.Scan(&d.ID, &d.Name, &d.Slug, &public, &showAsTab, &d.SortOrder, &created, &updated)
	d.Public = public == 1
	d.ShowAsTab = showAsTab == 1
	d.CreatedAt = time.Unix(0, created).UTC()
	d.UpdatedAt = time.Unix(0, updated).UTC()
	return d, err
}

func (m *Manager) CreateCard(ctx context.Context, dashboardID string, input CardInput) (Card, error) {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
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
	var operations []registryOperation
	if input.Type == CardRegistry {
		var registryConfig registrymonitor.Config
		_ = json.Unmarshal(input.Config, &registryConfig)
		if registryConfig.AuthMode == "anonymous" || input.RegistryPassword != "" {
			operation, prepareErr := m.prepareRegistryConnection(ctx, id, input)
			if prepareErr != nil {
				return Card{}, prepareErr
			}
			operations = append(operations, operation)
		}
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	defer transaction.Rollback()
	now := m.now().UTC()
	var order int
	if err = transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0)+1 FROM custom_dashboard_cards WHERE dashboard_id=?`, dashboardID).Scan(&order); err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	headers, _ := json.Marshal(input.Headers)
	config := input.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, dashboardID, input.Name, input.Type, input.SourceURL, string(headers), input.ValuePath, input.SecondaryPath, input.Formula, string(config), input.RefreshSeconds, order, now.UnixNano(), now.UnixNano())
	if err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if err := m.recordRegistryOperations(ctx, transaction, operations); err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if err := transaction.Commit(); err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if err := m.completeRegistryOperations(ctx, operations); err != nil {
		return Card{}, err
	}
	return m.getCard(ctx, id)
}

func (m *Manager) ImportCards(ctx context.Context, dashboardID string, inputs []CardInput) error {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
	if _, err := m.GetDashboard(ctx, dashboardID); err != nil {
		return err
	}
	if len(inputs) == 0 {
		return errors.New("at least one card is required")
	}
	for index := range inputs {
		if err := validateCard(&inputs[index]); err != nil {
			return err
		}
	}
	ids := make([]string, len(inputs))
	var operations []registryOperation
	for index := range ids {
		id, err := randomID()
		if err != nil {
			m.abortRegistryOperations(operations)
			return err
		}
		ids[index] = id
		if inputs[index].Type == CardRegistry {
			var config registrymonitor.Config
			_ = json.Unmarshal(inputs[index].Config, &config)
			if config.AuthMode == "anonymous" {
				operation, prepareErr := m.prepareRegistryConnection(ctx, id, inputs[index])
				if prepareErr != nil {
					m.abortRegistryOperations(operations)
					return prepareErr
				}
				operations = append(operations, operation)
			}
		}
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	defer transaction.Rollback()
	var order int
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0) FROM custom_dashboard_cards WHERE dashboard_id=?`, dashboardID).Scan(&order); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	now := m.now().UTC().UnixNano()
	for index, input := range inputs {
		headers, _ := json.Marshal(input.Headers)
		config := input.Config
		if len(config) == 0 {
			config = []byte(`{}`)
		}
		order++
		if _, err := transaction.ExecContext(ctx, `INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,source_url,headers_json,value_path,secondary_path,formula,config_json,refresh_seconds,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, ids[index], dashboardID, input.Name, input.Type, input.SourceURL, string(headers), input.ValuePath, input.SecondaryPath, input.Formula, string(config), input.RefreshSeconds, order, now, now); err != nil {
			m.abortRegistryOperations(operations)
			return err
		}
	}
	if err := m.recordRegistryOperations(ctx, transaction, operations); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	if err := transaction.Commit(); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	return m.completeRegistryOperations(ctx, operations)
}
func (m *Manager) UpdateCard(ctx context.Context, id string, input CardInput) (Card, error) {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
	if err := validateCard(&input); err != nil {
		return Card{}, err
	}
	current, err := m.getCard(ctx, id)
	if err != nil {
		return Card{}, err
	}
	var operations []registryOperation
	if input.Type == CardRegistry {
		var registryConfig registrymonitor.Config
		_ = json.Unmarshal(input.Config, &registryConfig)
		if registryConfig.AuthMode == "basic" && input.RegistryPassword == "" && !input.PreserveRegistryPassword {
			operation, prepareErr := m.prepareRegistryDelete(ctx, id)
			if prepareErr != nil {
				return Card{}, prepareErr
			}
			operations = append(operations, operation)
		} else {
			operation, prepareErr := m.prepareRegistryConnection(ctx, id, input)
			if prepareErr != nil {
				return Card{}, prepareErr
			}
			operations = append(operations, operation)
		}
	} else if current.Type == CardRegistry {
		operation, prepareErr := m.prepareRegistryDelete(ctx, id)
		if prepareErr != nil {
			return Card{}, prepareErr
		}
		operations = append(operations, operation)
	}
	headers, _ := json.Marshal(input.Headers)
	config := input.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE custom_dashboard_cards SET name=?,type=?,source_url=?,headers_json=?,value_path=?,secondary_path=?,formula=?,config_json=?,refresh_seconds=?,updated_at=? WHERE id=?`, input.Name, input.Type, input.SourceURL, string(headers), input.ValuePath, input.SecondaryPath, input.Formula, string(config), input.RefreshSeconds, m.now().UnixNano(), id)
	if err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		m.abortRegistryOperations(operations)
		return Card{}, sql.ErrNoRows
	}
	if err := m.recordRegistryOperations(ctx, transaction, operations); err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if err := transaction.Commit(); err != nil {
		m.abortRegistryOperations(operations)
		return Card{}, err
	}
	if err := m.completeRegistryOperations(ctx, operations); err != nil {
		return Card{}, err
	}
	return m.getCard(ctx, id)
}
func (m *Manager) DeleteCard(ctx context.Context, id string) error {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
	card, err := m.getCard(ctx, id)
	if err != nil {
		return err
	}
	var operations []registryOperation
	if card.Type == CardRegistry {
		operation, prepareErr := m.prepareRegistryDelete(ctx, id)
		if prepareErr != nil {
			return prepareErr
		}
		operations = append(operations, operation)
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `DELETE FROM custom_dashboard_cards WHERE id=?`, id)
	if err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		m.abortRegistryOperations(operations)
		return sql.ErrNoRows
	}
	if err := m.recordRegistryOperations(ctx, transaction, operations); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	if err := transaction.Commit(); err != nil {
		m.abortRegistryOperations(operations)
		return err
	}
	return m.completeRegistryOperations(ctx, operations)
}

func (m *Manager) MoveCard(ctx context.Context, id string, direction int) (string, error) {
	if direction != -1 && direction != 1 {
		return "", errors.New("卡片顺序移动方向无效")
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	var dashboardID string
	if err := transaction.QueryRowContext(ctx, `SELECT dashboard_id FROM custom_dashboard_cards WHERE id=?`, id).Scan(&dashboardID); err != nil {
		return "", err
	}
	rows, err := transaction.QueryContext(ctx, `SELECT id,sort_order FROM custom_dashboard_cards WHERE dashboard_id=? ORDER BY sort_order,created_at`, dashboardID)
	if err != nil {
		return "", err
	}
	type orderedCard struct {
		id    string
		order int
	}
	var cards []orderedCard
	for rows.Next() {
		var card orderedCard
		if err := rows.Scan(&card.id, &card.order); err != nil {
			_ = rows.Close()
			return "", err
		}
		cards = append(cards, card)
	}
	if err := rows.Close(); err != nil {
		return "", err
	}
	index := -1
	for candidate := range cards {
		if cards[candidate].id == id {
			index = candidate
			break
		}
	}
	destination := index + direction
	if index < 0 {
		return "", sql.ErrNoRows
	}
	if destination < 0 || destination >= len(cards) {
		return dashboardID, nil
	}
	// Reassign visible positions instead of swapping stored values: legacy or
	// imported cards can share a sort_order, making an equal-value swap a no-op.
	cards[index], cards[destination] = cards[destination], cards[index]
	now := m.now().UTC().UnixNano()
	for position, card := range cards {
		if _, err := transaction.ExecContext(ctx, `UPDATE custom_dashboard_cards SET sort_order=?,updated_at=? WHERE id=?`, position+1, now, card.id); err != nil {
			return "", err
		}
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return dashboardID, nil
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
		if card.Type == CardRegistry {
			card.CredentialConfigured, _ = m.registry.Configured(ctx, card.ID)
		}
		cards = append(cards, card)
	}
	return cards, rows.Err()
}
func (m *Manager) getCard(ctx context.Context, id string) (Card, error) {
	card, err := scanCard(m.db.QueryRowContext(ctx, cardSelect+` WHERE id=?`, id))
	if err == nil && card.Type == CardRegistry {
		card.CredentialConfigured, _ = m.registry.Configured(ctx, card.ID)
	}
	return card, err
}

func (m *Manager) GetCard(ctx context.Context, id string) (Card, error) {
	return m.getCard(ctx, id)
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
	if card.Type == CardRegistry {
		return m.refreshRegistryCard(ctx, card)
	}
	input := CardInput{Name: card.Name, Type: card.Type, SourceURL: card.SourceURL, Headers: card.Headers, ValuePath: card.ValuePath, SecondaryPath: card.SecondaryPath, Formula: card.Formula, Config: card.Config, RefreshSeconds: card.RefreshSeconds}
	result, refreshErr := m.runCardRequest(ctx, input, card.ID)
	if refreshErr != nil {
		return m.recordFailureDiagnostic(ctx, card, refreshErr, result.Diagnostic)
	}
	snapshot := Snapshot{Value: result.Value, Secondary: result.Secondary}
	if number, ok := asNumber(result.Value); ok {
		snapshot.Number = number
	}
	encoded, _ := json.Marshal(snapshot)
	now := m.now().UTC()
	_, err = m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET snapshot_json=?,last_error='',last_success_at=?,last_attempt_at=?,updated_at=? WHERE id=?`, string(encoded), now.UnixNano(), now.UnixNano(), now.UnixNano(), id)
	if err != nil {
		return card, err
	}
	return m.getCard(ctx, id)
}

func (m *Manager) RefreshDashboard(ctx context.Context, id string) error {
	dashboard, err := m.GetDashboard(ctx, id)
	if err != nil {
		return err
	}
	var refreshErrors []error
	for _, card := range dashboard.Cards {
		if card.Type == CardWebsite {
			continue
		}
		if _, err := m.RefreshCard(ctx, card.ID); err != nil {
			refreshErrors = append(refreshErrors, err)
		}
	}
	return errors.Join(refreshErrors...)
}

func (m *Manager) refreshRegistryCard(ctx context.Context, card Card) (Card, error) {
	var config registrymonitor.Config
	if err := json.Unmarshal(card.Config, &config); err != nil {
		return m.recordFailure(ctx, card, errors.New("Registry 卡片配置无效"))
	}
	testResult, err := m.runStoredRegistryRequest(ctx, card)
	results := testResult.Images
	if err != nil && len(results) == 0 {
		return m.recordFailureDiagnostic(ctx, card, err, testResult.Diagnostic)
	}
	previous := map[string]registrymonitor.ImageResult{}
	for _, image := range card.Snapshot.Images {
		previous[image.Image] = image
	}
	failures := 0
	successes := 0
	for index := range results {
		if results[index].Error == "" {
			successes++
			continue
		}
		failures++
		if old, ok := previous[results[index].Image]; ok && old.Tag != "" {
			results[index].Tag = old.Tag
			results[index].PushedAt = old.PushedAt
			results[index].PushTimeAvailable = old.PushTimeAvailable
			results[index].TimeSource = old.TimeSource
			results[index].Stale = true
		}
	}
	snapshot := Snapshot{Images: results}
	if failures > 0 {
		snapshot.Diagnostic = &testResult.Diagnostic
	}
	encoded, _ := json.Marshal(snapshot)
	now := m.now().UTC()
	lastError := ""
	if failures > 0 {
		lastError = fmt.Sprintf("%d 个镜像刷新失败", failures)
	}
	lastSuccess := int64(0)
	if !card.LastSuccessAt.IsZero() {
		lastSuccess = card.LastSuccessAt.UnixNano()
	}
	if successes > 0 {
		lastSuccess = now.UnixNano()
	}
	_, updateErr := m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET snapshot_json=?,last_error=?,last_success_at=?,last_attempt_at=?,updated_at=? WHERE id=?`, string(encoded), lastError, lastSuccess, now.UnixNano(), now.UnixNano(), card.ID)
	if updateErr != nil {
		return card, updateErr
	}
	updated, getErr := m.getCard(ctx, card.ID)
	if getErr != nil {
		return card, getErr
	}
	if failures > 0 {
		return updated, errors.New(lastError)
	}
	return updated, nil
}

type registryOperation struct {
	ID, CardID string
	Phase      string
}

func (m *Manager) prepareRegistryConnection(ctx context.Context, cardID string, input CardInput) (registryOperation, error) {
	operationID, err := randomID()
	if err != nil {
		return registryOperation{}, err
	}
	var config registrymonitor.Config
	if err := json.Unmarshal(input.Config, &config); err != nil {
		return registryOperation{}, err
	}
	if err := m.registry.Prepare(ctx, operationID, cardID, config, input.RegistryPassword, input.PreserveRegistryPassword); err != nil {
		if input.PreserveRegistryPassword && input.RegistryPassword == "" {
			return registryOperation{}, ErrCredentialUnavailable
		}
		return registryOperation{}, err
	}
	return registryOperation{ID: operationID, CardID: cardID}, nil
}

func (m *Manager) prepareRegistryDelete(ctx context.Context, cardID string) (registryOperation, error) {
	operationID, err := randomID()
	if err != nil {
		return registryOperation{}, err
	}
	if err := m.registry.PrepareDelete(ctx, operationID, cardID); err != nil {
		return registryOperation{}, err
	}
	return registryOperation{ID: operationID, CardID: cardID}, nil
}

func (m *Manager) recordRegistryOperations(ctx context.Context, transaction *sql.Tx, operations []registryOperation) error {
	for _, operation := range operations {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO custom_dashboard_registry_operations(operation_id,card_id,created_at) VALUES(?,?,?)`, operation.ID, operation.CardID, m.now().UTC().UnixNano()); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) completeRegistryOperations(ctx context.Context, operations []registryOperation) error {
	for _, operation := range operations {
		if operation.Phase != "committed" {
			if err := m.registry.Commit(ctx, operation.ID); err != nil {
				return err
			}
			if _, err := m.db.ExecContext(ctx, `UPDATE custom_dashboard_registry_operations SET phase='committed' WHERE operation_id=?`, operation.ID); err != nil {
				return err
			}
		}
		if err := m.registry.Acknowledge(ctx, operation.ID); err != nil {
			return err
		}
		if _, err := m.db.ExecContext(ctx, `DELETE FROM custom_dashboard_registry_operations WHERE operation_id=?`, operation.ID); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) abortRegistryOperations(operations []registryOperation) {
	for _, operation := range operations {
		_ = m.registry.Abort(context.Background(), operation.ID)
	}
}

// ReconcileRegistryOperations finishes Broker mutations whose SQLite commit
// succeeded before the Web process was interrupted. Commit is idempotent.
func (m *Manager) ReconcileRegistryOperations(ctx context.Context) error {
	m.registryMutationMu.Lock()
	defer m.registryMutationMu.Unlock()
	rows, err := m.db.QueryContext(ctx, `SELECT operation_id,card_id,phase FROM custom_dashboard_registry_operations ORDER BY created_at,operation_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var operations []registryOperation
	for rows.Next() {
		var operation registryOperation
		if err := rows.Scan(&operation.ID, &operation.CardID, &operation.Phase); err != nil {
			return err
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return m.completeRegistryOperations(ctx, operations)
}

func (m *Manager) runStoredRegistryRequest(ctx context.Context, card Card) (TestResult, error) {
	started := time.Now()
	var config registrymonitor.Config
	_ = json.Unmarshal(card.Config, &config)
	result := TestResult{Diagnostic: RequestDiagnostic{Code: DiagnosticOK, Stage: "complete", Summary: "请求成功", URL: redactRequestURL(config.Endpoint), AttemptedAt: m.now().UTC()}}
	images, err := m.registry.Inspect(ctx, card.ID)
	if err != nil {
		return finishRequestFailure(result, started, "registry_auth", DiagnosticRegistryAuth, "无法使用已保存的 Registry 连接", err), err
	}
	result.Images = images
	failures := 0
	for index, image := range images {
		if image.Error != "" {
			failures++
			result.Images[index].Error = safeRegistryError(image.Error)
		}
	}
	if failures > 0 {
		err = fmt.Errorf("%d 个镜像查询失败", failures)
		return finishRequestFailure(result, started, "registry_manifest", DiagnosticRegistryManifest, err.Error(), err), err
	}
	result.OK = true
	result.Diagnostic.DurationMS = elapsedMilliseconds(started, time.Now())
	return result, nil
}

func (m *Manager) recordFailure(ctx context.Context, card Card, refreshErr error) (Card, error) {
	code := classifyNetworkError(refreshErr)
	diagnostic := RequestDiagnostic{Code: code, Stage: "request", Summary: actionableSummary(code), URL: redactRequestURL(card.SourceURL), AttemptedAt: m.now().UTC()}
	return m.recordFailureDiagnostic(ctx, card, refreshErr, diagnostic)
}

func (m *Manager) recordFailureDiagnostic(ctx context.Context, card Card, refreshErr error, diagnostic RequestDiagnostic) (Card, error) {
	message := strings.TrimSpace(diagnostic.Summary)
	if message == "" {
		message = strings.TrimSpace(secretredaction.String(refreshErr.Error()))
	}
	if len(message) > 240 {
		message = message[:240]
	}
	now := m.now().UTC()
	diagnostic.AttemptedAt = now
	card.Snapshot.Diagnostic = &diagnostic
	encoded, _ := json.Marshal(card.Snapshot)
	_, _ = m.db.ExecContext(ctx, `UPDATE custom_dashboard_cards SET snapshot_json=?,last_error=?,last_attempt_at=?,updated_at=? WHERE id=?`, string(encoded), message, now.UnixNano(), now.UnixNano(), card.ID)
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
	case CardNumber, CardPercentage, CardQuota, CardKeyValue:
		if input.SourceURL == "" {
			return errors.New("数据地址不能为空")
		}
		parsed, err := url.Parse(input.SourceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return errors.New("数据地址必须使用 HTTP 或 HTTPS")
		}
		if input.ValuePath == "" {
			return errors.New("请填写取值表达式")
		}
		if input.Type == CardQuota && input.SecondaryPath == "" {
			return errors.New("请填写剩余额度表达式")
		}
	case CardWebsite:
		if len(input.Config) == 0 {
			input.Config = []byte(`{"monitorIds":[]}`)
		}
	case CardRegistry:
		var config registrymonitor.Config
		if err := json.Unmarshal(input.Config, &config); err != nil {
			return errors.New("Registry 卡片配置无效")
		}
		config = registrymonitor.NormalizeConfig(config)
		if err := registrymonitor.ValidateConfig(config); err != nil {
			return err
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return err
		}
		input.Config = encoded
		input.SourceURL = ""
		input.Headers = nil
		input.ValuePath = ""
		input.SecondaryPath = ""
		input.Formula = ""
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
	return validateSourceHeaders(input.Headers)
}

var reservedSourceHeaders = map[string]struct{}{
	"connection": {}, "content-length": {}, "host": {}, "keep-alive": {},
	"proxy-authenticate": {}, "proxy-authorization": {}, "proxy-connection": {},
	"te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {},
}

func validateSourceHeaders(headers map[string]string) error {
	if len(headers) > maxSourceHeaders {
		return errors.New("数据源请求头数量过多")
	}
	total := 0
	for name, value := range headers {
		total += len(name) + len(value)
		if total > maxSourceHeaderBytes {
			return errors.New("数据源请求头过大")
		}
		if !validHeaderName(name) {
			return errors.New("数据源请求头名称无效")
		}
		if _, reserved := reservedSourceHeaders[strings.ToLower(name)]; reserved {
			return errors.New("数据源请求头包含保留字段")
		}
		for _, char := range value {
			if (char < 0x20 && char != '\t') || char == 0x7f {
				return errors.New("数据源请求头值无效")
			}
		}
	}
	return nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", char) {
			continue
		}
		return false
	}
	return true
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
	expression = strings.NewReplacer("×", "*", "÷", "/", "−", "-").Replace(expression)
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
