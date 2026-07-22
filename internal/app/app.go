package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"

	"scriptboard/internal/diskspace"
	"scriptboard/internal/gitprotect"
	"scriptboard/internal/instancelock"
	"scriptboard/internal/managedfiles"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/scheduler"
)

const initialPasswordFilename = "initial-admin-password"
const currentSchemaVersion = 5

const (
	sessionCookieName   = "scriptboard_session"
	loginCSRFCookieName = "scriptboard_login_csrf"
)

type contextKey string

const (
	sessionContextKey contextKey = "session"
	secureContextKey  contextKey = "secure-request"
)

type Config struct {
	ManagedRoot       string
	StateRoot         string
	RunTimeoutGrace   time.Duration
	SchedulerNow      func() time.Time
	SchedulerTick     time.Duration
	GitExecutable     string
	ExecutorChains    map[string][]string
	AdminUsername     string
	AdminPassword     string
	AdminPasswordFile string
	TrustedProxies    []string
}

type App struct {
	db                 *sql.DB
	stateRoot          string
	managedRoot        string
	managed            *managedfiles.Store
	runs               *runmanager.Manager
	scheduler          *scheduler.Manager
	gitProtection      *gitprotect.Manager
	instanceLock       *instancelock.Lock
	handler            http.Handler
	loginMu            sync.Mutex
	loginFailures      map[string]loginFailure
	credentialOverride bool
	trustedProxies     []*net.IPNet
}

type loginFailure struct {
	count        int
	blockedUntil time.Time
}

func Open(config Config) (*App, error) {
	managedRoot, stateRoot, err := prepareRoots(config.ManagedRoot, config.StateRoot)
	if err != nil {
		return nil, err
	}
	instanceLock, err := instancelock.Acquire(stateRoot)
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = instanceLock.Close()
		}
	}()

	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		return nil, err
	}

	trustedProxies, err := parseTrustedProxies(config.TrustedProxies)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	application := &App{db: db, stateRoot: stateRoot, managedRoot: managedRoot, managed: managedfiles.Open(managedRoot), instanceLock: instanceLock, loginFailures: make(map[string]loginFailure), trustedProxies: trustedProxies}
	if err := application.initializeAdmin(stateRoot); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := application.applyCredentialOverride(config.AdminUsername, config.AdminPassword, config.AdminPasswordFile); err != nil {
		_ = db.Close()
		return nil, err
	}
	_, _ = db.Exec("DELETE FROM audit_events WHERE occurred_at < ?", time.Now().UTC().AddDate(-1, 0, 0).Unix())
	timeoutGrace := config.RunTimeoutGrace
	if timeoutGrace <= 0 {
		timeoutGrace = 30 * time.Second
	}
	application.runs = runmanager.New(db, application.managed, stateRoot, timeoutGrace, config.ExecutorChains)
	if cleaned, cleanupErr := application.runs.CleanupLogs(90*24*time.Hour, 1<<30); cleanupErr != nil {
		_ = db.Close()
		return nil, cleanupErr
	} else if cleaned > 0 {
		application.recordAudit("cleanup_run_logs", fmt.Sprintf("%d logs", cleaned), "succeeded", "system")
	}
	application.gitProtection, err = gitprotect.New(db, managedRoot, config.GitExecutable, stateRoot)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	application.runs.SetLifecycle(application.gitProtection)
	application.scheduler = scheduler.New(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick)
	application.handler = application.routes(managedRoot)
	opened = true
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func parseTrustedProxies(values []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			result = append(result, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("无效可信代理 %q", value)
		}
		result = append(result, network)
	}
	return result, nil
}

func (a *App) applyTrustedProxy(request *http.Request) *http.Request {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	peer := net.ParseIP(host)
	trusted := false
	for _, network := range a.trustedProxies {
		if peer != nil && network.Contains(peer) {
			trusted = true
			break
		}
	}
	if !trusted {
		return request
	}
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0; index-- {
		client := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if client == nil {
			continue
		}
		clientTrusted := false
		for _, network := range a.trustedProxies {
			if network.Contains(client) {
				clientTrusted = true
				break
			}
		}
		if !clientTrusted {
			request.RemoteAddr = client.String()
			break
		}
	}
	forwardedProto := strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")
	if strings.EqualFold(strings.TrimSpace(forwardedProto[len(forwardedProto)-1]), "https") {
		request = request.WithContext(context.WithValue(request.Context(), secureContextKey, true))
	}
	return request
}

func isSecureRequest(request *http.Request) bool {
	secure, _ := request.Context().Value(secureContextKey).(bool)
	return request.TLS != nil || secure
}

func (a *App) ResetAdminCredentials(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		return "", err
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("UPDATE admin SET username = ?, password_hash = ?, must_change_password = 1 WHERE id = 1", username, hash); err != nil {
		return "", err
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		return "", err
	}
	passwordPath := filepath.Join(a.stateRoot, "secrets", initialPasswordFilename)
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		_ = os.Remove(passwordPath)
		return "", err
	}
	a.recordAudit("admin_reset", username, "succeeded", "local-cli")
	return password, nil
}

func (a *App) applyCredentialOverride(username, password, passwordFile string) error {
	if passwordFile != "" {
		content, err := os.ReadFile(passwordFile)
		if err != nil {
			return fmt.Errorf("读取管理员密码文件: %w", err)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(content), "\n"), "\r")
	}
	if username == "" && password == "" {
		return nil
	}
	var currentUsername, currentHash string
	if err := a.db.QueryRow("SELECT username, password_hash FROM admin WHERE id = 1").Scan(&currentUsername, &currentHash); err != nil {
		return err
	}
	if username == "" {
		username = currentUsername
	}
	if !utf8.ValidString(username) || utf8.RuneCountInString(username) == 0 || utf8.RuneCountInString(username) > 64 {
		return errors.New("管理员用户名覆盖无效")
	}
	changed := username != currentUsername
	newHash := currentHash
	if password != "" {
		if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len([]byte(password)) > 256 || password == username {
			return errors.New("管理员密码覆盖不符合长度规则")
		}
		if !verifyPassword(password, currentHash) {
			changed = true
			hash, err := hashPassword(password)
			if err != nil {
				return err
			}
			newHash = hash
		}
	}
	a.credentialOverride = true
	if !changed {
		return nil
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("UPDATE admin SET username = ?, password_hash = ?, must_change_password = 0 WHERE id = 1", username, newHash); err != nil {
		return err
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	a.recordAudit("startup_credential_override", username, "succeeded", "system")
	return nil
}

func (a *App) Close() error {
	if a.scheduler != nil {
		a.scheduler.Close()
	}
	if a.runs != nil {
		a.runs.Close()
	}
	dbErr := a.db.Close()
	lockErr := a.instanceLock.Close()
	if dbErr != nil {
		return dbErr
	}
	return lockErr
}

func prepareRoots(managed, state string) (string, string, error) {
	if strings.TrimSpace(managed) == "" || strings.TrimSpace(state) == "" {
		return "", "", errors.New("受管根目录和内部状态目录不能为空")
	}
	for _, root := range []string{managed, state} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", "", fmt.Errorf("创建目录 %q: %w", root, err)
		}
	}
	managedReal, err := filepath.EvalSymlinks(managed)
	if err != nil {
		return "", "", fmt.Errorf("解析受管根目录: %w", err)
	}
	stateReal, err := filepath.EvalSymlinks(state)
	if err != nil {
		return "", "", fmt.Errorf("解析内部状态目录: %w", err)
	}
	managedReal, err = filepath.Abs(managedReal)
	if err != nil {
		return "", "", fmt.Errorf("解析受管根目录绝对路径: %w", err)
	}
	stateReal, err = filepath.Abs(stateReal)
	if err != nil {
		return "", "", fmt.Errorf("解析内部状态目录绝对路径: %w", err)
	}
	if pathContains(managedReal, stateReal) || pathContains(stateReal, managedReal) {
		return "", "", errors.New("受管根目录和内部状态目录不能相同或互相包含")
	}
	return managedReal, stateReal, nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func openDatabase(path string) (*sql.DB, error) {
	info, statErr := os.Stat(path)
	existingDatabase := statErr == nil && info.Size() > 0
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure SQLite: %w", err)
		}
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		return nil, fmt.Errorf("SQLite integrity check failed: result=%q error=%v", integrity, err)
	}
	var schemaVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read SQLite schema version: %w", err)
	}
	if schemaVersion > currentSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("database schema version %d is newer than supported version %d", schemaVersion, currentSchemaVersion)
	}
	if existingDatabase && schemaVersion < currentSchemaVersion {
		snapshot := path + fmt.Sprintf(".pre-migration-v%d", schemaVersion)
		_ = os.Remove(snapshot)
		quoted := strings.ReplaceAll(filepath.ToSlash(snapshot), "'", "''")
		if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("create pre-migration database snapshot: %w", err)
		}
	}
	migration, err := db.Begin()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("begin SQLite migration: %w", err)
	}
	defer func() { _ = migration.Rollback() }()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			must_change_password INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			csrf_token TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at INTEGER NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			result TEXT NOT NULL,
			source_address TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS trash_entries (
			id TEXT PRIMARY KEY,
			original_path TEXT NOT NULL,
			stored_name TEXT NOT NULL UNIQUE,
			deleted_at INTEGER NOT NULL,
			size INTEGER NOT NULL,
			is_directory INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			script_path TEXT NOT NULL,
			script_sha256 TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			template_arguments_json TEXT NOT NULL DEFAULT '[]',
			arguments_json TEXT NOT NULL,
			executor TEXT NOT NULL,
			source_type TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			started_at INTEGER,
			finished_at INTEGER,
			exit_code INTEGER,
			error TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			log_path TEXT NOT NULL
			, source_name TEXT NOT NULL DEFAULT ''
			, runtime_identity TEXT NOT NULL DEFAULT ''
			, log_expired INTEGER NOT NULL DEFAULT 0
			, log_incomplete INTEGER NOT NULL DEFAULT 0
			, log_truncated INTEGER NOT NULL DEFAULT 0
			, dropped_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS variables (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT NOT NULL REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			expression TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			enabled INTEGER NOT NULL,
			allow_overlap INTEGER NOT NULL,
			next_fire_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
			, deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_triggers (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			scheduled_for INTEGER NOT NULL,
			result TEXT NOT NULL,
			run_id TEXT NOT NULL,
			error TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_trigger_aggregates (
			schedule_id TEXT NOT NULL REFERENCES schedules(id),
			period TEXT NOT NULL,
			result TEXT NOT NULL,
			trigger_count INTEGER NOT NULL,
			PRIMARY KEY (schedule_id, period, result)
		)`,
		`CREATE TABLE IF NOT EXISTS git_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			status TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			branch TEXT NOT NULL,
			git_executable TEXT NOT NULL,
			max_tracked_file_bytes INTEGER NOT NULL,
			max_repository_bytes INTEGER NOT NULL,
			last_commit TEXT NOT NULL,
			abnormal_reason TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化 SQLite: %w", err)
		}
	}
	if schemaVersion == 1 {
		if _, err := migration.Exec("ALTER TABLE schedules ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate schedules to schema 2: %w", err)
		}
	}
	if schemaVersion > 0 && schemaVersion < 3 {
		if _, err := migration.Exec("ALTER TABLE runs ADD COLUMN source_name TEXT NOT NULL DEFAULT ''"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate runs source snapshot: %w", err)
		}
		if _, err := migration.Exec("ALTER TABLE runs ADD COLUMN runtime_identity TEXT NOT NULL DEFAULT ''"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate runs runtime identity: %w", err)
		}
	}
	if schemaVersion > 0 && schemaVersion < 4 {
		for _, statement := range []string{
			"ALTER TABLE runs ADD COLUMN log_expired INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE runs ADD COLUMN log_incomplete INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE runs ADD COLUMN log_truncated INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE runs ADD COLUMN dropped_bytes INTEGER NOT NULL DEFAULT 0",
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Run log metadata: %w", err)
			}
		}
	}
	if _, err := migration.Exec(fmt.Sprintf("PRAGMA user_version=%d", currentSchemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("record SQLite schema version: %w", err)
	}
	if _, err := migration.Exec(`UPDATE runs SET status = 'disconnected', finished_at = ?, error = CASE WHEN error = '' THEN 'service supervision was lost' ELSE error END WHERE status IN ('starting', 'running', 'stopping', 'timing_out')`, time.Now().UnixNano()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("recover disconnected runs: %w", err)
	}
	if err := migration.Commit(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("commit SQLite migration: %w", err)
	}
	return db, nil
}

func (a *App) initializeAdmin(stateRoot string) error {
	transaction, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("开始 admin 初始化事务: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	var exists int
	if err := transaction.QueryRow("SELECT EXISTS(SELECT 1 FROM admin WHERE id = 1)").Scan(&exists); err != nil {
		return fmt.Errorf("检查 admin: %w", err)
	}
	if exists != 0 {
		return transaction.Commit()
	}

	passwordBytes := make([]byte, 24)
	if _, err := rand.Read(passwordBytes); err != nil {
		return fmt.Errorf("生成初始密码: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if _, err := transaction.Exec(
		"INSERT INTO admin (id, username, password_hash, must_change_password) VALUES (1, 'admin', ?, 1)",
		hash,
	); err != nil {
		return fmt.Errorf("创建 admin: %w", err)
	}

	secretsRoot := filepath.Join(stateRoot, "secrets")
	if err := os.MkdirAll(secretsRoot, 0o700); err != nil {
		return fmt.Errorf("创建秘密目录: %w", err)
	}
	passwordPath := filepath.Join(secretsRoot, initialPasswordFilename)
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入初始密码: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		_ = os.Remove(passwordPath)
		return fmt.Errorf("提交 admin 初始化: %w", err)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码 salt: %w", err)
	}
	const memory = 64 * 1024
	const iterations = 3
	const parallelism = 2
	const keyLength = 32
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version, memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (a *App) routes(_ string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app-v2.css", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/css; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache, must-revalidate")
		_, _ = io.WriteString(response, appCSS)
	})
	mux.HandleFunc("GET /assets/app.css", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/css; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache, must-revalidate")
		_, _ = io.WriteString(response, appCSS)
	})
	mux.HandleFunc("GET /assets/app-v2.js", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache, must-revalidate")
		_, _ = io.WriteString(response, appJS)
	})
	mux.Handle("GET /", a.requireSession(false, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "/files/", http.StatusSeeOther)
	})))
	mux.HandleFunc("GET /login", func(response http.ResponseWriter, request *http.Request) {
		if current, _, ok := a.loadSession(request); ok {
			if current.mustChange {
				http.Redirect(response, request, "/settings/account", http.StatusSeeOther)
			} else {
				http.Redirect(response, request, "/files/", http.StatusSeeOther)
			}
			return
		}
		renderLoginPage(response, request, http.StatusOK, "", "")
	})
	mux.HandleFunc("POST /login", a.login)
	mux.Handle("POST /logout", a.requireSession(false, http.HandlerFunc(a.logout)))
	mux.Handle("GET /settings/account", a.requireSession(true, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := request.Context().Value(sessionContextKey).(session)
		var username string
		if err := a.db.QueryRow("SELECT username FROM admin WHERE id = 1").Scan(&username); err != nil {
			http.Error(response, "无法读取管理员账户", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = accountTemplate.Execute(response, struct {
			Username, CSRFToken string
			CredentialOverride  bool
		}{Username: username, CSRFToken: current.csrfToken, CredentialOverride: a.credentialOverride})
	})))
	mux.Handle("POST /settings/account", a.requireSession(true, http.HandlerFunc(a.changePassword)))
	mux.Handle("GET /files/{path...}", a.requireSession(false, http.HandlerFunc(a.filesPage)))
	mux.Handle("POST /files/mkdir", a.requireSession(false, http.HandlerFunc(a.createDirectory)))
	mux.Handle("POST /files/upload", a.requireSession(false, http.HandlerFunc(a.uploadFiles)))
	mux.Handle("GET /files/download/{path...}", a.requireSession(false, http.HandlerFunc(a.downloadFile)))
	mux.Handle("GET /files/preview/{path...}", a.requireSession(false, http.HandlerFunc(a.previewImage)))
	mux.Handle("POST /files/delete", a.requireSession(false, http.HandlerFunc(a.deleteFile)))
	mux.Handle("POST /files/move", a.requireSession(false, http.HandlerFunc(a.moveFile)))
	mux.Handle("POST /files/toggle-executable", a.requireSession(false, http.HandlerFunc(a.toggleExecutable)))
	mux.Handle("GET /trash", a.requireSession(false, http.HandlerFunc(a.trashPage)))
	mux.Handle("POST /trash/restore", a.requireSession(false, http.HandlerFunc(a.restoreTrash)))
	mux.Handle("POST /trash/purge", a.requireSession(false, http.HandlerFunc(a.purgeTrash)))
	mux.Handle("GET /files/edit/{path...}", a.requireSession(false, http.HandlerFunc(a.editTextPage)))
	mux.Handle("POST /files/edit/{path...}", a.requireSession(false, http.HandlerFunc(a.saveText)))
	mux.Handle("POST /runs/start", a.requireSession(false, http.HandlerFunc(a.startRun)))
	mux.Handle("GET /runs", a.requireSession(false, http.HandlerFunc(a.runsPage)))
	mux.Handle("GET /runs/{id}", a.requireSession(false, http.HandlerFunc(a.runDetails)))
	mux.Handle("POST /runs/{id}/stop", a.requireSession(false, http.HandlerFunc(a.stopRun)))
	mux.Handle("GET /runs/{id}/events", a.requireSession(false, http.HandlerFunc(a.runEvents)))
	mux.Handle("GET /variables", a.requireSession(false, http.HandlerFunc(a.variablesPage)))
	mux.Handle("POST /variables", a.requireSession(false, http.HandlerFunc(a.createVariable)))
	mux.Handle("POST /variables/{name}/update", a.requireSession(false, http.HandlerFunc(a.updateVariable)))
	mux.Handle("POST /variables/{name}/delete", a.requireSession(false, http.HandlerFunc(a.deleteVariable)))
	mux.Handle("POST /runs/{id}/quick-run", a.requireSession(false, http.HandlerFunc(a.saveQuickRun)))
	mux.Handle("GET /quick-runs", a.requireSession(false, http.HandlerFunc(a.quickRunsPage)))
	mux.Handle("POST /quick-runs/{id}/start", a.requireSession(false, http.HandlerFunc(a.startQuickRun)))
	mux.Handle("POST /quick-runs/{id}/move", a.requireSession(false, http.HandlerFunc(a.moveQuickRun)))
	mux.Handle("POST /quick-runs/{id}/delete", a.requireSession(false, http.HandlerFunc(a.deleteQuickRun)))
	mux.Handle("GET /schedules", a.requireSession(false, http.HandlerFunc(a.schedulesPage)))
	mux.Handle("POST /schedules", a.requireSession(false, http.HandlerFunc(a.createSchedule)))
	mux.Handle("POST /schedules/{id}/update", a.requireSession(false, http.HandlerFunc(a.updateSchedule)))
	mux.Handle("POST /schedules/{id}/toggle", a.requireSession(false, http.HandlerFunc(a.toggleSchedule)))
	mux.Handle("POST /schedules/{id}/delete", a.requireSession(false, http.HandlerFunc(a.deleteSchedule)))
	mux.Handle("GET /audit", a.requireSession(false, http.HandlerFunc(a.auditPage)))
	mux.Handle("GET /audit.csv", a.requireSession(false, http.HandlerFunc(a.auditDownload)))
	mux.Handle("GET /settings/version-protection", a.requireSession(false, http.HandlerFunc(a.versionProtectionPage)))
	mux.Handle("POST /settings/version-protection/enable", a.requireSession(false, http.HandlerFunc(a.enableVersionProtection)))
	mux.Handle("POST /settings/version-protection/adopt", a.requireSession(false, http.HandlerFunc(a.adoptVersionProtection)))
	mux.Handle("POST /settings/version-protection/disable", a.requireSession(false, http.HandlerFunc(a.disableVersionProtection)))
	mux.Handle("POST /settings/version-protection/checkpoint", a.requireSession(false, http.HandlerFunc(a.checkpointVersionProtection)))
	mux.Handle("POST /settings/version-protection/restore", a.requireSession(false, http.HandlerFunc(a.restoreVersionedFile)))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request = a.applyTrustedProxy(request)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if isSecureRequest(request) {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		pageResponse := &pageResponseWriter{ResponseWriter: response}
		mux.ServeHTTP(pageResponse, request)
		pageResponse.finish(a, request)
	})
}

type pageResponseWriter struct {
	http.ResponseWriter
	status    int
	buffering bool
	committed bool
	body      bytes.Buffer
}

func (w *pageResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.committed = true
	w.status = status
	contentType := w.Header().Get("Content-Type")
	w.buffering = (strings.HasPrefix(contentType, "text/html") && (status < 300 || status >= 400)) ||
		(status >= 400 && strings.HasPrefix(contentType, "text/plain"))
	if !w.buffering {
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *pageResponseWriter) Write(value []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		return w.body.Write(value)
	}
	return w.ResponseWriter.Write(value)
}

func (w *pageResponseWriter) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.status)
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
		w.body.Reset()
		w.buffering = false
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *pageResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *pageResponseWriter) finish(a *App, request *http.Request) {
	if !w.buffering {
		return
	}
	body := w.body.Bytes()
	if w.status >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		body = renderApplicationError(request, w.status, strings.TrimSpace(string(body)))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if request.URL.Path != "/login" {
		body = a.addApplicationHeader(request, body)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

type navigationItem struct {
	Href    string
	Label   string
	Current bool
}

type applicationHeaderData struct {
	Username    string
	CSRFToken   string
	Environment string
	ActiveRuns  int
	Navigation  []navigationItem
}

func (a *App) addApplicationHeader(request *http.Request, body []byte) []byte {
	current, username, ok := a.loadSession(request)
	if !ok {
		return body
	}
	activeRuns := 0
	_ = a.db.QueryRow("SELECT COUNT(*) FROM runs WHERE status IN ('starting', 'running', 'stopping', 'timing_out')").Scan(&activeRuns)
	environment := "本机"
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		remoteHost = request.RemoteAddr
	}
	if ip := net.ParseIP(remoteHost); ip != nil && !ip.IsLoopback() {
		environment = "远程"
	}
	items := []navigationItem{
		{Href: "/files/", Label: "文件"},
		{Href: "/runs", Label: "运行记录"},
		{Href: "/quick-runs", Label: "快捷执行"},
		{Href: "/schedules", Label: "计划"},
		{Href: "/variables", Label: "变量"},
		{Href: "/audit", Label: "审计"},
		{Href: "/settings/version-protection", Label: "版本保护"},
	}
	for index := range items {
		items[index].Current = items[index].Href == "/files/" && (strings.HasPrefix(request.URL.Path, "/files") || request.URL.Path == "/trash") ||
			items[index].Href != "/files/" && strings.HasPrefix(request.URL.Path, items[index].Href)
	}
	var header bytes.Buffer
	_ = applicationHeaderTemplate.Execute(&header, applicationHeaderData{
		Username: username, CSRFToken: current.csrfToken, Environment: environment,
		ActiveRuns: activeRuns, Navigation: items,
	})
	bodyText := string(body)
	bodyStart := strings.Index(bodyText, "<body")
	if bodyStart < 0 {
		return body
	}
	bodyEnd := strings.Index(bodyText[bodyStart:], ">")
	if bodyEnd < 0 {
		return body
	}
	insertAt := bodyStart + bodyEnd + 1
	return []byte(bodyText[:insertAt] + header.String() + bodyText[insertAt:])
}

func renderApplicationError(request *http.Request, status int, message string) []byte {
	destination, label := "/files/", "返回文件"
	switch {
	case strings.HasPrefix(request.URL.Path, "/settings/account"):
		destination, label = "/settings/account", "返回账户设置"
	case strings.HasPrefix(request.URL.Path, "/settings/version-protection"):
		destination, label = "/settings/version-protection", "返回版本保护"
	case strings.HasPrefix(request.URL.Path, "/runs"):
		destination, label = "/runs", "返回运行记录"
	case strings.HasPrefix(request.URL.Path, "/quick-runs"):
		destination, label = "/quick-runs", "返回快捷执行"
	case strings.HasPrefix(request.URL.Path, "/schedules"):
		destination, label = "/schedules", "返回计划"
	case strings.HasPrefix(request.URL.Path, "/variables"):
		destination, label = "/variables", "返回变量"
	case strings.HasPrefix(request.URL.Path, "/audit"):
		destination, label = "/audit", "返回审计"
	}
	var page bytes.Buffer
	_ = applicationErrorTemplate.Execute(&page, struct {
		Status             int
		Message            string
		Destination, Label string
	}{Status: status, Message: message, Destination: destination, Label: label})
	return page.Bytes()
}

const appCSS = `
:root{color-scheme:light;--canvas:#f3f3ef;--paper:#fbfbf8;--ink:#161713;--muted:#6d7068;--line:#d8d9d2;--line-strong:#bfc1b7;--accent:#e7f34b;--accent-strong:#d7e432;--danger:#b83b2f;--terminal:#151613;--terminal-ink:#f1f2e9;font:15px/1.5 Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
*{box-sizing:border-box}html{background:var(--canvas)}body{margin:0;background:var(--canvas);color:var(--ink);min-height:100vh}body:after{content:"";position:fixed;inset:0;pointer-events:none;opacity:.22;background-image:linear-gradient(rgba(20,21,18,.045) 1px,transparent 1px),linear-gradient(90deg,rgba(20,21,18,.045) 1px,transparent 1px);background-size:32px 32px;mask-image:linear-gradient(to bottom,black,transparent 38%)}
.app-header{position:sticky;top:0;z-index:20;background:rgba(243,243,239,.94);border-bottom:1px solid var(--line);backdrop-filter:blur(14px)}.app-header__inner{width:min(1440px,calc(100% - 48px));min-height:72px;margin:auto;display:grid;grid-template-columns:190px 1fr auto;align-items:center;gap:20px}.brand{display:flex;align-items:center;gap:11px;color:var(--ink);font-weight:800;text-decoration:none}.brand__mark{display:grid;place-items:center;width:28px;height:28px;background:var(--ink);color:var(--accent);font:800 15px/1 ui-monospace,SFMono-Regular,Consolas,monospace}.brand__word{font-size:17px}.app-nav{display:flex;align-items:center;gap:4px;overflow-x:auto;white-space:nowrap;scrollbar-width:none}.app-nav::-webkit-scrollbar{display:none}.app-nav a{color:var(--muted);padding:8px 11px;text-decoration:none;font-size:13px;font-weight:650;border-radius:4px;transition:background .16s ease,color .16s ease}.app-nav a:hover{background:rgba(22,23,19,.06);color:var(--ink)}.app-nav a[aria-current="page"]{background:var(--ink);color:#fff}.app-user{display:flex;align-items:center;justify-content:flex-end;gap:10px;white-space:nowrap}.app-user>a{font-size:12px;font-weight:700}.app-user form{display:block;margin:0;padding:0;border:0}.app-user input{display:none}.app-user button{min-height:32px;padding:6px 9px;background:transparent;border-color:var(--line-strong);color:var(--ink);font-size:12px}.app-status{display:flex;align-items:center;gap:7px;color:var(--muted);font:700 11px/1 ui-monospace,SFMono-Regular,Consolas,monospace}.app-status:before{content:"";width:7px;height:7px;background:#50a852;border-radius:50%;box-shadow:0 0 0 3px rgba(80,168,82,.12)}
main{position:relative;z-index:1;width:min(1180px,calc(100% - 48px));margin:0 auto;padding:58px 0 84px;overflow-x:visible;animation:arrive .34s cubic-bezier(.2,.8,.2,1)}main:before{content:"WORKSPACE / " attr(data-section);display:block;margin-bottom:14px;color:var(--muted);font:700 11px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.08em;text-transform:uppercase}h1{max-width:920px;font-size:clamp(34px,4.6vw,64px);line-height:1.02;letter-spacing:0;margin:0 0 42px;font-weight:750}h2{margin:42px 0 18px;font-size:20px;letter-spacing:0}p,dd,li{color:var(--muted)}a{color:var(--ink);text-decoration-color:var(--line-strong);text-underline-offset:3px}a:hover{text-decoration-color:var(--ink)}
form{display:flex;align-items:end;gap:10px;flex-wrap:wrap;margin:14px 0;padding:18px 0;border-top:1px solid var(--line)}td form{display:inline-flex;margin:2px 6px 2px 0;padding:0;border:0;align-items:center}label{display:grid;gap:7px;color:var(--muted);font-size:12px;font-weight:650}label:has(input[type="checkbox"]){display:flex;align-items:center;min-height:38px;padding:0 4px}input,textarea,select,button{font:inherit;border:1px solid var(--line-strong);border-radius:4px;background:var(--paper);color:var(--ink);padding:9px 11px;min-height:40px}input,select{min-width:150px}input[type="checkbox"]{min-width:16px;width:16px;min-height:16px;accent-color:var(--ink)}input[type="file"]{padding:6px}textarea{min-width:min(620px,88vw);min-height:130px;resize:vertical;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}button{cursor:pointer;background:var(--ink);border-color:var(--ink);color:#fff;font-weight:720;transition:transform .14s ease,background .14s ease,color .14s ease}button:hover{background:#2d2f29;transform:translateY(-1px)}button:disabled{cursor:not-allowed;opacity:.45;transform:none}form:first-of-type button[type="submit"],form:first-of-type button:not([type]){background:var(--accent);border-color:var(--accent-strong);color:var(--ink)}button[name="confirm"],button[name="path"]{background:transparent;color:var(--danger);border-color:#d9aaa5}button:focus-visible,input:focus-visible,textarea:focus-visible,select:focus-visible,a:focus-visible{outline:3px solid rgba(204,217,37,.7);outline-offset:2px}
table{width:100%;border-collapse:separate;border-spacing:0;margin-top:28px;min-width:720px;background:var(--paper);border-top:1px solid var(--ink);border-bottom:1px solid var(--line)}th,td{text-align:left;padding:15px 14px;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted);font-size:11px;text-transform:uppercase;letter-spacing:.06em;font-weight:750;background:#f6f6f1}tbody tr{transition:background .14s ease,transform .14s ease}tbody tr:hover{background:#f1f2e9}tbody tr:last-child td{border-bottom:0}td:first-child{font-weight:650}td a{font-weight:700}td ol{margin:0;padding-left:18px}td img{max-width:96px;height:auto}
pre{position:relative;background:var(--terminal);color:var(--terminal-ink);border:0;border-radius:4px;padding:24px;white-space:pre-wrap;word-break:break-word;max-height:65vh;overflow:auto;box-shadow:inset 0 0 0 1px #30312d;font:13px/1.65 ui-monospace,SFMono-Regular,Consolas,monospace}pre:before{content:"SCRIPTBOARD OUTPUT";display:block;margin-bottom:18px;color:#92958a;font-size:10px;letter-spacing:.1em}dl{display:grid;grid-template-columns:minmax(120px,max-content) 1fr;gap:0;border-top:1px solid var(--ink);border-bottom:1px solid var(--line);background:var(--paper)}dt,dd{padding:12px 14px;border-bottom:1px solid var(--line)}dt{color:var(--muted);font-size:12px;font-weight:700}dd{margin:0;word-break:break-all;color:var(--ink)}dt:last-of-type,dt:last-of-type+dd{border-bottom:0}img{display:block;border-radius:3px;margin-bottom:8px;border:1px solid var(--line)}[data-source="stderr"]{color:#ff8e82}[data-source="system"],[data-encoding-error="true"]{color:var(--accent)}[data-run-live-state]{font:650 12px/1.4 ui-monospace,SFMono-Regular,Consolas,monospace;color:var(--muted)}
.login-page{display:grid;place-items:center;background:var(--terminal);color:var(--terminal-ink)}.login-page:after{opacity:.35;background-image:linear-gradient(rgba(231,243,75,.07) 1px,transparent 1px),linear-gradient(90deg,rgba(231,243,75,.07) 1px,transparent 1px)}.login-page main{width:min(440px,calc(100% - 32px));margin:0;padding:54px 46px;background:var(--paper);color:var(--ink);overflow:visible;box-shadow:18px 18px 0 var(--accent)}.login-page main:before{content:"SCRIPTBOARD / ADMIN ACCESS"}.login-page h1{font-size:48px;margin-bottom:36px}.login-page form{display:grid;align-items:stretch;padding:0;border:0}.login-page input,.login-page button{width:100%}.login-page .app-header{display:none}.login-error{display:grid;gap:3px;margin:-16px 0 24px;padding:13px 14px;border-left:3px solid var(--danger);background:#f8e9e7;color:var(--danger);animation:error-in .2s ease-out}.login-error strong{font-size:12px}.login-error span{font-size:13px;color:#74312b}
.page-error{max-width:720px;padding:18px;border-left:3px solid var(--danger);background:#f8e9e7;color:#74312b}.error-code{margin:0 0 10px;font:700 12px/1 ui-monospace,SFMono-Regular,Consolas,monospace;color:var(--danger)}.error-return{display:inline-block;margin-top:12px;font-weight:750}
@keyframes arrive{from{opacity:0;transform:translateY(10px)}to{opacity:1;transform:none}}@keyframes error-in{from{opacity:0;transform:translateY(-4px)}to{opacity:1;transform:none}}@media(max-width:1050px){.app-header__inner{width:min(100% - 28px,1180px);grid-template-columns:auto 1fr;padding:10px 0}.app-user{justify-self:end}.app-nav{grid-column:1/-1;order:3;width:100%;padding-bottom:2px}main{width:min(100% - 28px,1180px);padding-top:38px}h1{margin-bottom:30px}}@media(max-width:640px){.brand__word{font-size:15px}.app-header__inner{gap:10px}.app-user{gap:7px}.app-user>a{display:none}.app-status{font-size:10px}main{padding-bottom:56px}main:before{margin-bottom:10px}h1{font-size:36px}form{align-items:stretch;flex-direction:column}td form{display:flex;align-items:stretch}input,textarea,select,button{width:100%;min-height:44px}input[type="checkbox"]{width:18px}table{display:block;width:100%;max-width:100%;min-width:0;overflow-x:auto}.login-page main{padding:40px 28px;box-shadow:10px 10px 0 var(--accent)}}@media(prefers-reduced-motion:reduce){*,*:before{animation:none!important;transition:none!important}}
`

const appJS = `
(()=>{
  const path=location.pathname;
  const main=document.querySelector('main');
  if(path==='/login'){
    document.body.classList.add('login-page');
  }else if(main){
    const links=[
      ['/files/','文件'],['/runs','运行记录'],['/quick-runs','快捷执行'],
      ['/schedules','计划'],['/variables','变量'],['/audit','审计'],
      ['/settings/version-protection','版本保护'],['/settings/account','账户']
    ];
    const section=links.find(([href])=>href==='/files/'?path.startsWith('/files')||path==='/trash':path.startsWith(href));
    main.dataset.section=section?.[1]||'控制台';
  }
  const root=document.querySelector('[data-run-events-url]');
  const log=document.querySelector('[data-run-log]');
  if(!root||!log||!window.EventSource)return;
  const pause=document.querySelector('[data-run-pause]');
  const state=document.querySelector('[data-run-live-state]');
  const limit=2000;
  let paused=false;
  let completed='';
  let pending=[];
  const trim=()=>{while(log.children.length>limit)log.firstElementChild.remove()};
  const append=(data,sequence)=>{
    const span=document.createElement('span');
    span.dataset.sequence=sequence;
    span.dataset.source=data.source||'output';
    span.textContent=data.text||'';
    if(data.encoding_error)span.title='输出包含无效 UTF-8，已替换显示';
    log.append(span);trim();log.scrollTop=log.scrollHeight;
  };
  let last=Number(log.lastElementChild?.dataset.sequence||0);
  const url=new URL(root.dataset.runEventsUrl,location.href);
  if(last>0)url.searchParams.set('after',String(last));
  const stream=new EventSource(url);
  stream.addEventListener('open',()=>{if(state)state.textContent='实时连接已建立'});
  stream.addEventListener('error',()=>{if(state)state.textContent='连接中断，正在自动重连…'});
  stream.addEventListener('output',event=>{
    let data;try{data=JSON.parse(event.data)}catch{return}
    last=Number(event.lastEventId||last);
    if(paused){pending.push([data,last]);if(pending.length>limit)pending.shift();return}
    append(data,last);
  });
  stream.addEventListener('complete',event=>{
    completed=event.data;stream.close();
    if(state)state.textContent='Run 已结束：'+completed;
    if(pause)pause.hidden=true;
    const runStatus=document.querySelector('[data-run-status]');if(runStatus)runStatus.textContent=completed;
    const stopForm=document.querySelector('[data-run-stop-form]');if(stopForm)stopForm.hidden=true;
  });
  pause?.addEventListener('click',()=>{
    paused=!paused;pause.textContent=paused?'继续显示':'暂停显示';
    if(state)state.textContent=paused?'显示已暂停；后台仍在接收':(completed?'Run 已结束：'+completed:'实时显示中');
    if(!paused){for(const item of pending)append(item[0],item[1]);pending=[]}
  });
})();
`

func (a *App) checkpointVersionProtection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.HasActive() {
		http.Error(response, "存在活动运行，不能创建 Git 检查点", http.StatusConflict)
		return
	}
	if err := a.gitProtection.Checkpoint("ScriptBoard manual checkpoint\n\nScriptBoard-Operation: manual-checkpoint"); err != nil {
		http.Error(response, "无法创建检查点："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("git_checkpoint", "git", "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/version-protection", http.StatusSeeOther)
}

func (a *App) restoreVersionedFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.HasActive() {
		http.Error(response, "存在活动运行，不能恢复版本文件", http.StatusConflict)
		return
	}
	if err := a.gitProtection.RestoreFile(request.FormValue("path"), request.FormValue("commit")); err != nil {
		http.Error(response, "无法恢复版本文件："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("restore_versioned_file", request.FormValue("path"), "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(request.FormValue("path"))
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) versionProtectionPage(response http.ResponseWriter, request *http.Request) {
	state, err := a.gitProtection.State()
	if err != nil {
		http.Error(response, "无法读取版本保护状态", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	historyPath := request.URL.Query().Get("path")
	var history []gitprotect.Commit
	if historyPath != "" && state.Enabled {
		history, err = a.gitProtection.History(historyPath)
		if err != nil {
			http.Error(response, "无法读取文件历史："+err.Error(), http.StatusBadRequest)
			return
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = versionProtectionTemplate.Execute(response, struct {
		State       gitprotect.State
		CSRFToken   string
		HistoryPath string
		History     []gitprotect.Commit
	}{State: state, CSRFToken: current.csrfToken, HistoryPath: historyPath, History: history})
}

func (a *App) disableVersionProtection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "停用版本保护需要明确确认", http.StatusForbidden)
		return
	}
	if a.runs.HasActive() {
		http.Error(response, "存在活动运行，不能停用版本保护", http.StatusConflict)
		return
	}
	if err := a.gitProtection.Disable(); err != nil {
		http.Error(response, "无法停用版本保护："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("disable_version_protection", "git", "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/version-protection", http.StatusSeeOther)
}

func (a *App) enableVersionProtection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if request.FormValue("confirm") != "yes" {
		http.Error(response, "启用版本保护需要明确确认", http.StatusBadRequest)
		return
	}
	if a.runs.HasActive() {
		http.Error(response, "存在活动运行，不能启用版本保护", http.StatusConflict)
		return
	}
	if err := a.gitProtection.Enable(); err != nil {
		a.recordAudit("enable_version_protection", "git", "failed", request.RemoteAddr)
		http.Error(response, "无法启用版本保护："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("enable_version_protection", "git", "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/version-protection", http.StatusSeeOther)
}

func (a *App) adoptVersionProtection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "adopt-clean-repository" {
		http.Error(response, "接管已有仓库需要明确确认", http.StatusForbidden)
		return
	}
	if a.runs.HasActive() {
		http.Error(response, "存在活动运行，不能接管 Git 仓库", http.StatusConflict)
		return
	}
	if err := a.gitProtection.Adopt(); err != nil {
		a.recordAudit("adopt_version_protection", "git", "failed", request.RemoteAddr)
		http.Error(response, "无法接管 Git 仓库："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("adopt_version_protection", "git", "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/settings/version-protection", http.StatusSeeOther)
}

type auditView struct {
	OccurredAt time.Time
	Action     string
	Target     string
	Result     string
	Source     string
}

func (a *App) auditPage(response http.ResponseWriter, _ *http.Request) {
	rows, err := a.db.Query("SELECT occurred_at, action, target, result, source_address FROM audit_events ORDER BY occurred_at DESC LIMIT 1000")
	if err != nil {
		http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var events []auditView
	for rows.Next() {
		var event auditView
		var occurredAt int64
		if err := rows.Scan(&occurredAt, &event.Action, &event.Target, &event.Result, &event.Source); err != nil {
			http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
			return
		}
		event.OccurredAt = time.Unix(occurredAt, 0).UTC()
		events = append(events, event)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = auditTemplate.Execute(response, events)
}

func (a *App) auditDownload(response http.ResponseWriter, _ *http.Request) {
	rows, err := a.db.Query("SELECT occurred_at, action, target, result, source_address FROM audit_events ORDER BY occurred_at")
	if err != nil {
		http.Error(response, "无法导出审计事件", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="scriptboard-audit.csv"`)
	writer := csv.NewWriter(response)
	_ = writer.Write([]string{"occurred_at", "action", "target", "result", "source_address"})
	for rows.Next() {
		var occurred int64
		var action, target, result, source string
		if rows.Scan(&occurred, &action, &target, &result, &source) != nil {
			return
		}
		_ = writer.Write([]string{time.Unix(occurred, 0).UTC().Format(time.RFC3339), action, target, result, source})
	}
	writer.Flush()
}

func (a *App) schedulesPage(response http.ResponseWriter, request *http.Request) {
	schedules, err := a.scheduler.List()
	if err != nil {
		http.Error(response, "无法读取计划", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = schedulesTemplate.Execute(response, struct {
		Schedules []scheduler.Schedule
		CSRFToken string
	}{Schedules: schedules, CSRFToken: current.csrfToken})
}

func (a *App) createSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := scheduleRequest(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := a.scheduler.Create(values)
	if err != nil {
		http.Error(response, "无法创建计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("create_schedule", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/schedules", http.StatusSeeOther)
}

func scheduleRequest(request *http.Request) (scheduler.CreateRequest, error) {
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		return scheduler.CreateRequest{}, errors.New("计划名称无效")
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 24*60*60 {
			return scheduler.CreateRequest{}, errors.New("超时必须是 0 到 86400 秒")
		}
		timeoutSeconds = parsed
	}
	return scheduler.CreateRequest{
		Name: name, ScriptPath: request.FormValue("script"), ArgumentsTemplate: request.FormValue("arguments"),
		Expression: request.FormValue("expression"), TimeoutSeconds: timeoutSeconds,
		AllowOverlap: request.FormValue("disallow_overlap") == "",
	}, nil
}

func (a *App) updateSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := scheduleRequest(request)
	if err == nil {
		err = a.scheduler.Update(request.PathValue("id"), values)
	}
	if err != nil {
		http.Error(response, "无法更新计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("update_schedule", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/schedules", http.StatusSeeOther)
}

func (a *App) toggleSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	enabled := request.FormValue("enabled") == "1"
	if err := a.scheduler.SetEnabled(request.PathValue("id"), enabled); err != nil {
		http.Error(response, "无法更改计划状态", http.StatusNotFound)
		return
	}
	a.recordAudit("toggle_schedule", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/schedules", http.StatusSeeOther)
}

func (a *App) deleteSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除计划需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	if err := a.scheduler.Delete(request.PathValue("id")); err != nil {
		http.Error(response, "无法删除计划", http.StatusNotFound)
		return
	}
	a.recordAudit("delete_schedule", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/schedules", http.StatusSeeOther)
}

type quickRunView struct {
	ID                string
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	TimeoutSeconds    int
	Valid             bool
}

type overlapView struct {
	Action, Script, Arguments, Timeout, CSRFToken string
}

func (a *App) saveQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行名称无效", http.StatusBadRequest)
		return
	}
	source, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "来源运行不存在", http.StatusNotFound)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建快捷执行", http.StatusInternalServerError)
		return
	}
	var sortOrder int
	_ = a.db.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs").Scan(&sortOrder)
	if _, err := a.db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, source.ScriptPath, source.ArgumentsTemplate, source.TimeoutSeconds, source.ID, sortOrder, time.Now().UTC().Unix(),
	); err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAudit("create_quick_run", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/quick-runs", http.StatusSeeOther)
}

func (a *App) quickRunsPage(response http.ResponseWriter, request *http.Request) {
	rows, err := a.db.Query("SELECT id, name, script_path, arguments_template, timeout_seconds FROM quick_runs ORDER BY sort_order, created_at")
	if err != nil {
		http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
		return
	}
	var quickRuns []quickRunView
	for rows.Next() {
		var quick quickRunView
		if err := rows.Scan(&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
			return
		}
		if info, infoErr := a.managed.Info(quick.ScriptPath); infoErr == nil && info.Mode().IsRegular() {
			quick.Valid = true
		}
		quickRuns = append(quickRuns, quick)
	}
	_ = rows.Close()
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = quickRunsTemplate.Execute(response, struct {
		QuickRuns []quickRunView
		CSRFToken string
	}{QuickRuns: quickRuns, CSRFToken: current.csrfToken})
}

func (a *App) startQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	var quick quickRunView
	if err := a.db.QueryRow("SELECT id, name, script_path, arguments_template, timeout_seconds FROM quick_runs WHERE id = ?", request.PathValue("id")).Scan(
		&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds,
	); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	if a.runs.IsActiveScript(quick.ScriptPath) && request.FormValue("confirm_overlap") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.WriteHeader(http.StatusConflict)
		_ = overlapTemplate.Execute(response, overlapView{Action: "/quick-runs/" + url.PathEscape(quick.ID) + "/start", Script: quick.ScriptPath, CSRFToken: current.csrfToken})
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath: quick.ScriptPath, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds,
		SourceType: "admin/quick-run", SourceName: quick.Name, Variables: variables,
	})
	if err != nil {
		http.Error(response, "无法启动快捷执行："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("start_quick_run", quick.ID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) moveQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	direction := request.FormValue("direction")
	operator, order := "<", "DESC"
	if direction == "down" {
		operator, order = ">", "ASC"
	} else if direction != "up" {
		http.Error(response, "排序方向无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	var currentOrder int
	if err := transaction.QueryRow("SELECT sort_order FROM quick_runs WHERE id = ?", request.PathValue("id")).Scan(&currentOrder); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	var neighborID string
	var neighborOrder int
	query := "SELECT id, sort_order FROM quick_runs WHERE sort_order " + operator + " ? ORDER BY sort_order " + order + " LIMIT 1"
	if scanErr := transaction.QueryRow(query, currentOrder).Scan(&neighborID, &neighborOrder); scanErr == nil {
		_, err = transaction.Exec("UPDATE quick_runs SET sort_order = CASE id WHEN ? THEN ? WHEN ? THEN ? END WHERE id IN (?, ?)", request.PathValue("id"), neighborOrder, neighborID, currentOrder, request.PathValue("id"), neighborID)
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		err = scanErr
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	a.recordAudit("move_quick_run", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/quick-runs", http.StatusSeeOther)
}

func (a *App) deleteQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除快捷执行需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	result, err := a.db.Exec("DELETE FROM quick_runs WHERE id = ?", request.PathValue("id"))
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil || count == 0 {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	a.recordAudit("delete_quick_run", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/quick-runs", http.StatusSeeOther)
}

func (a *App) runEvents(response http.ResponseWriter, request *http.Request) {
	lastSequence := int64(0)
	if value := request.URL.Query().Get("after"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			lastSequence = parsed
		}
	}
	if value := request.Header.Get("Last-Event-ID"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			lastSequence = parsed
		}
	}
	run, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "运行不存在", http.StatusNotFound)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "当前连接不支持 SSE", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, event := range run.Events {
			if event.Sequence <= lastSequence {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"source": event.Source, "text": event.Data, "time": event.Time, "encoding_error": event.EncodingError})
			_, _ = fmt.Fprintf(response, "id: %d\nevent: output\ndata: %s\n\n", event.Sequence, payload)
			lastSequence = event.Sequence
		}
		flusher.Flush()
		if run.Status != "starting" && run.Status != "running" && run.Status != "stopping" && run.Status != "timing_out" {
			_, _ = fmt.Fprintf(response, "event: complete\ndata: %s\n\n", run.Status)
			flusher.Flush()
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
		run, err = a.runs.Get(request.PathValue("id"))
		if err != nil {
			return
		}
	}
}

type variableView struct {
	Name  string
	Value string
}

func (a *App) variablesPage(response http.ResponseWriter, request *http.Request) {
	rows, err := a.db.Query("SELECT name, value FROM variables ORDER BY name")
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	var variables []variableView
	for rows.Next() {
		var variable variableView
		if err := rows.Scan(&variable.Name, &variable.Value); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取变量", http.StatusInternalServerError)
			return
		}
		variables = append(variables, variable)
	}
	_ = rows.Close()
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = variablesTemplate.Execute(response, struct {
		Variables []variableView
		CSRFToken string
	}{Variables: variables, CSRFToken: current.csrfToken})
}

var variableNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (a *App) createVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := request.FormValue("name")
	value := request.FormValue("value")
	if !variableNamePattern.MatchString(name) || len([]byte(value)) > 4<<10 {
		http.Error(response, "变量名称或值无效", http.StatusBadRequest)
		return
	}
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&count); err != nil || count >= 1000 {
		http.Error(response, "变量数量已达到上限", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Unix()
	if _, err := a.db.Exec("INSERT INTO variables (name, value, created_at, updated_at) VALUES (?, ?, ?, ?)", name, value, now, now); err != nil {
		http.Error(response, "变量已存在或无法保存", http.StatusConflict)
		return
	}
	a.recordAudit("create_variable", name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/variables", http.StatusSeeOther)
}

func (a *App) updateVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	original := request.PathValue("name")
	name, value := request.FormValue("name"), request.FormValue("value")
	if !variableNamePattern.MatchString(name) || len([]byte(value)) > 4<<10 {
		http.Error(response, "变量名称或值无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法更新变量", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec("UPDATE variables SET name = ?, value = ?, updated_at = ? WHERE name = ?", name, value, time.Now().UTC().Unix(), original)
	if err == nil && name != original {
		oldReference, newReference := "{{"+original+"}}", "{{"+name+"}}"
		_, err = transaction.Exec("UPDATE quick_runs SET arguments_template = replace(arguments_template, ?, ?)", oldReference, newReference)
		if err == nil {
			_, err = transaction.Exec("UPDATE schedules SET arguments_template = replace(arguments_template, ?, ?)", oldReference, newReference)
		}
	}
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
		err = transaction.Commit()
	}
	if err != nil || count == 0 {
		http.Error(response, "变量不存在、名称冲突或无法更新", http.StatusConflict)
		return
	}
	a.recordAudit("update_variable", original, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/variables", http.StatusSeeOther)
}

func (a *App) deleteVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除变量需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	name := request.PathValue("name")
	reference := "%{{" + name + "}}%"
	var references int
	if err := a.db.QueryRow("SELECT (SELECT COUNT(*) FROM quick_runs WHERE arguments_template LIKE ?) + (SELECT COUNT(*) FROM schedules WHERE deleted = 0 AND arguments_template LIKE ?)", reference, reference).Scan(&references); err != nil {
		http.Error(response, "无法检查变量引用", http.StatusInternalServerError)
		return
	}
	if references != 0 {
		http.Error(response, "变量仍被快捷执行或计划引用", http.StatusConflict)
		return
	}
	result, err := a.db.Exec("DELETE FROM variables WHERE name = ?", name)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil || count == 0 {
		http.Error(response, "变量不存在", http.StatusNotFound)
		return
	}
	a.recordAudit("delete_variable", name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/variables", http.StatusSeeOther)
}

func (a *App) stopRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.runs.Stop(id); err != nil {
		http.Error(response, "无法停止运行："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("stop_run", id, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) startRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.IsActiveScript(request.FormValue("script")) && request.FormValue("confirm_overlap") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.WriteHeader(http.StatusConflict)
		_ = overlapTemplate.Execute(response, overlapView{
			Action: "/runs/start", Script: request.FormValue("script"), Arguments: request.FormValue("arguments"), Timeout: request.FormValue("timeout_seconds"), CSRFToken: current.csrfToken,
		})
		return
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 24*60*60 {
			http.Error(response, "超时必须是 0 到 86400 秒", http.StatusBadRequest)
			return
		}
		timeoutSeconds = parsed
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath:        request.FormValue("script"),
		ArgumentsTemplate: request.FormValue("arguments"),
		SourceType:        "admin/manual",
		SourceName:        "manual",
		TimeoutSeconds:    timeoutSeconds,
		Variables:         variables,
	})
	if err != nil {
		a.recordAudit("start_run", request.FormValue("script"), "rejected", request.RemoteAddr)
		http.Error(response, "无法启动脚本："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("start_run", id, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) loadVariables() (map[string]string, error) {
	variables := make(map[string]string)
	rows, err := a.db.Query("SELECT name, value FROM variables")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		variables[name] = value
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return variables, rows.Close()
}

func (a *App) runDetails(response http.ResponseWriter, request *http.Request) {
	run, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(response, "运行不存在", http.StatusNotFound)
			return
		}
		http.Error(response, "无法读取运行："+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(run.Events) > 1000 {
		run.Events = run.Events[len(run.Events)-1000:]
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runTemplate.Execute(response, struct {
		Run       runmanager.Run
		CSRFToken string
	}{Run: run, CSRFToken: current.csrfToken})
}

func (a *App) runsPage(response http.ResponseWriter, _ *http.Request) {
	runs, err := a.runs.List(500)
	if err != nil {
		http.Error(response, "无法读取运行记录："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runsTemplate.Execute(response, runs)
}

func (a *App) moveFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	source := request.FormValue("source")
	destination := request.FormValue("destination")
	if a.runs.ConflictsPath(source) {
		http.Error(response, "活动运行持有该脚本或其后代的运行租约", http.StatusConflict)
		return
	}
	if err := a.managed.Move(source, destination); err != nil {
		http.Error(response, "无法移动条目："+err.Error(), http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err == nil {
		prefix := source + "/%"
		for _, table := range []string{"quick_runs", "schedules"} {
			query := "UPDATE " + table + " SET script_path = CASE WHEN script_path = ? THEN ? ELSE ? || substr(script_path, ?) END WHERE script_path = ? OR script_path LIKE ?"
			_, err = transaction.Exec(query, source, destination, destination, len(source)+1, source, prefix)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if transaction != nil {
			_ = transaction.Rollback()
		}
		_ = a.managed.Move(destination, source)
		http.Error(response, "无法同步更新引用："+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.checkpointWebMutation("move-entry", source+" -> "+destination); err != nil {
		http.Error(response, "条目已移动，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("move_entry", source+" -> "+destination, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(filepath.ToSlash(destination))
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) toggleExecutable(response http.ResponseWriter, request *http.Request) {
	if runtime.GOOS != "linux" {
		http.NotFound(response, request)
		return
	}
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	path := request.FormValue("path")
	if a.runs.ConflictsPath(path) {
		http.Error(response, "活动运行持有该脚本的运行租约", http.StatusConflict)
		return
	}
	if _, err := a.managed.ToggleOwnerExecute(path); err != nil {
		http.Error(response, "无法切换所有者执行权限："+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.checkpointWebMutation("toggle-owner-execute", path); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("toggle_owner_execute", path, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(path)
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) editTextPage(response http.ResponseWriter, request *http.Request) {
	relative := request.PathValue("path")
	document, err := a.managed.ReadText(relative, 1<<20)
	if err != nil {
		http.Error(response, "无法编辑文件："+err.Error(), http.StatusBadRequest)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = textEditorTemplate.Execute(response, struct {
		Path      string
		Content   string
		Digest    string
		CSRFToken string
	}{Path: relative, Content: document.Content, Digest: document.Digest, CSRFToken: current.csrfToken})
}

func (a *App) saveText(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := diskspace.Require(a.managedRoot, diskspace.MinimumWritableBytes); err != nil {
		http.Error(response, err.Error(), http.StatusInsufficientStorage)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
		return
	}
	relative := request.PathValue("path")
	if a.runs.ConflictsPath(relative) {
		http.Error(response, "活动运行持有该脚本的运行租约", http.StatusConflict)
		return
	}
	trashed, err := a.managed.SaveText(relative, request.FormValue("digest"), request.FormValue("content"), id, 1<<20)
	if errors.Is(err, managedfiles.ErrConflict) {
		http.Error(response, "文件已被外部修改，请重新打开后再保存", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(response, "无法保存文件："+err.Error(), http.StatusBadRequest)
		return
	}
	_, err = a.db.Exec(
		"INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, 0)",
		id, trashed.OriginalPath, trashed.StoredName, time.Now().UTC().Unix(), trashed.Size,
	)
	if err != nil {
		_ = a.managed.RollbackTextSave(relative, trashed.StoredName)
		http.Error(response, "无法记录文件旧版本", http.StatusInternalServerError)
		return
	}
	if err := a.checkpointWebMutation("edit-text", relative); err != nil {
		http.Error(response, "文件已保存，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("edit_text", relative, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(filepath.ToSlash(relative))
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) downloadFile(response http.ResponseWriter, request *http.Request) {
	relative := request.PathValue("path")
	file, info, err := a.managed.OpenRegular(relative)
	if err != nil {
		http.Error(response, "无法下载文件："+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()})
	response.Header().Set("Content-Disposition", disposition)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

func (a *App) previewImage(response http.ResponseWriter, request *http.Request) {
	relative := request.PathValue("path")
	extension := strings.ToLower(filepath.Ext(relative))
	contentTypes := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
	contentType, allowed := contentTypes[extension]
	if !allowed {
		http.Error(response, "该格式只能下载，不能内嵌预览", http.StatusUnsupportedMediaType)
		return
	}
	file, info, err := a.managed.OpenRegular(relative)
	if err != nil {
		http.Error(response, "无法预览图片："+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Disposition", "inline")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

func (a *App) deleteFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.ConflictsPath(request.FormValue("path")) {
		http.Error(response, "活动运行持有该脚本或其后代的运行租约", http.StatusConflict)
		return
	}
	path := filepath.ToSlash(strings.Trim(request.FormValue("path"), "/"))
	like := path + "/%"
	var quickCount, scheduleCount int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM quick_runs WHERE script_path = ? OR script_path LIKE ?", path, like).Scan(&quickCount)
	_ = a.db.QueryRow("SELECT COUNT(*) FROM schedules WHERE deleted = 0 AND (script_path = ? OR script_path LIKE ?)", path, like).Scan(&scheduleCount)
	if (quickCount > 0 || scheduleCount > 0) && request.FormValue("confirm_references") != "yes" {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusConflict)
		_ = deleteImpactTemplate.Execute(response, struct {
			Path                 string
			QuickRuns, Schedules int
			CSRFToken            string
		}{Path: path, QuickRuns: quickCount, Schedules: scheduleCount, CSRFToken: current.csrfToken})
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
		return
	}
	trashed, err := a.managed.MoveToTrash(request.FormValue("path"), id)
	if err != nil {
		http.Error(response, "无法删除条目："+err.Error(), http.StatusBadRequest)
		return
	}
	_, err = a.db.Exec(
		"INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, ?)",
		id, trashed.OriginalPath, trashed.StoredName, time.Now().UTC().Unix(), trashed.Size, trashed.Directory,
	)
	if err != nil {
		_ = a.managed.RestoreFromTrash(trashed.StoredName, trashed.OriginalPath)
		http.Error(response, "无法记录回收条目", http.StatusInternalServerError)
		return
	}
	path = filepath.ToSlash(strings.TrimSuffix(trashed.OriginalPath, "/"))
	like = path + "/%"
	if _, err := a.db.Exec("UPDATE schedules SET enabled = 0, updated_at = ? WHERE deleted = 0 AND (script_path = ? OR script_path LIKE ?)", time.Now().UTC().UnixNano(), path, like); err != nil {
		_ = a.managed.RestoreFromTrash(trashed.StoredName, trashed.OriginalPath)
		_, _ = a.db.Exec("DELETE FROM trash_entries WHERE id = ?", id)
		http.Error(response, "无法停用引用该条目的计划", http.StatusInternalServerError)
		return
	}
	if err := a.checkpointWebMutation("trash-entry", path); err != nil {
		http.Error(response, "条目已移入回收站，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("trash_entry", trashed.OriginalPath, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/trash", http.StatusSeeOther)
}

type trashView struct {
	ID           string
	OriginalPath string
	DeletedAt    time.Time
	Size         int64
	Directory    bool
}

func (a *App) trashPage(response http.ResponseWriter, request *http.Request) {
	rows, err := a.db.Query("SELECT id, original_path, deleted_at, size, is_directory FROM trash_entries ORDER BY deleted_at DESC")
	if err != nil {
		http.Error(response, "无法读取回收站", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var entries []trashView
	for rows.Next() {
		var entry trashView
		var deletedAt int64
		if err := rows.Scan(&entry.ID, &entry.OriginalPath, &deletedAt, &entry.Size, &entry.Directory); err != nil {
			http.Error(response, "无法读取回收条目", http.StatusInternalServerError)
			return
		}
		entry.DeletedAt = time.Unix(deletedAt, 0).UTC()
		entries = append(entries, entry)
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = trashTemplate.Execute(response, struct {
		Entries   []trashView
		CSRFToken string
	}{Entries: entries, CSRFToken: current.csrfToken})
}

func (a *App) restoreTrash(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.FormValue("id")
	var original, stored string
	if err := a.db.QueryRow("SELECT original_path, stored_name FROM trash_entries WHERE id = ?", id).Scan(&original, &stored); err != nil {
		http.Error(response, "回收条目不存在", http.StatusNotFound)
		return
	}
	if err := a.managed.RestoreFromTrash(stored, original); err != nil {
		http.Error(response, "无法恢复条目："+err.Error(), http.StatusConflict)
		return
	}
	if _, err := a.db.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		_, _ = a.managed.MoveToTrash(original, stored)
		http.Error(response, "无法更新回收站记录", http.StatusInternalServerError)
		return
	}
	if err := a.checkpointWebMutation("restore-trash", original); err != nil {
		http.Error(response, "条目已恢复，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("restore_trash", original, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(filepath.ToSlash(original))
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) purgeTrash(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "永久清理需要 CSRF 和明确确认", http.StatusForbidden)
		return
	}
	id := request.FormValue("id")
	var original, stored string
	if err := a.db.QueryRow("SELECT original_path, stored_name FROM trash_entries WHERE id = ?", id).Scan(&original, &stored); err != nil {
		http.Error(response, "回收条目不存在", http.StatusNotFound)
		return
	}
	if err := a.managed.PurgeTrash(stored); err != nil {
		http.Error(response, "无法永久清理条目："+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := a.db.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		http.Error(response, "回收条目已清理，但无法更新记录", http.StatusInternalServerError)
		return
	}
	a.recordAudit("purge_trash", original, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/trash", http.StatusSeeOther)
}

func (a *App) uploadFiles(response http.ResponseWriter, request *http.Request) {
	if err := diskspace.Require(a.managedRoot, diskspace.MinimumWritableBytes); err != nil {
		http.Error(response, err.Error(), http.StatusInsufficientStorage)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 2<<30)
	reader, err := request.MultipartReader()
	if err != nil {
		http.Error(response, "上传请求必须使用 multipart/form-data", http.StatusBadRequest)
		return
	}
	var csrfToken, relative string
	replace := false
	fileCount := 0
	type uploadResult struct {
		Name, Result, Detail string
		Succeeded            bool
	}
	var results []uploadResult
	succeeded := 0
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			http.Error(response, "读取上传请求失败："+nextErr.Error(), http.StatusBadRequest)
			return
		}
		if part.FileName() == "" {
			value, readErr := io.ReadAll(io.LimitReader(part, 64<<10))
			_ = part.Close()
			if readErr != nil {
				http.Error(response, "读取上传字段失败", http.StatusBadRequest)
				return
			}
			switch part.FormName() {
			case "csrf_token":
				csrfToken = string(value)
			case "path":
				relative = string(value)
			case "replace":
				replace = string(value) == "yes"
			}
			continue
		}
		current := request.Context().Value(sessionContextKey).(session)
		if subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(csrfToken)) != 1 {
			_ = part.Close()
			http.Error(response, "CSRF Token 无效", http.StatusForbidden)
			return
		}
		fileCount++
		if fileCount > 100 {
			_ = part.Close()
			http.Error(response, "单批最多上传 100 个文件", http.StatusRequestEntityTooLarge)
			return
		}
		filename := part.FileName()
		targetPath := pathpkg.Join(filepath.ToSlash(relative), filename)
		if a.runs.ConflictsPath(targetPath) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: "失败", Detail: "活动 Run 持有该上传目标的 Run Lease"})
			a.recordAudit("upload_file", filename, "rejected", request.RemoteAddr)
			continue
		}
		storedID, idErr := randomToken(18)
		if idErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: "失败", Detail: "无法创建上传事务"})
			continue
		}
		trashed, uploadErr := a.managed.Upload(relative, filename, part, 1<<30, replace, storedID)
		if uploadErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: "失败", Detail: uploadErr.Error()})
			a.recordAudit("upload_file", filename, "rejected", request.RemoteAddr)
			continue
		}
		_ = part.Close()
		if trashed != nil {
			_, err = a.db.Exec("INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, 0)", storedID, trashed.OriginalPath, trashed.StoredName, time.Now().UTC().Unix(), trashed.Size)
			if err != nil {
				_ = a.managed.RollbackTextSave(targetPath, storedID)
				results = append(results, uploadResult{Name: filename, Result: "失败", Detail: "替换已回滚：无法记录旧文件"})
				a.recordAudit("upload_file", filename, "failed", request.RemoteAddr)
				continue
			}
		}
		a.recordAudit("upload_file", filename, "succeeded", request.RemoteAddr)
		results = append(results, uploadResult{Name: filename, Result: "成功", Detail: "文件已保存", Succeeded: true})
		succeeded++
	}
	if fileCount == 0 {
		http.Error(response, "未选择上传文件", http.StatusBadRequest)
		return
	}
	if succeeded > 0 {
		if err := a.checkpointWebMutation("upload", relative); err != nil {
			results = append(results, uploadResult{Name: "Version Protection", Result: "失败", Detail: "文件已上传，但 checkpoint 失败：" + err.Error()})
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if succeeded < fileCount || len(results) > fileCount {
		response.WriteHeader(http.StatusMultiStatus)
	}
	if err := uploadResultsTemplate.Execute(response, struct {
		Link    string
		Results []uploadResult
	}{Link: filesURL(relative), Results: results}); err != nil {
		http.Error(response, "文件已上传，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) filesPage(response http.ResponseWriter, request *http.Request) {
	relative := strings.Trim(request.PathValue("path"), "/")
	entries, err := a.managed.List(relative)
	if err != nil {
		http.Error(response, "无法读取受管根目录："+err.Error(), http.StatusInternalServerError)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if query != "" {
		filtered := entries[:0]
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Name), strings.ToLower(query)) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	sortField, direction := request.URL.Query().Get("sort"), request.URL.Query().Get("direction")
	if sortField != "" {
		sort.SliceStable(entries, func(i, j int) bool {
			comparison := strings.Compare(strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name))
			switch sortField {
			case "size":
				comparison = cmp.Compare(entries[i].Size, entries[j].Size)
			case "modified":
				comparison = entries[i].ModifiedAt.Compare(entries[j].ModifiedAt)
			}
			if direction == "desc" {
				return comparison > 0
			}
			return comparison < 0
		})
	}
	page := 1
	if parsed, parseErr := strconv.Atoi(request.URL.Query().Get("page")); parseErr == nil && parsed > 0 {
		page = parsed
	}
	const pageSize = 100
	start := min((page-1)*pageSize, len(entries))
	end := min(start+pageSize, len(entries))
	type fileView struct {
		managedfiles.Entry
		Path, BrowseURL, DownloadURL, EditURL, PreviewURL string
		Protection                                        string
	}
	views := make([]fileView, 0, end-start)
	for _, entry := range entries[start:end] {
		path := pathpkg.Join(relative, entry.Name)
		view := fileView{Entry: entry, Path: path}
		if entry.Kind == managedfiles.Directory {
			view.BrowseURL = filesURL(path)
		} else if entry.Kind == managedfiles.Regular {
			view.Protection = a.gitProtection.ProtectionReason(path, entry.Size)
			view.DownloadURL = routeFileURL("/files/download/", path)
			view.EditURL = routeFileURL("/files/edit/", path)
			switch strings.ToLower(filepath.Ext(path)) {
			case ".png", ".jpg", ".jpeg", ".gif", ".webp":
				view.PreviewURL = routeFileURL("/files/preview/", path)
			}
		}
		views = append(views, view)
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = filesTemplate.Execute(response, struct {
		Entries             []fileView
		CSRFToken           string
		CurrentPath         string
		Query               string
		Page                int
		PreviousPage        int
		NextPage            int
		HasPrevious         bool
		HasNext             bool
		CanToggleExecutable bool
	}{Entries: views, CSRFToken: current.csrfToken, CurrentPath: relative, Query: query, Page: page, PreviousPage: page - 1, NextPage: page + 1, HasPrevious: page > 1, HasNext: end < len(entries), CanToggleExecutable: runtime.GOOS == "linux"})
}

func routeFileURL(prefix, relative string) string {
	parts := strings.Split(pathpkg.Clean(filepath.ToSlash(relative)), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return prefix + strings.Join(parts, "/")
}

func filesURL(relative string) string {
	if relative == "" {
		return "/files/"
	}
	parts := strings.Split(pathpkg.Clean(filepath.ToSlash(relative)), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return "/files/" + strings.Join(parts, "/") + "/"
}

func (a *App) createDirectory(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := a.managed.CreateDirectory(request.FormValue("path"), request.FormValue("name")); err != nil {
		http.Error(response, "无法创建目录："+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.checkpointWebMutation("create-directory", request.FormValue("name")); err != nil {
		http.Error(response, "目录已创建，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("create_directory", request.FormValue("name"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, filesURL(request.FormValue("path")), http.StatusSeeOther)
}

func (a *App) changePassword(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	if subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(request.FormValue("csrf_token"))) != 1 {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}

	var username, passwordHash string
	if err := a.db.QueryRow("SELECT username, password_hash FROM admin WHERE id = 1").Scan(&username, &passwordHash); err != nil {
		http.Error(response, "无法读取管理员账号", http.StatusInternalServerError)
		return
	}
	if !verifyPassword(request.FormValue("current_password"), passwordHash) {
		http.Error(response, "当前密码错误", http.StatusUnauthorized)
		return
	}
	newUsername := strings.TrimSpace(request.FormValue("username"))
	if newUsername == "" {
		newUsername = username
	}
	if !utf8.ValidString(newUsername) || utf8.RuneCountInString(newUsername) > 64 || strings.ContainsAny(newUsername, "\r\n\x00") {
		http.Error(response, "用户名必须为 1 至 64 个有效 Unicode 字符", http.StatusBadRequest)
		return
	}
	newPassword := request.FormValue("new_password")
	if newPassword != request.FormValue("confirm_password") {
		http.Error(response, "两次输入的新密码不一致", http.StatusBadRequest)
		return
	}
	if !utf8.ValidString(newPassword) || utf8.RuneCountInString(newPassword) < 12 || len([]byte(newPassword)) > 256 || newPassword == newUsername {
		http.Error(response, "密码必须至少包含 12 个 Unicode 字符、不超过 256 个 UTF-8 字节，且不能与用户名相同", http.StatusBadRequest)
		return
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.Exec("UPDATE admin SET username = ?, password_hash = ?, must_change_password = 0 WHERE id = 1", newUsername, newHash); err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		http.Error(response, "无法撤销会话", http.StatusInternalServerError)
		return
	}
	passwordPath := filepath.Join(a.stateRoot, "secrets", initialPasswordFilename)
	if err := os.Remove(passwordPath); err != nil && !os.IsNotExist(err) {
		http.Error(response, "无法删除一次性密码文件", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	a.recordAudit("change_credentials", newUsername, "succeeded", request.RemoteAddr)
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) login(response http.ResponseWriter, request *http.Request) {
	csrfCookie, err := request.Cookie(loginCSRFCookieName)
	if err != nil || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(request.FormValue("csrf_token"))) != 1 {
		renderLoginPage(response, request, http.StatusForbidden, request.FormValue("username"), "登录页面已过期，请重试")
		return
	}
	remoteHost, _, splitErr := net.SplitHostPort(request.RemoteAddr)
	if splitErr != nil {
		remoteHost = request.RemoteAddr
	}
	loginKeys := []string{"ip\x00" + remoteHost, "account\x00admin"}
	if retryAfter := a.loginRetryAfter(loginKeys...); retryAfter > 0 {
		response.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		a.recordAudit("login", "admin", "rate_limited", request.RemoteAddr)
		renderLoginPage(response, request, http.StatusTooManyRequests, request.FormValue("username"), "登录尝试过于频繁，请稍后重试")
		return
	}

	var username, passwordHash string
	var mustChange bool
	err = a.db.QueryRow("SELECT username, password_hash, must_change_password FROM admin WHERE id = 1").Scan(
		&username,
		&passwordHash,
		&mustChange,
	)
	if err != nil {
		renderLoginPage(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	if request.FormValue("username") != username || !verifyPassword(request.FormValue("password"), passwordHash) {
		a.recordLoginFailure(loginKeys...)
		a.recordAudit("login", "admin", "failed", request.RemoteAddr)
		renderLoginPage(response, request, http.StatusUnauthorized, request.FormValue("username"), "用户名或密码错误")
		return
	}
	a.clearLoginFailures(loginKeys...)

	token, err := randomToken(32)
	if err != nil {
		renderLoginPage(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	sessionCSRF, err := randomToken(32)
	if err != nil {
		renderLoginPage(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	now := time.Now().UTC()
	if _, err := a.db.Exec(
		"INSERT INTO sessions (token_hash, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		hashToken(token), sessionCSRF, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix(),
	); err != nil {
		renderLoginPage(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})
	http.SetCookie(response, &http.Cookie{Name: loginCSRFCookieName, Path: "/login", MaxAge: -1})
	a.recordAudit("login", "admin", "succeeded", request.RemoteAddr)
	if mustChange {
		http.Redirect(response, request, "/settings/account", http.StatusSeeOther)
		return
	}
	http.Redirect(response, request, "/files/", http.StatusSeeOther)
}

func (a *App) logout(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(cookie.Value))
	}
	a.recordAudit("logout", "admin", "succeeded", request.RemoteAddr)
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) loginRetryAfter(keys ...string) time.Duration {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	var longest time.Duration
	for _, key := range keys {
		if remaining := time.Until(a.loginFailures[key].blockedUntil); remaining > longest {
			longest = remaining
		}
	}
	return longest
}

func (a *App) recordLoginFailure(keys ...string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	for _, key := range keys {
		failure := a.loginFailures[key]
		failure.count++
		if failure.count >= 5 {
			exponent := failure.count - 5
			delay := 2 * time.Second
			if exponent >= 8 {
				delay = 5 * time.Minute
			} else {
				delay *= time.Duration(1 << exponent)
			}
			failure.blockedUntil = time.Now().Add(delay)
		}
		a.loginFailures[key] = failure
	}
}

func (a *App) clearLoginFailures(keys ...string) {
	a.loginMu.Lock()
	for _, key := range keys {
		delete(a.loginFailures, key)
	}
	a.loginMu.Unlock()
}

type session struct {
	csrfToken  string
	mustChange bool
}

func (a *App) loadSession(request *http.Request) (session, string, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return session{}, "", false
	}
	var current session
	var username string
	var lastSeen, expiresAt int64
	err = a.db.QueryRow(`
		SELECT sessions.csrf_token, sessions.last_seen_at, sessions.expires_at,
			admin.must_change_password, admin.username
		FROM sessions CROSS JOIN admin
		WHERE sessions.token_hash = ? AND admin.id = 1`, hashToken(cookie.Value),
	).Scan(&current.csrfToken, &lastSeen, &expiresAt, &current.mustChange, &username)
	now := time.Now().UTC()
	if err != nil || now.Unix() >= expiresAt || now.Sub(time.Unix(lastSeen, 0)) >= 12*time.Hour {
		if err == nil {
			_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(cookie.Value))
		}
		return session{}, "", false
	}
	return current, username, true
}

func validSessionCSRF(request *http.Request) bool {
	current, ok := request.Context().Value(sessionContextKey).(session)
	return ok && subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(request.FormValue("csrf_token"))) == 1
}

func (a *App) requireSession(allowPasswordChange bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, _, ok := a.loadSession(request)
		if !ok {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		cookie, _ := request.Cookie(sessionCookieName)
		now := time.Now().UTC()
		_, _ = a.db.Exec("UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", now.Unix(), hashToken(cookie.Value))
		if current.mustChange && !allowPasswordChange {
			http.Redirect(response, request, "/settings/account", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), sessionContextKey, current)))
	})
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (a *App) recordAudit(action, target, result, source string) {
	_, _ = a.db.Exec(
		"INSERT INTO audit_events (occurred_at, action, target, result, source_address) VALUES (?, ?, ?, ?, ?)",
		time.Now().UTC().Unix(), action, target, result, source,
	)
}

func (a *App) checkpointWebMutation(action, target string) error {
	state, err := a.gitProtection.State()
	if err != nil || !state.Enabled || a.runs.HasActive() {
		return err
	}
	return a.gitProtection.Checkpoint("ScriptBoard web checkpoint\n\nScriptBoard-Operation: " + action + "\nScriptBoard-Target: " + target)
}

type loginPageData struct {
	CSRFToken string
	Username  string
	Error     string
}

var applicationHeaderTemplate = template.Must(template.New("application-header").Parse(`<header class="app-header">
<div class="app-header__inner">
<a class="brand" href="/files/" aria-label="ScriptBoard 首页"><span class="brand__mark">&gt;_</span><span class="brand__word">ScriptBoard</span></a>
<nav class="app-nav" aria-label="主导航">{{range .Navigation}}<a href="{{.Href}}" {{if .Current}}aria-current="page"{{end}}>{{.Label}}</a>{{end}}</nav>
<div class="app-user"><span class="app-status">{{.Environment}} · {{.ActiveRuns}} 个运行</span><a href="/settings/account">{{.Username}}</a><form method="post" action="/logout"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit">退出</button></form></div>
</div></header>`))

var applicationErrorTemplate = template.Must(template.New("application-error").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>操作未完成 · ScriptBoard</title></head>
<body><main class="error-page"><p class="error-code">HTTP {{.Status}}</p><h1>操作未完成</h1><div class="page-error" role="alert">{{.Message}}</div><p><a class="error-return" href="{{.Destination}}">{{.Label}}</a></p></main></body></html>`))

func renderLoginPage(response http.ResponseWriter, request *http.Request, status int, username, errorMessage string) {
	token, err := randomToken(32)
	if err != nil {
		http.Error(response, "无法创建登录表单", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    token,
		Path:     "/login",
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteStrictMode,
	})
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = loginTemplate.Execute(response, loginPageData{CSRFToken: token, Username: username, Error: errorMessage})
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>登录 · ScriptBoard</title></head>
<body class="login-page"><main><h1>登录</h1>
{{if .Error}}<div class="login-error" role="alert"><strong>登录失败</strong><span>{{.Error}}</span></div>{{end}}
<form method="post" action="/login">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>用户名 <input name="username" {{if not .Error}}autofocus {{end}}value="{{.Username}}" autocomplete="username" required></label>
<label>密码 <input name="password" type="password" autocomplete="current-password" required {{if .Error}}autofocus{{end}}></label>
<button type="submit">登录</button>
</form></main></body>
</html>`))

var accountTemplate = template.Must(template.New("account").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>账户设置 · ScriptBoard</title></head>
<body><main><h1>账户设置</h1>
{{if .CredentialOverride}}<p>当前实例配置了启动凭据覆盖；此处修改只在下次重启前有效。要永久保留网页修改，请移除启动配置中的覆盖值。</p>{{end}}
<form method="post" action="/settings/account">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>用户名 <input name="username" value="{{.Username}}" autocomplete="username" required></label>
<label>当前密码 <input name="current_password" type="password" autocomplete="current-password" required></label>
<label>新密码 <input name="new_password" type="password" autocomplete="new-password" required></label>
<label>确认新密码 <input name="confirm_password" type="password" autocomplete="new-password" required></label>
<button type="submit">保存账户凭据</button>
</form>
</main></body>
</html>`))

var filesTemplate = template.Must(template.New("files").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>文件 · ScriptBoard</title></head>
<body><main><h1>文件</h1>
<form method="get"><label>搜索当前目录 <input name="q" value="{{.Query}}"></label><select name="sort"><option value="">自然顺序</option><option value="name">名称</option><option value="size">大小</option><option value="modified">修改时间</option></select><select name="direction"><option value="asc">升序</option><option value="desc">降序</option></select><button>筛选</button></form>
<form method="post" action="/files/mkdir"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label>目录名 <input name="name" required></label><button type="submit">新建目录</button></form>
<form method="post" action="/files/upload" enctype="multipart/form-data"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label><input name="replace" type="checkbox" value="yes">同名时替换并将旧文件移入回收站</label><label>文件 <input name="files" type="file" multiple required></label><button type="submit">上传</button></form>
<table><thead><tr><th>名称</th><th>类型</th><th>大小</th><th>版本保护</th><th>修改时间</th><th>操作</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{if .BrowseURL}}<a href="{{.BrowseURL}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</td><td>{{if eq .Kind "directory"}}目录{{else if eq .Kind "regular"}}文件{{else}}受限{{end}}</td><td>{{.Size}}</td><td>{{.Protection}}</td><td>{{.ModifiedAt}}</td><td>{{if .PreviewURL}}<a href="{{.PreviewURL}}"><img src="{{.PreviewURL}}" alt="{{.Name}}" width="96" loading="lazy"></a>{{end}}{{if .DownloadURL}}<a href="{{.DownloadURL}}">下载</a> <a href="{{.EditURL}}">编辑</a>{{end}}{{if ne .Kind "restricted"}}<form method="post" action="/runs/start"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="script" value="{{.Path}}">{{if eq .Kind "regular"}}<button>运行</button>{{end}}</form><form method="post" action="/files/move"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="source" value="{{.Path}}"><input name="destination" placeholder="新路径" required><button>移动/重命名</button></form>{{if and $.CanToggleExecutable (eq .Kind "regular")}}<form method="post" action="/files/toggle-executable"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="path" value="{{.Path}}">切换 owner execute</button></form>{{end}}<form method="post" action="/files/delete"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="path" value="{{.Path}}">移入回收站</button></form>{{end}}</td></tr>
{{else}}<tr><td colspan="6">目录为空</td></tr>{{end}}
</tbody></table><p>第 {{.Page}} 页 {{if .HasPrevious}}<a href="?q={{urlquery .Query}}&page={{.PreviousPage}}">上一页</a>{{end}} {{if .HasNext}}<a href="?q={{urlquery .Query}}&page={{.NextPage}}">下一页</a>{{end}}</p></main></body>
</html>`))

var uploadResultsTemplate = template.Must(template.New("upload-results").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>上传结果 · ScriptBoard</title></head><body><main><h1>上传结果</h1><table><thead><tr><th>文件</th><th>结果</th><th>详情</th></tr></thead><tbody>{{range .Results}}<tr><td>{{.Name}}</td><td>{{.Result}}</td><td>{{.Detail}}</td></tr>{{end}}</tbody></table><p><a href="{{.Link}}">返回文件列表</a></p></main></body></html>`))

var deleteImpactTemplate = template.Must(template.New("delete-impact").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>确认引用影响 · ScriptBoard</title></head><body><main><h1>确认引用影响</h1><p>删除 {{.Path}} 将使 {{.QuickRuns}} 个快捷执行路径失效，并停用 {{.Schedules}} 个计划。恢复文件不会自动重新启用计划。</p><form method="post" action="/files/delete"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.Path}}"><button name="confirm_references" value="yes">确认移入回收站</button></form></main></body></html>`))

var trashTemplate = template.Must(template.New("trash").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>回收站 · ScriptBoard</title></head>
<body><main><h1>回收站</h1><table><thead><tr><th>原路径</th><th>删除时间</th><th>大小</th><th>操作</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{.OriginalPath}}</td><td>{{.DeletedAt}}</td><td>{{.Size}}</td><td><form method="post" action="/trash/restore"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">恢复</button></form><form method="post" action="/trash/purge"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button name="confirm" value="yes" type="submit">永久清理</button></form></td></tr>
{{else}}<tr><td colspan="4">回收站为空</td></tr>{{end}}
</tbody></table></main></body></html>`))

var textEditorTemplate = template.Must(template.New("text-editor").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>编辑 {{.Path}} · ScriptBoard</title></head>
<body><main><h1>编辑 {{.Path}}</h1><form method="post" action="/files/edit/{{.Path}}">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="digest" value="{{.Digest}}">
<textarea name="content" required>{{.Content}}</textarea><button type="submit">保存</button>
</form></main></body></html>`))

var runTemplate = template.Must(template.New("run").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app-v2.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>运行 {{.Run.ID}} · ScriptBoard</title></head>
<body><main data-run-events-url="/runs/{{.Run.ID}}/events"><h1>运行 {{.Run.ID}}</h1><dl><dt>脚本</dt><dd>{{.Run.ScriptPath}}</dd><dt>状态</dt><dd data-run-status>{{.Run.Status}}</dd><dt>来源</dt><dd>{{.Run.SourceType}} / {{.Run.SourceName}}</dd><dt>运行身份</dt><dd>{{.Run.RuntimeIdentity}}</dd><dt>执行器</dt><dd>{{.Run.Executor}}</dd><dt>SHA-256</dt><dd>{{.Run.ScriptDigest}}</dd></dl>
{{if .Run.Error}}<p>{{.Run.Error}}</p>{{end}}{{if .Run.LogExpired}}<p>运行日志已按保留策略清理。</p>{{end}}{{if .Run.LogIncomplete}}<p>运行日志写入不完整。</p>{{end}}{{if .Run.LogTruncated}}<p>运行日志已达到上限，丢弃 {{.Run.DroppedBytes}} 字节。</p>{{end}}{{if or (eq .Run.Status "running") (eq .Run.Status "stopping")}}<form data-run-stop-form method="post" action="/runs/{{.Run.ID}}/stop"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit">{{if eq .Run.Status "stopping"}}强制停止{{else}}停止{{end}}</button></form>{{end}}<form method="post" action="/runs/{{.Run.ID}}/quick-run"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>快捷执行名称 <input name="name" required></label><button type="submit">保存快捷执行</button></form><p><button type="button" data-run-pause>暂停显示</button> <span data-run-live-state>正在连接实时输出…</span></p><pre data-run-log>{{range .Run.Events}}<span data-sequence="{{.Sequence}}" data-source="{{.Source}}" {{if .EncodingError}}data-encoding-error="true" title="输出包含无效 UTF-8，已替换显示"{{end}}>{{.Data}}</span>{{end}}</pre>
</main></body></html>`))

var runsTemplate = template.Must(template.New("runs").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>运行记录 · ScriptBoard</title></head><body><main><h1>运行记录</h1><table><thead><tr><th>时间</th><th>脚本</th><th>来源</th><th>状态</th><th>执行器</th></tr></thead><tbody>{{range .}}<tr><td>{{.CreatedAt}}</td><td><a href="/runs/{{.ID}}">{{.ScriptPath}}</a></td><td>{{.SourceType}} / {{.SourceName}}</td><td>{{.Status}}</td><td>{{.Executor}}</td></tr>{{else}}<tr><td colspan="5">暂无运行记录</td></tr>{{end}}</tbody></table></main></body></html>`))

var overlapTemplate = template.Must(template.New("overlap").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>确认并发运行 · ScriptBoard</title></head><body><main><h1>确认并发运行</h1><p>{{.Script}} 已有活动运行。确认后将并发启动另一个运行。</p><form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="script" value="{{.Script}}"><input type="hidden" name="arguments" value="{{.Arguments}}"><input type="hidden" name="timeout_seconds" value="{{.Timeout}}"><button name="confirm_overlap" value="yes">确认并发启动</button></form></main></body></html>`))

var quickRunsTemplate = template.Must(template.New("quick-runs").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>快捷执行 · ScriptBoard</title></head><body><main><h1>快捷执行</h1>
<table><thead><tr><th>名称</th><th>脚本</th><th>参数</th><th>操作</th></tr></thead><tbody>{{range .QuickRuns}}<tr><td>{{.Name}}</td><td>{{.ScriptPath}}</td><td>{{.ArgumentsTemplate}}</td><td>
<form method="post" action="/quick-runs/{{.ID}}/start"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button type="submit" {{if not .Valid}}disabled title="Script 路径已失效"{{end}}>{{if .Valid}}启动{{else}}路径失效{{end}}</button></form>
<form method="post" action="/quick-runs/{{.ID}}/move"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="direction" value="up">上移</button><button name="direction" value="down">下移</button></form>
<form method="post" action="/quick-runs/{{.ID}}/delete"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="confirm" value="yes">删除</button></form>
</td></tr>{{else}}<tr><td colspan="4">暂无快捷执行</td></tr>{{end}}</tbody></table>
</main></body></html>`))

var schedulesTemplate = template.Must(template.New("schedules").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>计划 · ScriptBoard</title></head><body><main><h1>计划</h1>
<form method="post" action="/schedules"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>名称 <input name="name" required></label><label>脚本 <input name="script" required></label><label>参数 <input name="arguments"></label><label>五段 cron <input name="expression" list="cron-presets" placeholder="* * * * *" required><datalist id="cron-presets"><option value="*/5 * * * *">每 5 分钟</option><option value="0 * * * *">每小时</option><option value="0 0 * * *">每天</option><option value="0 0 * * 1">每周一</option><option value="0 0 1 * *">每月首日</option></datalist></label><label>超时秒数 <input name="timeout_seconds" type="number" min="0" max="86400"></label><label><input name="disallow_overlap" type="checkbox" value="1">禁止重叠</label><button type="submit">创建</button></form>
<table><thead><tr><th>配置</th><th>未来五次触发</th><th>最近结果</th><th>操作</th></tr></thead><tbody>{{range .Schedules}}<tr><td><form method="post" action="/schedules/{{.ID}}/update"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input name="name" value="{{.Name}}" required><input name="script" value="{{.ScriptPath}}" required><input name="arguments" value="{{.ArgumentsTemplate}}"><input name="expression" value="{{.Expression}}" required><input name="timeout_seconds" type="number" value="{{.TimeoutSeconds}}"><label><input name="disallow_overlap" type="checkbox" value="1" {{if not .AllowOverlap}}checked{{end}}>禁止重叠</label><button>保存</button></form></td><td><ol>{{range .NextFive}}<li>{{.}}</li>{{end}}</ol></td><td>{{.LastResult}}{{if .LastError}}<br>{{.LastError}}{{end}}<input type="hidden" name="last_run_id" value="{{.LastRunID}}"></td><td><form method="post" action="/schedules/{{.ID}}/toggle"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="enabled" value="{{if .Enabled}}0{{else}}1{{end}}">{{if .Enabled}}停用{{else}}启用{{end}}</button></form><form method="post" action="/schedules/{{.ID}}/delete"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="confirm" value="yes">删除</button></form></td></tr>{{else}}<tr><td colspan="4">暂无计划</td></tr>{{end}}</tbody></table>
</main></body></html>`))

var auditTemplate = template.Must(template.New("audit").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>审计事件 · ScriptBoard</title></head><body><main><h1>审计事件</h1><p><a href="/audit.csv">下载 CSV</a></p>
<table><thead><tr><th>时间</th><th>操作</th><th>目标</th><th>结果</th><th>来源</th></tr></thead><tbody>{{range .}}<tr><td>{{.OccurredAt}}</td><td>{{.Action}}</td><td>{{.Target}}</td><td>{{.Result}}</td><td>{{.Source}}</td></tr>{{else}}<tr><td colspan="5">暂无审计事件</td></tr>{{end}}</tbody></table>
</main></body></html>`))

var versionProtectionTemplate = template.Must(template.New("version-protection").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>版本保护 · ScriptBoard</title></head><body><main><h1>版本保护</h1>
<dl><dt>状态</dt><dd>{{.State.Status}}</dd><dt>仓库字节数</dt><dd>{{.State.RepositoryBytes}}{{if .State.StorageWarning}}（已超过容量上限的 80%）{{end}}</dd><dt>最近提交</dt><dd>{{.State.LastCommit}}</dd>{{if .State.AbnormalReason}}<dt>异常</dt><dd>{{.State.AbnormalReason}}</dd>{{end}}</dl>
{{if not .State.Enabled}}<form method="post" action="/settings/version-protection/enable"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label><input type="checkbox" name="confirm" value="yes" required>确认启用或重新启用本地 Git 保护</label><button type="submit">启用</button></form><form method="post" action="/settings/version-protection/adopt"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><p>若 Managed Root 已是干净且安全的 Git 仓库，可明确接管。</p><button name="confirm" value="adopt-clean-repository">接管已有仓库</button></form>{{else}}<form method="post" action="/settings/version-protection/checkpoint"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit">立即创建 checkpoint</button></form><form method="get"><label>文件路径 <input name="path" value="{{.HistoryPath}}"></label><button>查看历史</button></form>{{if .History}}<table><tbody>{{range .History}}<tr><td>{{.Time}}</td><td>{{.Hash}}</td><td>{{.Subject}}</td></tr>{{end}}</tbody></table>{{end}}<form method="post" action="/settings/version-protection/restore"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>路径 <input name="path" required></label><label>Commit <input name="commit" required></label><button type="submit">恢复单个文件</button></form><form method="post" action="/settings/version-protection/disable"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button name="confirm" value="yes">停用（保留历史）</button></form>{{end}}
</main></body></html>`))

var variablesTemplate = template.Must(template.New("variables").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=5"><script defer src="/assets/app-v2.js?v=5"></script><title>变量 · ScriptBoard</title></head>
<body><main><h1>变量</h1><form method="post" action="/variables"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>名称 <input name="name" required></label><label>值 <textarea name="value"></textarea></label><button type="submit">创建</button></form>
<table><thead><tr><th>名称</th><th>值</th><th>操作</th></tr></thead><tbody>{{range .Variables}}<tr><td colspan="2"><form method="post" action="/variables/{{.Name}}/update"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input name="name" value="{{.Name}}" required><textarea name="value">{{.Value}}</textarea><button>保存</button></form></td><td><form method="post" action="/variables/{{.Name}}/delete"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><button name="confirm" value="yes">删除</button></form></td></tr>{{else}}<tr><td colspan="3">暂无变量</td></tr>{{end}}</tbody></table>
</main></body></html>`))
