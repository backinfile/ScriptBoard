package redismanager

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"

	"scriptboard/internal/secretstore"
)

type TLSMode string
type ConnectionState string
type Environment string

const (
	TLSDisabled            TLSMode         = "disabled"
	TLSVerifyIdentity      TLSMode         = "verify_identity"
	TLSInsecureSkipVerify  TLSMode         = "insecure_skip_verify"
	ConnectionUntried      ConnectionState = "untried"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionFailed       ConnectionState = "failed"
	EnvironmentProduction  Environment     = "production"
	EnvironmentDevelopment Environment     = "development"
	EnvironmentUnspecified Environment     = "unspecified"
)

type Instance struct {
	ID, Name, Host, Username, CAPath string
	Environment                      Environment
	Port, Database                   int
	TLSMode                          TLSMode
	CredentialConfigured             bool
	ConnectionState                  ConnectionState
	CreatedAt, UpdatedAt             time.Time
}

type InstanceInput struct {
	ID, Name, Host, Username, Password, CAPath string
	Environment                                Environment
	Port, Database                             int
	TLSMode                                    TLSMode
}

type ConnectionTest struct {
	OK, TLS, CanScan, CanInfo  bool
	Version, Mode, Role, Error string
	Latency                    time.Duration
}
type Overview struct {
	Version, Mode, Role                                                             string
	Uptime                                                                          time.Duration
	ConnectedClients, BlockedClients, KeyCount, ExpiringKeys, UsedMemory, MaxMemory uint64
	OperationsPerSecond, HitRate                                                    float64
	Persistence                                                                     string
}
type ScanRequest struct {
	Cursor        uint64
	Pattern, Type string
	Count         int64
}
type KeySummary struct {
	Name, Type string
	TTL        time.Duration
	Expires    bool
	SizeBytes  int64
}
type ScanPage struct {
	Cursor uint64
	Keys   []KeySummary
}

type KeyValueItem struct {
	Field, Value string
}
type KeyValue struct {
	Name, Type, Value string
	Items             []KeyValueItem
	Truncated         bool
}

type Backend interface {
	StoreCredential(context.Context, Instance, string) error
	DeleteCredential(context.Context, string) error
	Test(context.Context, Instance) (ConnectionTest, error)
	Overview(context.Context, Instance) (Overview, error)
	Scan(context.Context, Instance, ScanRequest) (ScanPage, error)
	ReadKey(context.Context, Instance, string) (KeyValue, error)
}

type Options struct {
	DB          *sql.DB
	StateRoot   string
	SecretStore *secretstore.Store
	Backend     Backend
	Now         func() time.Time
}
type Manager struct {
	db      *sql.DB
	backend Backend
	now     func() time.Time
}

func (m *Manager) ExecutionBackend() Backend { return m.backend }

func New(options Options) (*Manager, error) {
	if options.DB == nil || strings.TrimSpace(options.StateRoot) == "" {
		return nil, errors.New("Redis manager database and State Root are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	manager := &Manager{db: options.DB, backend: options.Backend, now: now}
	if manager.backend == nil {
		vault := options.SecretStore
		var err error
		if vault == nil {
			vault, err = secretstore.New(options.StateRoot)
			if err != nil {
				return nil, err
			}
		}
		store := newCredentialStore(options.StateRoot, vault)
		manager.backend = &localBackend{credentials: store}
	}
	return manager, nil
}

func (m *Manager) SaveInstance(ctx context.Context, input InstanceInput) (Instance, error) {
	input.Name, input.Host, input.Username, input.CAPath = strings.TrimSpace(input.Name), strings.TrimSpace(input.Host), strings.TrimSpace(input.Username), strings.TrimSpace(input.CAPath)
	if input.Name == "" || input.Host == "" || input.Port < 1 || input.Port > 65535 || input.Database < 0 {
		return Instance{}, errors.New("Redis name, host, port, and non-negative database are required")
	}
	if net.ParseIP(input.Host) == nil && len(input.Host) > 253 {
		return Instance{}, errors.New("Redis host is too long")
	}
	if input.TLSMode == "" {
		input.TLSMode = TLSVerifyIdentity
	}
	if input.TLSMode != TLSDisabled && input.TLSMode != TLSVerifyIdentity && input.TLSMode != TLSInsecureSkipVerify {
		return Instance{}, errors.New("invalid Redis TLS mode")
	}
	if input.Environment == "" {
		input.Environment = EnvironmentUnspecified
	}
	if input.Environment != EnvironmentProduction && input.Environment != EnvironmentDevelopment && input.Environment != EnvironmentUnspecified {
		return Instance{}, errors.New("invalid Redis environment")
	}
	id := strings.TrimSpace(input.ID)
	creating := id == ""
	if creating {
		id = randomID()
	}
	configured := creating || input.Password != ""
	state := ConnectionUntried
	var previous Instance
	var err error
	if !creating {
		previous, err = m.Instance(ctx, id)
		if err != nil {
			return Instance{}, err
		}
		if !configured {
			configured = previous.CredentialConfigured
		}
		if input.Password == "" && (previous.Host != input.Host || previous.Port != input.Port || previous.Username != input.Username || previous.Database != input.Database || previous.TLSMode != input.TLSMode || previous.CAPath != input.CAPath) {
			return Instance{}, errors.New("Redis password is required when connection or TLS settings change")
		}
	}
	now := m.now().UTC()
	if creating {
		_, err = m.db.ExecContext(ctx, `INSERT INTO redis_instances(id,name,environment,host,port,username,database_index,tls_mode,ca_path,credential_configured,connection_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, input.Name, input.Environment, input.Host, input.Port, input.Username, input.Database, input.TLSMode, input.CAPath, configured, state, now.UnixNano(), now.UnixNano())
	} else {
		_, err = m.db.ExecContext(ctx, `UPDATE redis_instances SET name=?,environment=?,host=?,port=?,username=?,database_index=?,tls_mode=?,ca_path=?,credential_configured=?,connection_state=?,updated_at=? WHERE id=?`, input.Name, input.Environment, input.Host, input.Port, input.Username, input.Database, input.TLSMode, input.CAPath, configured, state, now.UnixNano(), id)
	}
	if err != nil {
		return Instance{}, err
	}
	if creating || input.Password != "" {
		instance := Instance{ID: id, Name: input.Name, Environment: input.Environment, Host: input.Host, Port: input.Port, Username: input.Username, Database: input.Database, TLSMode: input.TLSMode, CAPath: input.CAPath, CredentialConfigured: true}
		if err := m.backend.StoreCredential(ctx, instance, input.Password); err != nil {
			if creating {
				_, _ = m.db.ExecContext(context.Background(), "DELETE FROM redis_instances WHERE id=?", id)
			} else {
				_, _ = m.db.ExecContext(context.Background(), `UPDATE redis_instances SET name=?,environment=?,host=?,port=?,username=?,database_index=?,tls_mode=?,ca_path=?,credential_configured=?,connection_state=?,updated_at=? WHERE id=?`, previous.Name, previous.Environment, previous.Host, previous.Port, previous.Username, previous.Database, previous.TLSMode, previous.CAPath, previous.CredentialConfigured, previous.ConnectionState, previous.UpdatedAt.UnixNano(), previous.ID)
			}
			return Instance{}, err
		}
	}
	return m.Instance(ctx, id)
}

func scanInstance(scanner interface{ Scan(...any) error }) (Instance, error) {
	var i Instance
	var configured bool
	var created, updated int64
	err := scanner.Scan(&i.ID, &i.Name, &i.Environment, &i.Host, &i.Port, &i.Username, &i.Database, &i.TLSMode, &i.CAPath, &configured, &i.ConnectionState, &created, &updated)
	i.CredentialConfigured = configured
	i.CreatedAt = time.Unix(0, created).UTC()
	i.UpdatedAt = time.Unix(0, updated).UTC()
	return i, err
}

const instanceColumns = `id,name,environment,host,port,username,database_index,tls_mode,ca_path,credential_configured,connection_state,created_at,updated_at`

func (m *Manager) Instance(ctx context.Context, id string) (Instance, error) {
	return scanInstance(m.db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM redis_instances WHERE id=?`, id))
}
func (m *Manager) Instances(ctx context.Context) ([]Instance, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM redis_instances ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		i, e := scanInstance(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (m *Manager) DeleteInstance(ctx context.Context, id string) error {
	if err := m.backend.DeleteCredential(ctx, id); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx, "DELETE FROM redis_instances WHERE id=?", id)
	return err
}
func (m *Manager) TestInstance(ctx context.Context, id string) (ConnectionTest, error) {
	i, e := m.Instance(ctx, id)
	if e != nil {
		return ConnectionTest{}, e
	}
	r, e := m.backend.Test(ctx, i)
	state := ConnectionConnected
	if e != nil || !r.OK {
		state = ConnectionFailed
	}
	_, _ = m.db.ExecContext(context.Background(), "UPDATE redis_instances SET connection_state=? WHERE id=?", state, id)
	return r, e
}
func (m *Manager) Overview(ctx context.Context, id string) (Overview, error) {
	i, e := m.Instance(ctx, id)
	if e != nil {
		return Overview{}, e
	}
	return m.backend.Overview(ctx, i)
}
func (m *Manager) Scan(ctx context.Context, id string, r ScanRequest) (ScanPage, error) {
	if r.Count <= 0 || r.Count > 500 {
		r.Count = 200
	}
	if len(r.Pattern) > 512 {
		return ScanPage{}, errors.New("Redis scan pattern is too long")
	}
	i, e := m.Instance(ctx, id)
	if e != nil {
		return ScanPage{}, e
	}
	return m.backend.Scan(ctx, i, r)
}
func (m *Manager) ReadKey(ctx context.Context, id, key string) (KeyValue, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 512 || strings.ContainsAny(key, "\r\n\x00") {
		return KeyValue{}, errors.New("Redis key is invalid")
	}
	i, e := m.Instance(ctx, id)
	if e != nil {
		return KeyValue{}, e
	}
	return m.backend.ReadKey(ctx, i, key)
}
func randomID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
