package mysqlmanager

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/secretstore"
)

type TLSMode string

type ConnectionState string

const (
	TLSDisabled       TLSMode = "disabled"
	TLSPreferred      TLSMode = "preferred"
	TLSRequired       TLSMode = "required"
	TLSVerifyIdentity TLSMode = "verify_identity"

	ConnectionUntried   ConnectionState = "untried"
	ConnectionConnected ConnectionState = "connected"
	ConnectionFailed    ConnectionState = "failed"
)

type Instance struct {
	ID, Name, Host, Username, CAPath string
	Port                             int
	TLSMode                          TLSMode
	CredentialConfigured             bool
	ConnectionState                  ConnectionState
	CreatedAt, UpdatedAt             time.Time
	Password                         string `json:"-"`
}

type InstanceInput struct {
	ID, Name, Host, Username, Password, CAPath string
	Port                                       int
	TLSMode                                    TLSMode
}

type Options struct {
	DB               *sql.DB
	StateRoot        string
	BackupRoot       string
	DumpExecutable   string
	ClientExecutable string
	Now              func() time.Time
	Audit            func(AuditEvent)
	SecretStore      *secretstore.Store
	Backend          Backend
}

type AuditEvent struct {
	Action, Target, Result string
	Actor                  Actor
}

type InstanceService interface {
	SaveInstance(context.Context, InstanceInput) (Instance, error)
	Instance(context.Context, string) (Instance, error)
	Instances(context.Context) ([]Instance, error)
	DeleteInstance(context.Context, string) error
	TestInstance(context.Context, string) (ConnectionTest, error)
	Databases(context.Context, string) ([]Database, error)
	Status(context.Context, string) (Status, error)
	CreateDatabase(context.Context, string, CreateDatabaseInput) error
}

type BackupService interface {
	Backup(context.Context, BackupRequest) (Backup, error)
	BackupBatch(context.Context, BatchBackupRequest) ([]Backup, error)
	ImportBackup(context.Context, ImportRequest) (Backup, error)
	Backups(context.Context, string, string) ([]Backup, error)
	BackupDatabases(context.Context, string) ([]string, error)
	BackupByID(context.Context, string) (Backup, error)
	DeleteBackup(context.Context, string) error
}

type RestoreService interface {
	Restore(context.Context, RestoreRequest) (Operation, error)
	DropDatabase(context.Context, DropDatabaseRequest) (Operation, error)
	Operation(context.Context, string) (Operation, error)
	Operations(context.Context, string) ([]Operation, error)
	RequestCancel(context.Context, string) error
	RecoverInterrupted(context.Context) error
}

type PlanService interface {
	SavePlan(context.Context, PlanInput) (Plan, error)
	Plan(context.Context, string) (Plan, error)
	Plans(context.Context) ([]Plan, error)
	DeletePlan(context.Context, string) error
	ReconcilePlans(context.Context) error
	RunDuePlans(context.Context) error
}

var (
	_ InstanceService = (*Manager)(nil)
	_ BackupService   = (*Manager)(nil)
	_ RestoreService  = (*Manager)(nil)
	_ PlanService     = (*Manager)(nil)
)

type commandRunner interface {
	Run(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error
}

type Manager struct {
	db         *sql.DB
	stateRoot  string
	backupRoot string
	now        func() time.Time
	secrets    credentialStore
	runner     commandRunner
	dumpTool   string
	clientTool string
	dumpFlavor string
	flavorTool string
	active     map[string]string
	cancels    map[string]context.CancelFunc
	server     databaseServer
	backend    Backend
	audit      func(AuditEvent)
	mu         sync.Mutex
}

func New(options Options) (*Manager, error) {
	if options.DB == nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("MySQL manager database and State Root are required")
	}
	stateRoot, err := filepath.Abs(options.StateRoot)
	if err != nil {
		return nil, err
	}
	backupRoot := strings.TrimSpace(options.BackupRoot)
	if backupRoot == "" {
		backupRoot = filepath.Join(stateRoot, "database-backups", "mysql")
	}
	backupRoot, err = filepath.Abs(backupRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return nil, fmt.Errorf("prepare MySQL backup root: %w", err)
	}
	backupRoot = loadBackupRoot(context.Background(), options.DB, backupRoot)
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return nil, fmt.Errorf("prepare configured MySQL backup root: %w", err)
	}
	dumpTool := loadSetting(context.Background(), options.DB, "dump_executable", defaultString(strings.TrimSpace(options.DumpExecutable), "mysqldump"))
	clientTool := loadSetting(context.Background(), options.DB, "client_executable", defaultString(strings.TrimSpace(options.ClientExecutable), "mysql"))
	now := options.Now
	if now == nil {
		now = time.Now
	}
	manager := &Manager{
		db: options.DB, stateRoot: stateRoot, backupRoot: backupRoot, now: now,
		dumpTool: dumpTool, clientTool: clientTool, active: make(map[string]string), cancels: make(map[string]context.CancelFunc), audit: options.Audit,
	}
	if options.Backend != nil {
		manager.backend = options.Backend
		if initializer, ok := options.Backend.(interface{ InitializeTools(ToolSettings) }); ok {
			initializer.InitializeTools(ToolSettings{DumpExecutable: dumpTool, ClientExecutable: clientTool})
		}
		return manager, nil
	}
	vault := options.SecretStore
	if vault == nil {
		vault, err = secretstore.New(stateRoot)
		if err != nil {
			return nil, fmt.Errorf("initialize MySQL credential store: %w", err)
		}
	}
	manager.secrets = credentialStore{directory: filepath.Join(stateRoot, "secrets"), vault: vault}
	if err := manager.secrets.ensureMigrated(); err != nil {
		return nil, fmt.Errorf("migrate MySQL credentials: %w", err)
	}
	if err := manager.secrets.ensureBindings(options.DB); err != nil {
		return nil, fmt.Errorf("bind MySQL credentials to instances: %w", err)
	}
	manager.runner = osCommandRunner{}
	manager.server = &mysqlDatabaseServer{}
	manager.backend = &localBackend{manager: manager}
	return manager, nil
}

func (m *Manager) recordAudit(event AuditEvent) {
	if m.audit != nil {
		m.audit(event)
	}
}

func (m *Manager) BackupRoot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.backupRoot
}

func (m *Manager) SaveInstance(ctx context.Context, input InstanceInput) (Instance, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.CAPath = strings.TrimSpace(input.CAPath)
	if input.Name == "" || input.Host == "" || input.Username == "" || input.Port < 1 || input.Port > 65535 {
		return Instance{}, errors.New("instance name, host, port, and username are required")
	}
	if input.TLSMode == "" {
		input.TLSMode = TLSPreferred
	}
	if !validTLSMode(input.TLSMode) {
		return Instance{}, errors.New("invalid MySQL TLS mode")
	}
	if input.TLSMode == TLSVerifyIdentity && input.CAPath == "" {
		return Instance{}, errors.New("verified TLS requires a CA path")
	}
	if net.ParseIP(input.Host) == nil && len(input.Host) > 253 {
		return Instance{}, errors.New("MySQL host is too long")
	}
	now := m.now().UTC()
	id := strings.TrimSpace(input.ID)
	creating := id == ""
	if creating {
		if input.Password == "" {
			return Instance{}, errors.New("MySQL password is required")
		}
		id = randomID()
	}
	credentialConfigured := input.Password != ""
	connectionState := ConnectionUntried
	var previous Instance
	var err error
	if !creating && !credentialConfigured {
		previous, err = m.Instance(ctx, id)
		if err != nil {
			return Instance{}, err
		}
		credentialConfigured = previous.CredentialConfigured
		connectionState = previous.ConnectionState
		if previous.Host != input.Host || previous.Port != input.Port || previous.Username != input.Username || previous.TLSMode != input.TLSMode || previous.CAPath != input.CAPath {
			connectionState = ConnectionUntried
		}
	} else if !creating {
		previous, err = m.Instance(ctx, id)
		if err != nil {
			return Instance{}, err
		}
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Instance{}, err
	}
	defer transaction.Rollback()
	if creating {
		_, err = transaction.ExecContext(ctx, `INSERT INTO mysql_instances
			(id, name, host, port, username, tls_mode, ca_path, credential_configured, connection_state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.Name, input.Host, input.Port, input.Username,
			input.TLSMode, input.CAPath, credentialConfigured, ConnectionUntried, now.UnixNano(), now.UnixNano())
	} else {
		_, err = transaction.ExecContext(ctx, `UPDATE mysql_instances SET name=?, host=?, port=?, username=?, tls_mode=?, ca_path=?,
			credential_configured=?, connection_state=?, updated_at=? WHERE id=?`, input.Name, input.Host, input.Port, input.Username,
			input.TLSMode, input.CAPath, credentialConfigured, connectionState, now.UnixNano(), id)
	}
	if err != nil {
		return Instance{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Instance{}, err
	}
	if input.Password != "" {
		credentialInstance := Instance{ID: id, Name: input.Name, Host: input.Host, Port: input.Port, Username: input.Username, TLSMode: input.TLSMode, CAPath: input.CAPath, CredentialConfigured: true}
		if err := m.backend.StoreCredential(ctx, credentialInstance, input.Password); err != nil {
			if creating {
				_ = m.backend.DeleteCredential(ctx, id)
				_, _ = m.db.ExecContext(context.Background(), "DELETE FROM mysql_instances WHERE id=?", id)
			} else {
				_, _ = m.db.ExecContext(context.Background(), `UPDATE mysql_instances SET name=?,host=?,port=?,username=?,tls_mode=?,ca_path=?,credential_configured=?,connection_state=?,updated_at=? WHERE id=?`,
					previous.Name, previous.Host, previous.Port, previous.Username, previous.TLSMode, previous.CAPath, previous.CredentialConfigured, previous.ConnectionState, previous.UpdatedAt.UnixNano(), previous.ID)
			}
			return Instance{}, err
		}
	}
	return m.Instance(ctx, id)
}

func (m *Manager) Instance(ctx context.Context, id string) (Instance, error) {
	var instance Instance
	var configured bool
	var createdAt, updatedAt int64
	err := m.db.QueryRowContext(ctx, `SELECT id, name, host, port, username, tls_mode, ca_path, credential_configured, connection_state, created_at, updated_at
		FROM mysql_instances WHERE id=?`, id).Scan(&instance.ID, &instance.Name, &instance.Host, &instance.Port,
		&instance.Username, &instance.TLSMode, &instance.CAPath, &configured, &instance.ConnectionState, &createdAt, &updatedAt)
	if err != nil {
		return Instance{}, err
	}
	instance.CredentialConfigured = configured
	instance.CreatedAt = time.Unix(0, createdAt).UTC()
	instance.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return instance, nil
}

func (m *Manager) Instances(ctx context.Context) ([]Instance, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, name, host, port, username, tls_mode, ca_path, credential_configured, connection_state, created_at, updated_at
		FROM mysql_instances ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Instance
	for rows.Next() {
		var item Instance
		var configured bool
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.Name, &item.Host, &item.Port, &item.Username, &item.TLSMode, &item.CAPath,
			&configured, &item.ConnectionState, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CredentialConfigured = configured
		item.CreatedAt, item.UpdatedAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (m *Manager) DeleteInstance(ctx context.Context, id string) error {
	var active int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mysql_operations WHERE instance_id=? AND phase NOT IN
		('completed','cancelled','failed','rolled_back','needs_attention')`, id).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return errors.New("instance has an active MySQL operation")
	}
	if err := m.backend.DeleteCredential(ctx, id); err != nil {
		return err
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, "DELETE FROM mysql_backup_plans WHERE instance_id=?", id); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM mysql_instances WHERE id=?", id); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	return nil
}

func (m *Manager) instancePassword(id string) (string, error) { return m.secrets.get(id) }

func validTLSMode(value TLSMode) bool {
	return value == TLSDisabled || value == TLSPreferred || value == TLSRequired || value == TLSVerifyIdentity
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func loadSetting(ctx context.Context, database *sql.DB, key, fallback string) string {
	var value string
	if err := database.QueryRowContext(ctx, "SELECT value FROM mysql_settings WHERE key=?", key).Scan(&value); err == nil && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func randomID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}
