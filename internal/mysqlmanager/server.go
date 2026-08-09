package mysqlmanager

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type ConnectionTest struct {
	OK, TLS, CanReadDatabases, CanBackup, CanRestore, CanCreate, CanDrop bool
	Version, Server, Cipher, Error                                       string
}

type Database struct {
	Name, Charset, Collation string
	SizeBytes                uint64
	NonTransactionalTables   int
}

type Status struct {
	Healthy                                    bool
	Version, Server, Cipher                    string
	Uptime                                     time.Duration
	CurrentConnections, MaximumConnections     uint64
	ThreadsRunning, SlowQueries, DatabaseCount uint64
	QueriesPerSecond, TransactionsPerSecond    float64
	DataBytes, IndexBytes                      uint64
}

type CreateDatabaseInput struct {
	Name, Charset, Collation string
}

type databaseServer interface {
	Test(context.Context, Instance, string) (ConnectionTest, error)
	Databases(context.Context, Instance, string) ([]Database, error)
	Status(context.Context, Instance, string) (Status, error)
	DatabaseExists(context.Context, Instance, string, string) (bool, error)
	CreateDatabase(context.Context, Instance, string, CreateDatabaseInput) error
	ReplaceDatabase(context.Context, Instance, string, string) error
	DropDatabase(context.Context, Instance, string, string) error
	NonTransactionalTables(context.Context, Instance, string, string) ([]string, error)
}

type mysqlDatabaseServer struct {
	tlsNames sync.Map
}

func (m *Manager) TestInstance(ctx context.Context, id string) (ConnectionTest, error) {
	instance, err := m.Instance(ctx, id)
	if err != nil {
		return ConnectionTest{}, err
	}
	password, err := m.instancePassword(id)
	if err != nil {
		return ConnectionTest{}, err
	}
	result, testErr := m.server.Test(ctx, instance, password)
	state := ConnectionConnected
	if testErr != nil || !result.OK {
		state = ConnectionFailed
	}
	if stateErr := m.recordConnectionState(id, state); stateErr != nil {
		return result, errors.Join(testErr, stateErr)
	}
	return result, testErr
}

func (m *Manager) Databases(ctx context.Context, id string) ([]Database, error) {
	instance, password, err := m.instanceAndPassword(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.server.Databases(ctx, instance, password)
}

func (m *Manager) Status(ctx context.Context, id string) (Status, error) {
	instance, password, err := m.instanceAndPassword(ctx, id)
	if err != nil {
		return Status{}, err
	}
	status, statusErr := m.server.Status(ctx, instance, password)
	state := ConnectionConnected
	if statusErr != nil {
		state = ConnectionFailed
	}
	if stateErr := m.recordConnectionState(id, state); stateErr != nil {
		return status, errors.Join(statusErr, stateErr)
	}
	return status, statusErr
}

func (m *Manager) recordConnectionState(id string, state ConnectionState) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := m.db.ExecContext(ctx, `UPDATE mysql_instances SET connection_state=? WHERE id=?`, state, id)
	return err
}

func (m *Manager) CreateDatabase(ctx context.Context, id string, input CreateDatabaseInput) error {
	if IsSystemDatabase(input.Name) {
		return errors.New("system databases cannot be created or replaced")
	}
	instance, password, err := m.instanceAndPassword(ctx, id)
	if err != nil {
		return err
	}
	return m.server.CreateDatabase(ctx, instance, password, input)
}

func (m *Manager) instanceAndPassword(ctx context.Context, id string) (Instance, string, error) {
	instance, err := m.Instance(ctx, id)
	if err != nil {
		return Instance{}, "", err
	}
	password, err := m.instancePassword(id)
	return instance, password, err
}

func (server *mysqlDatabaseServer) Test(ctx context.Context, instance Instance, password string) (ConnectionTest, error) {
	database, err := server.open(instance, password)
	if err != nil {
		return ConnectionTest{Error: err.Error()}, err
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return ConnectionTest{Error: err.Error()}, err
	}
	result := ConnectionTest{OK: true, CanReadDatabases: true}
	_ = database.QueryRowContext(ctx, `SELECT @@version, @@version_comment,
		COALESCE((SELECT VARIABLE_VALUE FROM performance_schema.session_status WHERE VARIABLE_NAME='Ssl_cipher'), '')`).Scan(&result.Version, &result.Server, &result.Cipher)
	result.TLS = result.Cipher != ""
	var grants strings.Builder
	if rows, queryErr := database.QueryContext(ctx, "SHOW GRANTS"); queryErr == nil {
		defer rows.Close()
		for rows.Next() {
			var grant string
			if rows.Scan(&grant) == nil {
				grants.WriteString(strings.ToUpper(grant))
				grants.WriteByte('\n')
			}
		}
	}
	grantText := grants.String()
	all := strings.Contains(grantText, "ALL PRIVILEGES")
	result.CanBackup = all || strings.Contains(grantText, "SELECT")
	result.CanRestore = all || strings.Contains(grantText, "INSERT") || strings.Contains(grantText, "CREATE")
	result.CanCreate = all || strings.Contains(grantText, "CREATE")
	result.CanDrop = all || strings.Contains(grantText, "DROP")
	return result, nil
}

func (server *mysqlDatabaseServer) Databases(ctx context.Context, instance Instance, password string) ([]Database, error) {
	database, err := server.open(instance, password)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.QueryContext(ctx, `SELECT s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME, s.DEFAULT_COLLATION_NAME,
		COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0),
		COALESCE(SUM(CASE WHEN t.ENGINE IS NOT NULL AND UPPER(t.ENGINE) <> 'INNODB' THEN 1 ELSE 0 END), 0)
		FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA=s.SCHEMA_NAME
		GROUP BY s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME, s.DEFAULT_COLLATION_NAME ORDER BY s.SCHEMA_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Database
	for rows.Next() {
		var item Database
		if err := rows.Scan(&item.Name, &item.Charset, &item.Collation, &item.SizeBytes, &item.NonTransactionalTables); err != nil {
			return nil, err
		}
		if !IsSystemDatabase(item.Name) {
			result = append(result, item)
		}
	}
	return result, rows.Err()
}

func (server *mysqlDatabaseServer) Status(ctx context.Context, instance Instance, password string) (Status, error) {
	database, err := server.open(instance, password)
	if err != nil {
		return Status{}, err
	}
	defer database.Close()
	result := Status{Healthy: true}
	if err := database.QueryRowContext(ctx, "SELECT @@version, @@version_comment, @@max_connections").Scan(&result.Version, &result.Server, &result.MaximumConnections); err != nil {
		return Status{}, err
	}
	values := make(map[string]float64)
	rows, err := database.QueryContext(ctx, `SHOW GLOBAL STATUS WHERE Variable_name IN
		('Uptime','Threads_connected','Threads_running','Questions','Com_commit','Com_rollback','Slow_queries','Ssl_cipher')`)
	if err != nil {
		return Status{}, err
	}
	for rows.Next() {
		var name, value string
		if rows.Scan(&name, &value) == nil {
			if name == "Ssl_cipher" {
				result.Cipher = value
			} else if number, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
				values[name] = number
			}
		}
	}
	_ = rows.Close()
	uptime := values["Uptime"]
	result.Uptime = time.Duration(uptime) * time.Second
	result.CurrentConnections = uint64(values["Threads_connected"])
	result.ThreadsRunning = uint64(values["Threads_running"])
	result.SlowQueries = uint64(values["Slow_queries"])
	if uptime > 0 {
		result.QueriesPerSecond = values["Questions"] / uptime
		result.TransactionsPerSecond = (values["Com_commit"] + values["Com_rollback"]) / uptime
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(DISTINCT TABLE_SCHEMA), COALESCE(SUM(DATA_LENGTH),0), COALESCE(SUM(INDEX_LENGTH),0)
		FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')`).
		Scan(&result.DatabaseCount, &result.DataBytes, &result.IndexBytes); err != nil {
		return Status{}, err
	}
	return result, nil
}

func (server *mysqlDatabaseServer) DatabaseExists(ctx context.Context, instance Instance, password, name string) (bool, error) {
	database, err := server.open(instance, password)
	if err != nil {
		return false, err
	}
	defer database.Close()
	var exists bool
	err = database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?)", name).Scan(&exists)
	return exists, err
}

func (server *mysqlDatabaseServer) CreateDatabase(ctx context.Context, instance Instance, password string, input CreateDatabaseInput) error {
	input.Name, input.Charset, input.Collation = strings.TrimSpace(input.Name), strings.TrimSpace(input.Charset), strings.TrimSpace(input.Collation)
	if input.Name == "" || len(input.Name) > 64 || strings.ContainsRune(input.Name, 0) {
		return errors.New("invalid MySQL database name")
	}
	database, err := server.open(instance, password)
	if err != nil {
		return err
	}
	defer database.Close()
	statement := "CREATE DATABASE " + quoteIdentifier(input.Name)
	if input.Charset != "" {
		if !simpleSQLName(input.Charset) {
			return errors.New("invalid MySQL character set")
		}
		statement += " CHARACTER SET " + input.Charset
	}
	if input.Collation != "" {
		if !simpleSQLName(input.Collation) {
			return errors.New("invalid MySQL collation")
		}
		statement += " COLLATE " + input.Collation
	}
	_, err = database.ExecContext(ctx, statement)
	return err
}

func (server *mysqlDatabaseServer) ReplaceDatabase(ctx context.Context, instance Instance, password, name string) error {
	if IsSystemDatabase(name) || strings.TrimSpace(name) == "" {
		return errors.New("system or empty database cannot be replaced")
	}
	database, err := server.open(instance, password)
	if err != nil {
		return err
	}
	defer database.Close()
	charset, collation := "utf8mb4", ""
	_ = database.QueryRowContext(ctx, `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?`, name).Scan(&charset, &collation)
	if _, err := database.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdentifier(name)); err != nil {
		return err
	}
	statement := "CREATE DATABASE " + quoteIdentifier(name) + " CHARACTER SET " + charset
	if collation != "" {
		statement += " COLLATE " + collation
	}
	_, err = database.ExecContext(ctx, statement)
	return err
}

func (server *mysqlDatabaseServer) DropDatabase(ctx context.Context, instance Instance, password, name string) error {
	if IsSystemDatabase(name) || strings.TrimSpace(name) == "" {
		return errors.New("system or empty database cannot be dropped")
	}
	database, err := server.open(instance, password)
	if err != nil {
		return err
	}
	defer database.Close()
	_, err = database.ExecContext(ctx, "DROP DATABASE "+quoteIdentifier(name))
	return err
}

func (server *mysqlDatabaseServer) NonTransactionalTables(ctx context.Context, instance Instance, password, name string) ([]string, error) {
	database, err := server.open(instance, password)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA=? AND ENGINE IS NOT NULL AND UPPER(ENGINE)<>'INNODB' ORDER BY TABLE_NAME`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func (server *mysqlDatabaseServer) open(instance Instance, password string) (*sql.DB, error) {
	config := mysql.NewConfig()
	config.User, config.Passwd = instance.Username, password
	config.Net, config.Addr = "tcp", fmt.Sprintf("%s:%d", instance.Host, instance.Port)
	config.ParseTime, config.Timeout, config.ReadTimeout, config.WriteTimeout = true, 10*time.Second, 30*time.Second, 30*time.Second
	switch instance.TLSMode {
	case TLSDisabled:
		config.TLSConfig = "false"
	case TLSPreferred:
		config.TLSConfig = "preferred"
	case TLSRequired:
		config.TLSConfig = "skip-verify"
	case TLSVerifyIdentity:
		body, err := os.ReadFile(instance.CAPath)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(body) {
			return nil, errors.New("MySQL CA file contains no certificates")
		}
		digest := sha256.Sum256([]byte(instance.ID + "\x00" + instance.Host + "\x00" + instance.CAPath))
		name := "scriptboard-" + hex.EncodeToString(digest[:8])
		if _, loaded := server.tlsNames.LoadOrStore(name, struct{}{}); !loaded {
			if err := mysql.RegisterTLSConfig(name, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: instance.Host}); err != nil {
				server.tlsNames.Delete(name)
				return nil, err
			}
		}
		config.TLSConfig = name
	default:
		return nil, errors.New("invalid MySQL TLS mode")
	}
	return sql.Open("mysql", config.FormatDSN())
}

func quoteIdentifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }

func simpleSQLName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_') {
			return false
		}
	}
	return true
}
