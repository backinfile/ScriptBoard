package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/diskspace"
	"scriptboard/internal/gitprotect"
	"scriptboard/internal/hoststatus"
	"scriptboard/internal/instancelock"
	"scriptboard/internal/managedfiles"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/scheduler"
	updatepkg "scriptboard/internal/update"
	"scriptboard/internal/websitemonitor"
)

const initialPasswordFilename = "initial-admin-password"
const currentSchemaVersion = buildinfo.DatabaseSchemaVersion

//go:embed web/assets/* web/templates/*
var webFiles embed.FS

func mustWebAsset(path string) string {
	content, err := webFiles.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(content)
}

func mustWebTemplate(name string) *template.Template {
	path := "web/templates/" + name + ".html"
	return template.Must(template.New(name).Funcs(webTemplateFunctions()).Parse(
		mustWebAsset("web/templates/deferred-region.html") + mustWebAsset(path),
	))
}

func webTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"assetVersion": func() string { return webAssetVersion },
		"displayTime": func(input any) string {
			value, ok := input.(time.Time)
			if pointer, pointerOK := input.(*time.Time); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
			if !ok {
				return "-"
			}
			if value.IsZero() {
				return "-"
			}
			return value.Local().Format("2006-01-02 15:04:05")
		},
		"machineTime": func(value time.Time) string {
			if value.IsZero() {
				return ""
			}
			return value.UTC().Format(time.RFC3339)
		},
		"humanBytes":         humanBytes,
		"humanRate":          func(value float64) string { return humanBytes(uint64(math.Max(0, value))) + "/s" },
		"percent":            func(value float64) string { return fmt.Sprintf("%.1f%%", value) },
		"applicationSortURL": applicationSortURL,
		"duration":           humanDuration,
		"localDuration": func(locale webLocale, value time.Duration) string {
			if locale == localeSimplifiedChinese {
				return humanDuration(value)
			}
			if value < 0 {
				value = 0
			}
			days := int(value / (24 * time.Hour))
			value %= 24 * time.Hour
			hours := int(value / time.Hour)
			value %= time.Hour
			minutes := int(value / time.Minute)
			if days > 0 {
				return fmt.Sprintf("%d d %d hr", days, hours)
			}
			if hours > 0 {
				return fmt.Sprintf("%d hr %d min", hours, minutes)
			}
			return fmt.Sprintf("%d min", minutes)
		},
		"slice": func(values ...string) []string { return values },
		"jsonText": func(value json.RawMessage) string {
			var output bytes.Buffer
			if json.Indent(&output, value, "", "  ") == nil {
				return output.String()
			}
			return string(value)
		},
		"deref": func(value *float64) float64 {
			if value == nil {
				return 0
			}
			return *value
		},
		"t": func(locale webLocale, key string) string {
			return webText(locale, key)
		},
		"localTime": func(locale webLocale, input any) string {
			value, ok := input.(time.Time)
			if pointer, pointerOK := input.(*time.Time); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
			if !ok || value.IsZero() {
				return webText(locale, "common.not_available")
			}
			if locale == localeSimplifiedChinese {
				return value.Local().Format("2006年01月02日 15:04")
			}
			return value.Local().Format("Jan 2, 2006 15:04")
		},
		"instanceMachineTime": func(value time.Time) string {
			if value.IsZero() {
				return ""
			}
			return value.Format(time.RFC3339)
		},
		"instanceTime": func(locale webLocale, value time.Time) string {
			if value.IsZero() {
				return webText(locale, "common.not_available")
			}
			if locale == localeSimplifiedChinese {
				return value.Format("2006年1月2日 15:04")
			}
			return value.Format("Jan 2, 2006 15:04")
		},
		"statusText": func(locale webLocale, status string) string {
			if label := webText(locale, "run.status."+status); label != "run.status."+status {
				return label
			}
			return status
		},
		"runSourceText": func(locale webLocale, source string) string {
			key := map[string]string{
				"manual":             "run.source.manual",
				"admin/manual":       "run.source.manual",
				"quick_run":          "run.source.quick_run",
				"admin/quick-run":    "run.source.quick_run",
				"schedule":           "run.source.scheduled",
				"scheduler":          "run.source.scheduled",
				"admin/schedule-now": "run.source.schedule_now",
			}[source]
			if key == "" {
				return source
			}
			return webText(locale, key)
		},
		"resultText": func(locale webLocale, result string) string {
			if label := webText(locale, "result."+result); label != "result."+result {
				return label
			}
			return result
		},
		"roleText": func(locale webLocale, role string) string {
			if label := webText(locale, "storage.role."+role); label != "storage.role."+role {
				return label
			}
			return role
		},
	}
}

func humanBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}

func humanDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	days := int(value / (24 * time.Hour))
	value %= 24 * time.Hour
	hours := int(value / time.Hour)
	value %= time.Hour
	minutes := int(value / time.Minute)
	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
	}
	return fmt.Sprintf("%d 分钟", minutes)
}

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
	ManagedRoot           string
	StateRoot             string
	RunTimeoutGrace       time.Duration
	SchedulerNow          func() time.Time
	SchedulerTick         time.Duration
	GitExecutable         string
	ExecutorChains        map[string][]string
	AdminUsername         string
	AdminPassword         string
	AdminPasswordFile     string
	TrustedProxies        []string
	WebsiteMonitorOptions websitemonitor.Options
	UpdateCheck           bool
	UpdateInterval        time.Duration
	UpdateSource          updatepkg.ReleaseSource
	RequestShutdown       func()
	ApplicationProbe      appstatus.Probe
}

type App struct {
	db                 *sql.DB
	stateRoot          string
	managedRoot        string
	managed            *managedfiles.Store
	runs               *runmanager.Manager
	scheduler          *scheduler.Manager
	gitProtection      *gitprotect.Manager
	hostStatus         *hoststatus.Monitor
	applicationStatus  *appstatus.Monitor
	logStreamSlots     chan struct{}
	logHistorySlots    chan struct{}
	shellStatusCache   *shellStatusCache
	websiteMonitor     *websitemonitor.Manager
	instanceLock       *instancelock.Lock
	handler            http.Handler
	loginMu            sync.Mutex
	loginFailures      map[string]loginFailure
	credentialOverride bool
	trustedProxies     []*net.IPNet
	updates            *updatepkg.Manager
	updateCancel       context.CancelFunc
	updateContext      context.Context
	validation         atomic.Bool
	validationID       string
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
	validationID, validating := updatepkg.PendingValidation(stateRoot, buildinfo.Current())

	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		return nil, err
	}

	trustedProxies, err := parseTrustedProxies(config.TrustedProxies)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	application := &App{
		db: db, stateRoot: stateRoot, managedRoot: managedRoot,
		managed: managedfiles.Open(managedRoot), instanceLock: instanceLock,
		loginFailures: make(map[string]loginFailure), trustedProxies: trustedProxies,
		logStreamSlots: make(chan struct{}, 8), logHistorySlots: make(chan struct{}, 4),
	}
	if !validating {
		if err := application.initializeAdmin(stateRoot); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := application.applyCredentialOverride(config.AdminUsername, config.AdminPassword, config.AdminPasswordFile); err != nil {
			_ = db.Close()
			return nil, err
		}
		_, _ = db.Exec("DELETE FROM audit_events WHERE occurred_at < ?", time.Now().UTC().AddDate(-1, 0, 0).Unix())
	}
	timeoutGrace := config.RunTimeoutGrace
	if timeoutGrace <= 0 {
		timeoutGrace = 30 * time.Second
	}
	application.runs = runmanager.New(db, application.managed, stateRoot, timeoutGrace, config.ExecutorChains)
	if validating {
		if _, entered := application.runs.EnterMaintenance(); !entered {
			_ = db.Close()
			return nil, errors.New("validation mode cannot start while a Run is active")
		}
		application.validation.Store(true)
		application.validationID = validationID
	}
	if !validating {
		if cleaned, cleanupErr := application.runs.CleanupLogs(90*24*time.Hour, 1<<30); cleanupErr != nil {
			_ = db.Close()
			return nil, cleanupErr
		} else if cleaned > 0 {
			application.recordAudit("cleanup_run_logs", fmt.Sprintf("%d logs", cleaned), "succeeded", "system")
		}
	}
	application.gitProtection, err = gitprotect.New(db, managedRoot, config.GitExecutable, stateRoot)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	application.runs.SetLifecycle(application.gitProtection)
	if validating {
		application.scheduler = scheduler.NewPaused(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick)
	} else {
		application.scheduler = scheduler.New(db, application.runs, application.loadVariables, config.SchedulerNow, config.SchedulerTick)
	}
	probe, _ := hoststatus.NewSystemProbe(managedRoot, stateRoot)
	application.hostStatus, err = hoststatus.New(db, probe, hoststatus.Options{SkipInitialCleanup: validating})
	if err != nil {
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	applicationProbe := config.ApplicationProbe
	if applicationProbe == nil {
		applicationProbe = appstatus.NewSystemProbe()
	}
	application.applicationStatus, err = appstatus.New(db, applicationProbe, appstatus.Options{})
	if err != nil {
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	application.websiteMonitor, err = websitemonitor.New(db, config.WebsiteMonitorOptions)
	if err != nil {
		application.applicationStatus.Close()
		application.hostStatus.Close()
		application.scheduler.Close()
		application.runs.Close()
		_ = db.Close()
		return nil, err
	}
	if !validating {
		application.hostStatus.Start(context.Background())
		application.applicationStatus.Start(context.Background())
	}
	application.updateContext, application.updateCancel = context.WithCancel(context.Background())
	application.updates = updatepkg.NewManager(updatepkg.ManagerConfig{
		StateRoot: stateRoot, CheckEnabled: config.UpdateCheck, CheckInterval: config.UpdateInterval,
		Source: config.UpdateSource, RequestShutdown: config.RequestShutdown,
	})
	if validating {
		go application.monitorUpdateValidation(validationID)
	} else {
		application.updates.Start(application.updateContext)
	}
	go application.monitorUpdateResults()
	application.shellStatusCache = newShellStatusCache(5*time.Second, time.Now, application.loadShellStatus)
	application.handler = application.routes(managedRoot)
	opened = true
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) ValidationOperationID() string {
	return a.validationID
}

func (a *App) beginUpdateMaintenance() (int, bool) {
	a.scheduler.PauseAndWait()
	active, entered := a.runs.EnterMaintenance()
	if !entered {
		a.scheduler.Resume()
		return active, false
	}
	return 0, true
}

func (a *App) endUpdateMaintenance() {
	a.runs.LeaveMaintenance()
	a.scheduler.Resume()
}

func (a *App) monitorUpdateValidation(operationID string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.updateContext.Done():
			return
		case <-ticker.C:
			operation, err := updatepkg.LoadOperation(a.stateRoot, operationID)
			if err != nil {
				continue
			}
			switch operation.Phase {
			case updatepkg.PhaseCommitted, updatepkg.PhaseRolledBack, updatepkg.PhaseFailedSafe:
				a.validation.Store(false)
				a.runs.LeaveMaintenance()
				a.scheduler.Resume()
				a.hostStatus.Start(context.Background())
				a.applicationStatus.Start(context.Background())
				a.updates.Start(a.updateContext)
				return
			}
		}
	}
}

func (a *App) monitorUpdateResults() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.updateContext.Done():
			return
		case <-ticker.C:
			if a.validation.Load() {
				continue
			}
			active, err := updatepkg.LoadActive(a.stateRoot)
			if err != nil {
				continue
			}
			operation, err := updatepkg.LoadOperation(a.stateRoot, active.OperationID)
			if err != nil {
				continue
			}
			var action, result string
			switch operation.Phase {
			case updatepkg.PhaseCommitted:
				action, result = "update_succeeded", "succeeded"
			case updatepkg.PhaseRolledBack:
				action, result = "update_rolled_back", "failed"
			case updatepkg.PhaseNeedsRecovery:
				action, result = "update_recovery_required", "failed"
			case updatepkg.PhaseFailedSafe:
				action, result = "update_failed_safe", "failed"
			default:
				continue
			}
			root, _ := updatepkg.OperationDirectory(a.stateRoot, operation.ID)
			imported := filepath.Join(root, "audit-imported")
			if _, err := os.Stat(imported); err == nil {
				continue
			}
			a.recordAudit(action, operation.TargetVersion, result, "system")
			_ = os.WriteFile(imported, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
		}
	}
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
	if _, err := transaction.Exec("UPDATE admin SET username = ?, password_hash = ?, must_change_password = 0 WHERE id = 1", username, hash); err != nil {
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
	if a.updateCancel != nil {
		a.updateCancel()
	}
	if a.hostStatus != nil {
		a.hostStatus.Close()
	}
	if a.applicationStatus != nil {
		a.applicationStatus.Close()
	}
	if a.websiteMonitor != nil {
		a.websiteMonitor.Close()
	}
	if a.scheduler != nil {
		a.scheduler.Close()
	}
	if a.runs != nil {
		a.runs.Close()
	}
	_, _ = a.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
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
			, source_id TEXT NOT NULL DEFAULT ''
			, runtime_identity TEXT NOT NULL DEFAULT ''
			, log_expired INTEGER NOT NULL DEFAULT 0
			, log_incomplete INTEGER NOT NULL DEFAULT 0
			, log_truncated INTEGER NOT NULL DEFAULT 0
			, dropped_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS variables (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			is_password INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_run_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS quick_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			script_path TEXT NOT NULL,
			arguments_template TEXT NOT NULL,
			timeout_seconds INTEGER NOT NULL,
			source_run_id TEXT REFERENCES runs(id),
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL,
			locked INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			group_id TEXT REFERENCES schedule_groups(id) ON DELETE SET NULL,
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
		`CREATE TABLE IF NOT EXISTS host_metric_minutes (
			bucket_at INTEGER PRIMARY KEY,
			sample_count INTEGER NOT NULL,
			average_json TEXT NOT NULL,
			maximum_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS application_pins (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('host', 'docker')),
			identity TEXT NOT NULL,
			name TEXT NOT NULL,
			technical TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (kind, identity)
		)`,
		`CREATE TABLE IF NOT EXISTS application_metric_minutes (
			application_id TEXT NOT NULL,
			bucket_at INTEGER NOT NULL,
			sample_count INTEGER NOT NULL,
			cpu_average REAL NOT NULL,
			cpu_maximum REAL NOT NULL,
			memory_average INTEGER NOT NULL,
			memory_maximum INTEGER NOT NULL,
			read_average REAL NOT NULL,
			read_maximum REAL NOT NULL,
			write_average REAL NOT NULL,
			write_maximum REAL NOT NULL,
			PRIMARY KEY (application_id, bucket_at)
		)`,
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化 SQLite: %w", err)
		}
	}
	for _, statement := range websitemonitor.SchemaStatements {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize Website Monitor SQLite schema: %w", err)
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
	if schemaVersion > 0 && schemaVersion < 7 {
		if _, err := migration.Exec("ALTER TABLE variables ADD COLUMN is_password INTEGER NOT NULL DEFAULT 0"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("migrate variable display types: %w", err)
		}
	}
	if schemaVersion > 0 && schemaVersion < 10 {
		for _, statement := range []string{
			`CREATE TABLE quick_runs_v10 (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				script_path TEXT NOT NULL,
				arguments_template TEXT NOT NULL,
				timeout_seconds INTEGER NOT NULL,
				source_run_id TEXT REFERENCES runs(id),
				sort_order INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			)`,
			`INSERT INTO quick_runs_v10
				(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
				SELECT id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at FROM quick_runs`,
			"DROP TABLE quick_runs",
			"ALTER TABLE quick_runs_v10 RENAME TO quick_runs",
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate file-created Quick Runs: %w", err)
			}
		}
	}
	if schemaVersion > 0 && schemaVersion < 11 {
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS quick_run_groups (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL COLLATE NOCASE UNIQUE,
				sort_order INTEGER NOT NULL,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			"ALTER TABLE quick_runs ADD COLUMN group_id TEXT REFERENCES quick_run_groups(id) ON DELETE SET NULL",
			"ALTER TABLE quick_runs ADD COLUMN locked INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE quick_runs ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0",
			"UPDATE quick_runs SET updated_at = created_at",
		} {
			if _, err := migration.Exec(statement); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Quick Run organization: %w", err)
			}
		}
	}
	if schemaVersion < 12 {
		var groupColumnExists int
		if err := migration.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name = 'group_name'`).Scan(&groupColumnExists); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect Schedule groups migration: %w", err)
		}
		if groupColumnExists == 0 {
			if _, err := migration.Exec("ALTER TABLE schedules ADD COLUMN group_name TEXT NOT NULL DEFAULT ''"); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Schedule groups: %w", err)
			}
		}
	}
	if schemaVersion < 13 {
		var sourceIDColumnExists int
		if err := migration.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name = 'source_id'`).Scan(&sourceIDColumnExists); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect Run source ID migration: %w", err)
		}
		if sourceIDColumnExists == 0 {
			if _, err := migration.Exec("ALTER TABLE runs ADD COLUMN source_id TEXT NOT NULL DEFAULT ''"); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Run source IDs: %w", err)
			}
		}
		if _, err := migration.Exec(`UPDATE runs
			SET source_id = COALESCE((
				SELECT schedule_id FROM schedule_triggers
				WHERE schedule_triggers.run_id = runs.id
				ORDER BY scheduled_for DESC
				LIMIT 1
			), '')
			WHERE source_id = '' AND source_type IN ('scheduler', 'admin/schedule-now')`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("backfill Run schedule IDs: %w", err)
		}
	}
	if schemaVersion < 14 {
		if _, err := migration.Exec(`CREATE TABLE IF NOT EXISTS schedule_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE UNIQUE,
			sort_order INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("create Schedule groups: %w", err)
		}
		var groupIDColumnExists int
		if err := migration.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name = 'group_id'`).Scan(&groupIDColumnExists); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("inspect Schedule group IDs migration: %w", err)
		}
		if groupIDColumnExists == 0 {
			if _, err := migration.Exec("ALTER TABLE schedules ADD COLUMN group_id TEXT REFERENCES schedule_groups(id) ON DELETE SET NULL"); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Schedule group IDs: %w", err)
			}
		}
		rows, err := migration.Query(`SELECT MIN(TRIM(group_name))
			FROM schedules
			WHERE deleted = 0 AND TRIM(group_name) <> ''
			GROUP BY LOWER(TRIM(group_name))
			ORDER BY MIN(created_at)`)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("read legacy Schedule groups: %w", err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				_ = db.Close()
				return nil, fmt.Errorf("scan legacy Schedule group: %w", err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = db.Close()
			return nil, fmt.Errorf("read legacy Schedule groups: %w", err)
		}
		_ = rows.Close()
		var sortOrder int
		if err := migration.QueryRow("SELECT COALESCE(MAX(sort_order), 0) FROM schedule_groups").Scan(&sortOrder); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("read Schedule group order: %w", err)
		}
		now := time.Now().UTC().Unix()
		for _, name := range names {
			var groupID string
			err := migration.QueryRow("SELECT id FROM schedule_groups WHERE name = ? COLLATE NOCASE", name).Scan(&groupID)
			if errors.Is(err, sql.ErrNoRows) {
				groupID, err = randomToken(18)
				if err == nil {
					sortOrder++
					_, err = migration.Exec(`INSERT INTO schedule_groups (id, name, sort_order, created_at, updated_at)
						VALUES (?, ?, ?, ?, ?)`, groupID, name, sortOrder, now, now)
				}
			}
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("migrate Schedule group %q: %w", name, err)
			}
			if _, err := migration.Exec(`UPDATE schedules SET group_id = ?
				WHERE group_id IS NULL AND deleted = 0 AND TRIM(group_name) = ? COLLATE NOCASE`,
				groupID, name); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("assign migrated Schedule group %q: %w", name, err)
			}
		}
	}
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS quick_run_groups_order_idx ON quick_run_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS quick_runs_group_order_idx ON quick_runs(group_id, sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_group_idx ON schedules(group_name, created_at)",
		"CREATE INDEX IF NOT EXISTS schedule_groups_order_idx ON schedule_groups(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS schedules_group_order_idx ON schedules(group_id, created_at)",
		"CREATE INDEX IF NOT EXISTS runs_source_idx ON runs(source_type, source_id, created_at DESC)",
		"CREATE INDEX IF NOT EXISTS application_pins_order_idx ON application_pins(sort_order, created_at)",
		"CREATE INDEX IF NOT EXISTS application_metric_minutes_bucket_idx ON application_metric_minutes(bucket_at)",
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("index Quick Run organization: %w", err)
		}
	}
	for _, statement := range []string{
		"DROP TABLE IF EXISTS ai_events",
		"DROP TABLE IF EXISTS ai_skill_usage",
		"DROP TABLE IF EXISTS ai_attachments",
		"DROP TABLE IF EXISTS ai_batch_actions",
		"DROP TABLE IF EXISTS ai_batches",
		"DROP TABLE IF EXISTS ai_turns",
		"DROP TABLE IF EXISTS ai_messages",
		"DROP TABLE IF EXISTS ai_history_summaries",
		"DROP TABLE IF EXISTS ai_conversations",
		"DROP TABLE IF EXISTS ai_profiles",
		"DROP TABLE IF EXISTS ai_settings",
	} {
		if _, err := migration.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("remove legacy AI storage: %w", err)
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
		"INSERT INTO admin (id, username, password_hash, must_change_password) VALUES (1, 'admin', ?, 0)",
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
	mux.HandleFunc("GET /assets/app-v2.css", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/css; charset=utf-8", appCSS)
	})
	mux.HandleFunc("GET /assets/app.css", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/css; charset=utf-8", appCSS)
	})
	mux.HandleFunc("GET /assets/app-v2.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", appJS)
	})
	mux.HandleFunc("GET /assets/markdown-it.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", markdownItJS)
	})
	mux.HandleFunc("GET /assets/purify.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", domPurifyJS)
	})
	mux.HandleFunc("GET /assets/highlight.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightJS)
	})
	mux.HandleFunc("GET /assets/highlight-powershell.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightPowerShellJS)
	})
	mux.HandleFunc("GET /assets/highlight-dos.min.js", func(response http.ResponseWriter, request *http.Request) {
		serveWebAsset(response, request, "text/javascript; charset=utf-8", highlightDOSJS)
	})
	mux.Handle("GET /{$}", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "/monitor", http.StatusSeeOther)
	})))
	mux.HandleFunc("GET /login", func(response http.ResponseWriter, request *http.Request) {
		if _, _, ok := a.loadSession(request); ok {
			http.Redirect(response, request, "/monitor", http.StatusSeeOther)
			return
		}
		renderLoginPage(response, request, http.StatusOK, "", "")
	})
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /settings/locale", a.setWebLocale)
	mux.Handle("POST /logout", a.requireSession(http.HandlerFunc(a.logout)))
	mux.Handle("GET /monitor", a.requireSession(http.HandlerFunc(a.overviewPage)))
	mux.Handle("GET /monitor/data", a.requireSession(http.HandlerFunc(a.overviewData)))
	mux.Handle("GET /monitor/status", a.requireSession(http.HandlerFunc(a.shellStatus)))
	mux.Handle("GET /monitor/applications", a.requireSession(http.HandlerFunc(a.applicationsPage)))
	mux.Handle("GET /monitor/applications/data", a.requireSession(http.HandlerFunc(a.applicationsData)))
	mux.Handle("GET /monitor/applications/{id}/logs", a.requireSession(http.HandlerFunc(a.applicationLogPage)))
	mux.Handle("GET /monitor/applications/{id}/logs/history", a.requireSession(http.HandlerFunc(a.applicationLogHistory)))
	mux.Handle("GET /monitor/applications/{id}/logs/events", a.requireSession(http.HandlerFunc(a.applicationLogEvents)))
	mux.Handle("GET /monitor/applications/{id}/details", a.requireSession(http.HandlerFunc(a.applicationDetails)))
	mux.Handle("POST /monitor/applications/{id}/pin", a.requireSession(http.HandlerFunc(a.pinApplication)))
	mux.Handle("POST /monitor/applications/{id}/unpin", a.requireSession(http.HandlerFunc(a.unpinApplication)))
	mux.Handle("POST /monitor/applications/{id}/move", a.requireSession(http.HandlerFunc(a.movePinnedApplication)))
	mux.Handle("GET /monitor/websites", a.requireSession(http.HandlerFunc(a.websiteMonitorList)))
	mux.Handle("GET /monitor/websites/data", a.requireSession(http.HandlerFunc(a.websiteMonitorData)))
	mux.Handle("GET /monitor/websites/new", a.requireSession(http.HandlerFunc(a.websiteMonitorCreateTask)))
	mux.Handle("POST /monitor/websites", a.requireSession(http.HandlerFunc(a.createWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/reorder", a.requireSession(http.HandlerFunc(a.reorderWebsiteMonitors)))
	mux.Handle("GET /monitor/websites/nginx", a.requireSession(http.HandlerFunc(a.websiteMonitorNginxTask)))
	mux.Handle("POST /monitor/websites/nginx/scan", a.requireSession(http.HandlerFunc(a.scanWebsiteMonitorNginx)))
	mux.Handle("POST /monitor/websites/nginx/import", a.requireSession(http.HandlerFunc(a.importWebsiteMonitorNginx)))
	mux.Handle("GET /monitor/websites/{id}/edit", a.requireSession(http.HandlerFunc(a.websiteMonitorEditTask)))
	mux.Handle("POST /monitor/websites/{id}", a.requireSession(http.HandlerFunc(a.updateWebsiteMonitor)))
	mux.Handle("GET /monitor/websites/{id}", a.requireSession(http.HandlerFunc(a.websiteMonitorDetail)))
	mux.Handle("GET /monitor/websites/{id}/data", a.requireSession(http.HandlerFunc(a.websiteMonitorDetailData)))
	mux.Handle("POST /monitor/websites/{id}/check", a.requireSession(http.HandlerFunc(a.checkWebsiteMonitorNow)))
	mux.Handle("POST /monitor/websites/{id}/pause", a.requireSession(http.HandlerFunc(a.pauseWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/resume", a.requireSession(http.HandlerFunc(a.resumeWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/move", a.requireSession(http.HandlerFunc(a.moveWebsiteMonitor)))
	mux.Handle("POST /monitor/websites/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteWebsiteMonitor)))
	mux.Handle("GET /settings/account", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
			Locale              webLocale
		}{Username: username, CSRFToken: current.csrfToken, CredentialOverride: a.credentialOverride, Locale: resolveWebLocale(request)})
	})))
	mux.Handle("POST /settings/account", a.requireSession(http.HandlerFunc(a.changePassword)))
	mux.Handle("GET /settings/display", a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = displaySettingsTemplate.Execute(response, struct {
			Locale webLocale
		}{Locale: resolveWebLocale(request)})
	})))
	mux.Handle("GET /settings/updates", a.requireSession(http.HandlerFunc(a.updatesPage)))
	mux.Handle("GET /settings/updates/status", a.requireSession(http.HandlerFunc(a.updateStatus)))
	mux.Handle("POST /settings/updates/check", a.requireSession(http.HandlerFunc(a.checkUpdate)))
	mux.Handle("POST /settings/updates/prepare", a.requireSession(http.HandlerFunc(a.prepareUpdate)))
	mux.Handle("POST /settings/updates/apply", a.requireSession(http.HandlerFunc(a.applyUpdate)))
	mux.Handle("GET /resources/files/new-directory", a.requireSession(http.HandlerFunc(a.newDirectoryTask)))
	mux.Handle("GET /resources/files/upload", a.requireSession(http.HandlerFunc(a.uploadTask)))
	mux.Handle("GET /resources/files/log", a.requireSession(http.HandlerFunc(a.fileLogPage)))
	mux.Handle("GET /resources/files/log/history", a.requireSession(http.HandlerFunc(a.fileLogHistory)))
	mux.Handle("GET /resources/files/log/events", a.requireSession(http.HandlerFunc(a.fileLogEvents)))
	mux.Handle("GET /resources/files/run/{path...}", a.requireSession(http.HandlerFunc(a.runFileTask)))
	mux.Handle("GET /resources/files/quick-run/{path...}", a.requireSession(http.HandlerFunc(a.quickRunFromFileTask)))
	mux.Handle("GET /resources/files/{path...}", a.requireSession(http.HandlerFunc(a.filesPage)))
	mux.Handle("POST /resources/files/mkdir", a.requireSession(http.HandlerFunc(a.createDirectory)))
	mux.Handle("POST /resources/files/conflicts", a.requireSession(http.HandlerFunc(a.uploadConflicts)))
	mux.Handle("POST /resources/files/upload", a.requireSession(http.HandlerFunc(a.uploadFiles)))
	mux.Handle("GET /resources/files/download/{path...}", a.requireSession(http.HandlerFunc(a.downloadFile)))
	mux.Handle("GET /resources/files/preview/{path...}", a.requireSession(http.HandlerFunc(a.previewImage)))
	mux.Handle("GET /resources/files/view/{path...}", a.requireSession(http.HandlerFunc(a.previewTextPage)))
	mux.Handle("POST /resources/files/delete", a.requireSession(http.HandlerFunc(a.deleteFile)))
	mux.Handle("POST /resources/files/move", a.requireSession(http.HandlerFunc(a.moveFile)))
	mux.Handle("POST /resources/files/toggle-executable", a.requireSession(http.HandlerFunc(a.toggleExecutable)))
	mux.Handle("GET /resources/trash", a.requireSession(http.HandlerFunc(a.trashPage)))
	mux.Handle("POST /resources/trash/restore", a.requireSession(http.HandlerFunc(a.restoreTrash)))
	mux.Handle("POST /resources/trash/purge", a.requireSession(http.HandlerFunc(a.purgeTrash)))
	mux.Handle("GET /resources/files/edit/{path...}", a.requireSession(http.HandlerFunc(a.editTextPage)))
	mux.Handle("POST /resources/files/edit/{path...}", a.requireSession(http.HandlerFunc(a.saveText)))
	mux.Handle("POST /history/runs/start", a.requireSession(http.HandlerFunc(a.startRun)))
	mux.Handle("GET /history/runs", a.requireSession(http.HandlerFunc(a.runsPage)))
	mux.Handle("GET /history/runs/{id}/save-quick-run", a.requireSession(http.HandlerFunc(a.saveQuickRunTask)))
	mux.Handle("GET /history/runs/{id}", a.requireSession(http.HandlerFunc(a.runDetails)))
	mux.Handle("POST /history/runs/{id}/stop", a.requireSession(http.HandlerFunc(a.stopRun)))
	mux.Handle("GET /history/runs/{id}/events", a.requireSession(http.HandlerFunc(a.runEvents)))
	mux.Handle("GET /resources/variables", a.requireSession(http.HandlerFunc(a.variablesPage)))
	mux.Handle("GET /resources/variables/new", a.requireSession(http.HandlerFunc(a.newVariableTask)))
	mux.Handle("GET /resources/variables/{name}/edit", a.requireSession(http.HandlerFunc(a.editVariableTask)))
	mux.Handle("POST /resources/variables", a.requireSession(http.HandlerFunc(a.createVariable)))
	mux.Handle("POST /resources/variables/{name}/update", a.requireSession(http.HandlerFunc(a.updateVariable)))
	mux.Handle("POST /resources/variables/{name}/delete", a.requireSession(http.HandlerFunc(a.deleteVariable)))
	mux.Handle("POST /history/runs/{id}/quick-run", a.requireSession(http.HandlerFunc(a.saveQuickRun)))
	mux.Handle("GET /config/quick-runs", a.requireSession(http.HandlerFunc(a.quickRunsPage)))
	mux.Handle("POST /config/quick-runs", a.requireSession(http.HandlerFunc(a.createQuickRunFromFile)))
	mux.Handle("GET /config/quick-runs/groups/new", a.requireSession(http.HandlerFunc(a.newQuickRunGroupTask)))
	mux.Handle("POST /config/quick-runs/groups", a.requireSession(http.HandlerFunc(a.createQuickRunGroup)))
	mux.Handle("GET /config/quick-runs/groups/{id}/edit", a.requireSession(http.HandlerFunc(a.editQuickRunGroupTask)))
	mux.Handle("POST /config/quick-runs/groups/{id}/update", a.requireSession(http.HandlerFunc(a.updateQuickRunGroup)))
	mux.Handle("POST /config/quick-runs/groups/{id}/move", a.requireSession(http.HandlerFunc(a.moveQuickRunGroup)))
	mux.Handle("POST /config/quick-runs/groups/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteQuickRunGroup)))
	mux.Handle("GET /config/quick-runs/{id}/move-group", a.requireSession(http.HandlerFunc(a.moveQuickRunToGroupTask)))
	mux.Handle("POST /config/quick-runs/{id}/move-group", a.requireSession(http.HandlerFunc(a.moveQuickRunToGroup)))
	mux.Handle("GET /config/quick-runs/{id}/edit", a.requireSession(http.HandlerFunc(a.editQuickRunTask)))
	mux.Handle("POST /config/quick-runs/{id}/update", a.requireSession(http.HandlerFunc(a.updateQuickRun)))
	mux.Handle("GET /config/quick-runs/{id}/copy", a.requireSession(http.HandlerFunc(a.copyQuickRunTask)))
	mux.Handle("POST /config/quick-runs/{id}/copy", a.requireSession(http.HandlerFunc(a.copyQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/lock", a.requireSession(http.HandlerFunc(a.setQuickRunLocked)))
	mux.Handle("POST /config/quick-runs/{id}/start", a.requireSession(http.HandlerFunc(a.startQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/move", a.requireSession(http.HandlerFunc(a.moveQuickRun)))
	mux.Handle("POST /config/quick-runs/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteQuickRun)))
	mux.Handle("GET /config/schedules", a.requireSession(http.HandlerFunc(a.schedulesPage)))
	mux.Handle("GET /config/schedules/groups/new", a.requireSession(http.HandlerFunc(a.newScheduleGroupTask)))
	mux.Handle("POST /config/schedules/groups", a.requireSession(http.HandlerFunc(a.createScheduleGroup)))
	mux.Handle("GET /config/schedules/groups/{id}/edit", a.requireSession(http.HandlerFunc(a.editScheduleGroupTask)))
	mux.Handle("POST /config/schedules/groups/{id}/update", a.requireSession(http.HandlerFunc(a.updateScheduleGroup)))
	mux.Handle("POST /config/schedules/groups/{id}/move", a.requireSession(http.HandlerFunc(a.moveScheduleGroup)))
	mux.Handle("POST /config/schedules/groups/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteScheduleGroup)))
	mux.Handle("GET /config/schedules/new", a.requireSession(http.HandlerFunc(a.newScheduleTask)))
	mux.Handle("GET /config/schedules/{id}/edit", a.requireSession(http.HandlerFunc(a.editScheduleTask)))
	mux.Handle("POST /config/schedules/preview", a.requireSession(http.HandlerFunc(a.previewScheduleCron)))
	mux.Handle("POST /config/schedules/{id}/preview", a.requireSession(http.HandlerFunc(a.previewScheduleCron)))
	mux.Handle("POST /config/schedules", a.requireSession(http.HandlerFunc(a.createSchedule)))
	mux.Handle("POST /config/schedules/{id}/update", a.requireSession(http.HandlerFunc(a.updateSchedule)))
	mux.Handle("POST /config/schedules/{id}/toggle", a.requireSession(http.HandlerFunc(a.toggleSchedule)))
	mux.Handle("POST /config/schedules/{id}/run", a.requireSession(http.HandlerFunc(a.runScheduleNow)))
	mux.Handle("POST /config/schedules/{id}/delete", a.requireSession(http.HandlerFunc(a.deleteSchedule)))
	mux.Handle("GET /history/audit", a.requireSession(http.HandlerFunc(a.auditPage)))
	mux.Handle("GET /history/audit.csv", a.requireSession(http.HandlerFunc(a.auditDownload)))
	mux.Handle("GET /settings/version-protection", a.requireSession(http.HandlerFunc(a.versionProtectionPage)))
	mux.Handle("POST /settings/version-protection/enable", a.requireSession(http.HandlerFunc(a.enableVersionProtection)))
	mux.Handle("POST /settings/version-protection/adopt", a.requireSession(http.HandlerFunc(a.adoptVersionProtection)))
	mux.Handle("POST /settings/version-protection/disable", a.requireSession(http.HandlerFunc(a.disableVersionProtection)))
	mux.Handle("POST /settings/version-protection/checkpoint", a.requireSession(http.HandlerFunc(a.checkpointVersionProtection)))
	mux.Handle("POST /settings/version-protection/restore", a.requireSession(http.HandlerFunc(a.restoreVersionedFile)))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request = a.applyTrustedProxy(request)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if isSecureRequest(request) {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if a.validation.Load() && request.Method != http.MethodGet {
			response.Header().Set("Retry-After", "2")
			http.Error(response, webText(resolveWebLocale(request), "updates.validation_write_blocked"), http.StatusServiceUnavailable)
			return
		}
		pageResponse := &pageResponseWriter{ResponseWriter: response}
		mux.ServeHTTP(pageResponse, request)
		pageResponse.finish(a, request)
	})
}

func serveWebAsset(response http.ResponseWriter, request *http.Request, contentType, body string) {
	response.Header().Set("Content-Type", contentType)
	if request.URL.Query().Has("v") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		response.Header().Set("Cache-Control", "no-cache, must-revalidate")
	}
	_, _ = io.WriteString(response, body)
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
	locale := resolveWebLocale(request)
	if request.URL.Path != "/login" {
		if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
			body = []byte(prepareApplicationDocument(body, locale))
		} else {
			body = a.addApplicationShell(request, body)
		}
	}
	w.Header().Set("Content-Language", string(locale))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

const listPageSize = 20

func isDeferredDataShell(request *http.Request) bool {
	return request.Header.Get("X-ScriptBoard-Navigation") == "pjax" &&
		request.Header.Get("X-ScriptBoard-Data") == "shell"
}

type paginationView struct {
	Page, PageCount, Total, Start, End int
	PreviousURL, NextURL               string
	HasPrevious, HasNext               bool
}

type overviewRunView struct {
	ID         string    `json:"id"`
	ScriptPath string    `json:"scriptPath"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
}

type overviewResponse struct {
	hoststatus.Overview
	ActiveRuns    []overviewRunView `json:"activeRuns"`
	HostUptime    time.Duration     `json:"hostUptime"`
	ServiceUptime time.Duration     `json:"serviceUptime"`
}

func validOverviewRange(value string) bool {
	switch value {
	case "", hoststatus.Range15Minutes, hoststatus.Range1Hour, hoststatus.Range6Hours, hoststatus.Range24Hours:
		return true
	default:
		return false
	}
}

func (a *App) loadOverview(request *http.Request, selectedRange string) (overviewResponse, error) {
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	overview, err := a.hostStatus.Overview(request.Context(), selectedRange)
	if err != nil {
		return overviewResponse{}, err
	}
	runs, err := a.activeOverviewRuns()
	if err != nil {
		return overviewResponse{}, err
	}
	now := time.Now().UTC()
	response := overviewResponse{Overview: overview, ActiveRuns: runs}
	if !overview.Facts.BootedAt.IsZero() {
		response.HostUptime = now.Sub(overview.Facts.BootedAt)
	}
	if !overview.Facts.ServiceStartedAt.IsZero() {
		response.ServiceUptime = now.Sub(overview.Facts.ServiceStartedAt)
	}
	return response, nil
}

func (a *App) overviewPage(response http.ResponseWriter, request *http.Request) {
	selectedRange := request.URL.Query().Get("range")
	if !validOverviewRange(selectedRange) {
		selectedRange = hoststatus.Range1Hour
	}
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	view, err := a.loadOverview(request, selectedRange)
	if err != nil {
		http.Error(response, "无法读取宿主状态："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = overviewTemplate.Execute(response, struct {
		overviewResponse
		Range  string
		Locale webLocale
	}{overviewResponse: view, Range: selectedRange, Locale: resolveWebLocale(request)})
}

func (a *App) overviewData(response http.ResponseWriter, request *http.Request) {
	selectedRange := request.URL.Query().Get("range")
	if !validOverviewRange(selectedRange) {
		http.Error(response, "无效的概览时间范围", http.StatusBadRequest)
		return
	}
	if selectedRange == "" {
		selectedRange = hoststatus.Range1Hour
	}
	view, err := a.loadOverview(request, selectedRange)
	if err != nil {
		http.Error(response, "无法读取宿主状态："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func (a *App) activeOverviewRuns() ([]overviewRunView, error) {
	rows, err := a.db.Query(`SELECT id, script_path, status, started_at, created_at FROM runs
		WHERE status IN ('starting', 'running', 'stopping', 'timing_out') ORDER BY created_at DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []overviewRunView
	for rows.Next() {
		var value overviewRunView
		var started sql.NullInt64
		var created int64
		if err := rows.Scan(&value.ID, &value.ScriptPath, &value.Status, &started, &created); err != nil {
			return nil, err
		}
		stamp := created
		if started.Valid {
			stamp = started.Int64
		}
		value.StartedAt = time.Unix(0, stamp).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}

func newPagination(request *http.Request, total int) paginationView {
	pageCount := max(1, (total+listPageSize-1)/listPageSize)
	page := 1
	if parsed, err := strconv.Atoi(request.URL.Query().Get("page")); err == nil && parsed > 0 {
		page = min(parsed, pageCount)
	}
	start := min((page-1)*listPageSize, total)
	end := min(start+listPageSize, total)
	view := paginationView{
		Page: page, PageCount: pageCount, Total: total, Start: start, End: end,
		HasPrevious: page > 1, HasNext: page < pageCount,
	}
	pageURL := func(target int) string {
		query := request.URL.Query()
		query.Set("page", strconv.Itoa(target))
		return "?" + query.Encode()
	}
	if view.HasPrevious {
		view.PreviousURL = pageURL(page - 1)
	}
	if view.HasNext {
		view.NextURL = pageURL(page + 1)
	}
	return view
}

func renderApplicationError(request *http.Request, status int, message string) []byte {
	destination, label := "/resources/files/", "返回文件"
	switch {
	case strings.HasPrefix(request.URL.Path, "/monitor/websites"):
		destination, label = "/monitor/websites", "返回网站监控"
	case strings.HasPrefix(request.URL.Path, "/monitor"):
		destination, label = "/monitor", "返回概览"
	case strings.HasPrefix(request.URL.Path, "/settings/account"):
		destination, label = "/settings/account", "返回账户设置"
	case strings.HasPrefix(request.URL.Path, "/settings/display"):
		destination, label = "/settings/display", "返回状态显示设置"
	case strings.HasPrefix(request.URL.Path, "/settings/version-protection"):
		destination, label = "/settings/version-protection", "返回版本保护"
	case strings.HasPrefix(request.URL.Path, "/config/quick-runs"):
		destination, label = "/config/quick-runs", "返回快捷执行"
	case strings.HasPrefix(request.URL.Path, "/config/schedules"):
		destination, label = "/config/schedules", "返回计划"
	case strings.HasPrefix(request.URL.Path, "/resources/variables"):
		destination, label = "/resources/variables", "返回变量"
	case strings.HasPrefix(request.URL.Path, "/history/audit"):
		destination, label = "/history/audit", "返回审计"
	}
	var page bytes.Buffer
	summaryKey := "error.internal"
	switch status {
	case http.StatusBadRequest:
		summaryKey = "error.bad_request"
	case http.StatusUnauthorized:
		summaryKey = "error.unauthorized"
	case http.StatusForbidden:
		summaryKey = "error.forbidden"
	case http.StatusNotFound:
		summaryKey = "error.not_found"
	case http.StatusConflict:
		summaryKey = "error.conflict"
	}
	locale := resolveWebLocale(request)
	_ = applicationErrorTemplate.Execute(&page, struct {
		Status             int
		Message, Summary   string
		Destination, Label string
		Locale             webLocale
	}{Status: status, Message: message, Summary: webText(locale, summaryKey), Destination: destination, Label: label, Locale: locale})
	return page.Bytes()
}

var appCSS = mustWebAsset("web/assets/app.css")

var appJS = mustWebAsset("web/assets/app.js")

var markdownItJS = mustWebAsset("web/assets/markdown-it.min.js")

var domPurifyJS = mustWebAsset("web/assets/purify.min.js")

var highlightJS = mustWebAsset("web/assets/highlight.min.js")

var highlightPowerShellJS = mustWebAsset("web/assets/highlight-powershell.min.js")

var highlightDOSJS = mustWebAsset("web/assets/highlight-dos.min.js")

var webAssetVersion = func() string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		appCSS,
		appJS,
		markdownItJS,
		domPurifyJS,
		highlightJS,
		highlightPowerShellJS,
		highlightDOSJS,
	}, "\x00")))
	return hex.EncodeToString(digest[:6])
}()

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
	pagination := newPagination(request, len(history))
	if len(history) > 0 {
		history = history[pagination.Start:pagination.End]
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = versionProtectionTemplate.Execute(response, struct {
		State       gitprotect.State
		CSRFToken   string
		HistoryPath string
		History     []gitprotect.Commit
		Pagination  paginationView
		Locale      webLocale
	}{State: state, CSRFToken: current.csrfToken, HistoryPath: historyPath, History: history, Pagination: pagination, Locale: resolveWebLocale(request)})
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

var (
	errInvalidDateRange = errors.New("invalid date range")
	errDateRangeOrder   = errors.New("start date is after end date")
)

type localDateRange struct {
	FromDate    string
	ToDate      string
	From        time.Time
	ToExclusive time.Time
	HasFromDate bool
	HasToDate   bool
}

func parseLocalDateRange(values url.Values) (localDateRange, error) {
	dateRange := localDateRange{
		FromDate: strings.TrimSpace(values.Get("from")),
		ToDate:   strings.TrimSpace(values.Get("to")),
	}
	var err error
	if dateRange.FromDate != "" {
		dateRange.From, err = time.ParseInLocation(time.DateOnly, dateRange.FromDate, time.Local)
		if err != nil {
			return localDateRange{}, errInvalidDateRange
		}
		dateRange.HasFromDate = true
	}
	if dateRange.ToDate != "" {
		to, parseErr := time.ParseInLocation(time.DateOnly, dateRange.ToDate, time.Local)
		err = parseErr
		if err != nil {
			return localDateRange{}, errInvalidDateRange
		}
		dateRange.ToExclusive = to.AddDate(0, 0, 1)
		dateRange.HasToDate = true
	}
	if dateRange.HasFromDate && dateRange.HasToDate && dateRange.From.After(dateRange.ToExclusive.AddDate(0, 0, -1)) {
		return localDateRange{}, errDateRangeOrder
	}
	return dateRange, nil
}

type auditFilters struct {
	Query              string
	FromDate           string
	ToDate             string
	FromUnix           int64
	ToExclusiveUnix    int64
	HasFromDate        bool
	HasToDate          bool
	HasActiveSelection bool
}

func parseAuditFilters(values url.Values) (auditFilters, error) {
	dateRange, err := parseLocalDateRange(values)
	if err != nil {
		return auditFilters{}, err
	}
	filters := auditFilters{
		Query:       strings.TrimSpace(values.Get("q")),
		FromDate:    dateRange.FromDate,
		ToDate:      dateRange.ToDate,
		FromUnix:    dateRange.From.Unix(),
		HasFromDate: dateRange.HasFromDate,
		HasToDate:   dateRange.HasToDate,
	}
	if dateRange.HasToDate {
		filters.ToExclusiveUnix = dateRange.ToExclusive.Unix()
	}
	filters.HasActiveSelection = filters.Query != "" || filters.HasFromDate || filters.HasToDate
	return filters, nil
}

func (a *App) auditPage(response http.ResponseWriter, request *http.Request) {
	filters, err := parseAuditFilters(request.URL.Query())
	if err != nil {
		key := "common.invalid_date_range"
		if errors.Is(err, errDateRangeOrder) {
			key = "common.invalid_date_order"
		}
		http.Error(response, webText(resolveWebLocale(request), key), http.StatusBadRequest)
		return
	}
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = auditTemplate.Execute(response, struct {
			Events       []auditView
			Pagination   paginationView
			Filters      auditFilters
			Locale       webLocale
			DeferredData bool
		}{Filters: filters, Locale: locale, DeferredData: true})
		return
	}
	like := "%" + filters.Query + "%"
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)`,
		filters.Query, like, like, like, like,
		filters.HasFromDate, filters.FromUnix,
		filters.HasToDate, filters.ToExclusiveUnix).Scan(&total); err != nil {
		http.Error(response, "无法读取审计事件", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query(`SELECT occurred_at, action, target, result, source_address FROM audit_events
		WHERE (? = '' OR action LIKE ? OR target LIKE ? OR result LIKE ? OR source_address LIKE ?)
		AND (? = 0 OR occurred_at >= ?)
		AND (? = 0 OR occurred_at < ?)
		ORDER BY occurred_at DESC LIMIT ? OFFSET ?`,
		filters.Query, like, like, like, like,
		filters.HasFromDate, filters.FromUnix,
		filters.HasToDate, filters.ToExclusiveUnix,
		listPageSize, pagination.Start)
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
	_ = auditTemplate.Execute(response, struct {
		Events       []auditView
		Pagination   paginationView
		Filters      auditFilters
		Locale       webLocale
		DeferredData bool
	}{Events: events, Pagination: pagination, Filters: filters, Locale: locale})
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
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = schedulesTemplate.Execute(response, struct {
			Schedules    []scheduler.Schedule
			Groups       []scheduleGroup
			CSRFToken    string
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	groups, err := a.loadScheduleGroups()
	if err != nil {
		http.Error(response, "无法读取计划分组", http.StatusInternalServerError)
		return
	}
	schedules, err := a.scheduler.List()
	if err != nil {
		http.Error(response, "无法读取计划", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = schedulesTemplate.Execute(response, struct {
		Schedules    []scheduler.Schedule
		Groups       []scheduleGroup
		CSRFToken    string
		Locale       webLocale
		DeferredData bool
	}{
		Schedules: schedules, Groups: organizeScheduleGroups(groups, schedules, locale),
		CSRFToken: current.csrfToken, Locale: locale,
	})
}

func (a *App) createSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := a.scheduleRequest(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := a.scheduler.Create(values)
	if err != nil {
		if isScheduleCronError(err) {
			a.renderScheduleCronSubmissionError(response, request, err)
			return
		}
		http.Error(response, "无法创建计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("create_schedule", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) scheduleRequest(request *http.Request) (scheduler.CreateRequest, error) {
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		return scheduler.CreateRequest{}, errors.New("计划名称无效")
	}
	groupID, groupName, err := a.resolveScheduleGroup(request.FormValue("group_id"))
	if err != nil {
		return scheduler.CreateRequest{}, errors.New("计划分组不存在")
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 0 || parsed > 24*60*60 {
			return scheduler.CreateRequest{}, errors.New("超时必须是 0 到 86400 秒")
		}
		timeoutSeconds = parsed
	}
	return scheduler.CreateRequest{
		Name: name, GroupID: groupID, GroupName: groupName,
		ScriptPath: request.FormValue("script"), ArgumentsTemplate: request.FormValue("arguments"),
		Expression: request.FormValue("expression"), TimeoutSeconds: timeoutSeconds,
		AllowOverlap: request.FormValue("disallow_overlap") == "",
	}, nil
}

func (a *App) updateSchedule(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	values, err := a.scheduleRequest(request)
	if err == nil {
		err = a.scheduler.Update(request.PathValue("id"), values)
	}
	if err != nil {
		if isScheduleCronError(err) {
			a.renderScheduleCronSubmissionError(response, request, err)
			return
		}
		http.Error(response, "无法更新计划："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("update_schedule", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
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
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) runScheduleNow(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id, err := a.scheduler.RunNow(request.PathValue("id"))
	if err != nil {
		http.Error(response, "无法立即执行计划："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("run_schedule_now", request.PathValue("id"), "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
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
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

type quickRunView struct {
	ID                string
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	TimeoutSeconds    int
	GroupID           string
	Valid             bool
	Locked            bool
}

type overlapView struct {
	Action, Script, Arguments, Timeout, CSRFToken string
	Locale                                        webLocale
}

type quickRunCreateRequest struct {
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	TimeoutSeconds    int
	SourceRunID       *string
	GroupID           *string
}

func (a *App) createQuickRun(values quickRunCreateRequest) (string, error) {
	id, err := randomToken(18)
	if err != nil {
		return "", err
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	var sortOrder int
	if err := transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs WHERE group_id IS ?", values.GroupID).Scan(&sortOrder); err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	if _, err := transaction.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at, group_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, values.Name, values.ScriptPath, values.ArgumentsTemplate, values.TimeoutSeconds,
		values.SourceRunID, sortOrder, now, values.GroupID, now,
	); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return id, nil
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
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRun(quickRunCreateRequest{
		Name: name, ScriptPath: source.ScriptPath, ArgumentsTemplate: source.ArgumentsTemplate,
		TimeoutSeconds: source.TimeoutSeconds, SourceRunID: &source.ID, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAudit("create_quick_run", id, "succeeded", request.RemoteAddr)
	destination := "/config/quick-runs"
	if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
		destination = "/history/runs/" + url.PathEscape(source.ID)
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (a *App) createQuickRunFromFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行名称无效", http.StatusBadRequest)
		return
	}
	scriptPath := filepath.ToSlash(strings.Trim(request.FormValue("script"), "/"))
	info, err := a.managed.Info(scriptPath)
	if err != nil || !info.Mode().IsRegular() || !isScriptExtension(scriptPath) {
		http.Error(response, "脚本不存在或不可运行", http.StatusBadRequest)
		return
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 0 || parsed > 24*60*60 {
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
	argumentsTemplate := request.FormValue("arguments")
	if err := runmanager.ValidateArgumentsTemplate(argumentsTemplate, variables); err != nil {
		http.Error(response, "参数无效："+err.Error(), http.StatusBadRequest)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRun(quickRunCreateRequest{
		Name: name, ScriptPath: scriptPath, ArgumentsTemplate: argumentsTemplate,
		TimeoutSeconds: timeoutSeconds, SourceRunID: nil, GroupID: groupID,
	})
	if err != nil {
		http.Error(response, "无法保存快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAudit("create_quick_run", id, "succeeded", request.RemoteAddr)
	destination := "/config/quick-runs"
	if request.Header.Get("X-ScriptBoard-Navigation") == "pjax" {
		if returnTo := safeFilesReturnTo(request.FormValue("return_to")); returnTo != "" {
			destination = returnTo
		}
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (a *App) quickRunsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = quickRunsTemplate.Execute(response, struct {
			QuickRuns    []quickRunView
			Groups       []quickRunGroup
			CSRFToken    string
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "无法读取快捷执行分组", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.Query(`SELECT id, name, script_path, arguments_template, timeout_seconds, group_id, locked
		FROM quick_runs ORDER BY sort_order, created_at`)
	if err != nil {
		http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
		return
	}
	var quickRuns []quickRunView
	for rows.Next() {
		var quick quickRunView
		var groupID sql.NullString
		if err := rows.Scan(&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds, &groupID, &quick.Locked); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取快捷执行", http.StatusInternalServerError)
			return
		}
		if info, infoErr := a.managed.Info(quick.ScriptPath); infoErr == nil && info.Mode().IsRegular() {
			quick.Valid = true
		}
		if groupID.Valid {
			quick.GroupID = groupID.String
		}
		quickRuns = append(quickRuns, quick)
	}
	_ = rows.Close()
	groupIndexes := make(map[string]int, len(groups))
	for index := range groups {
		groupIndexes[groups[index].ID] = index
		groups[index].QuickRunCount = 0
	}
	var ungrouped []quickRunView
	for _, quick := range quickRuns {
		if index, ok := groupIndexes[quick.GroupID]; ok {
			groups[index].Items = append(groups[index].Items, quick)
			groups[index].QuickRunCount++
		} else {
			ungrouped = append(ungrouped, quick)
		}
	}
	if len(ungrouped) > 0 {
		groups = append(groups, quickRunGroup{
			ID: "ungrouped", Name: webText(locale, "quick_runs.ungrouped"),
			QuickRunCount: len(ungrouped), Items: ungrouped, Ungrouped: true,
		})
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := quickRunsTemplate.Execute(response, struct {
		QuickRuns    []quickRunView
		Groups       []quickRunGroup
		CSRFToken    string
		Locale       webLocale
		DeferredData bool
	}{QuickRuns: quickRuns, Groups: groups, CSRFToken: current.csrfToken, Locale: locale}); err != nil {
		http.Error(response, "Unable to render Quick Runs: "+err.Error(), http.StatusInternalServerError)
	}
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
		_ = overlapTemplate.Execute(response, overlapView{Action: "/config/quick-runs/" + url.PathEscape(quick.ID) + "/start", Script: quick.ScriptPath, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath: quick.ScriptPath, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds,
		SourceType: "admin/quick-run", SourceName: quick.Name, SourceID: quick.ID, Variables: variables,
	})
	if err != nil {
		http.Error(response, "无法启动快捷执行："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("start_quick_run", quick.ID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
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
	var groupID sql.NullString
	if err := transaction.QueryRow("SELECT sort_order, group_id FROM quick_runs WHERE id = ?", request.PathValue("id")).Scan(&currentOrder, &groupID); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	var groupValue any
	if groupID.Valid {
		groupValue = groupID.String
	}
	var neighborID string
	var neighborOrder int
	query := "SELECT id, sort_order FROM quick_runs WHERE group_id IS ? AND sort_order " + operator + " ? ORDER BY sort_order " + order + " LIMIT 1"
	if scanErr := transaction.QueryRow(query, groupValue, currentOrder).Scan(&neighborID, &neighborOrder); scanErr == nil {
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
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) deleteQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除快捷执行需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	result, err := a.db.Exec("DELETE FROM quick_runs WHERE id = ? AND locked = 0", id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil {
		http.Error(response, "无法删除快捷执行", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		if quick, loadErr := a.loadQuickRun(id); loadErr == nil && quick.Locked {
			http.Error(response, "快捷执行已锁定，请先解锁", http.StatusConflict)
			return
		}
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	a.recordAudit("delete_quick_run", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
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
	Name       string
	Value      string
	IsPassword bool
}

func (a *App) variablesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = variablesTemplate.Execute(response, struct {
			Variables    []variableView
			CSRFToken    string
			Pagination   paginationView
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&total); err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query("SELECT name, value, is_password FROM variables ORDER BY name LIMIT ? OFFSET ?", listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取变量", http.StatusInternalServerError)
		return
	}
	var variables []variableView
	for rows.Next() {
		var variable variableView
		if err := rows.Scan(&variable.Name, &variable.Value, &variable.IsPassword); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取变量", http.StatusInternalServerError)
			return
		}
		variables = append(variables, variable)
	}
	_ = rows.Close()
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = variablesTemplate.Execute(response, struct {
		Variables    []variableView
		CSRFToken    string
		Pagination   paginationView
		Locale       webLocale
		DeferredData bool
	}{Variables: variables, CSRFToken: current.csrfToken, Pagination: pagination, Locale: locale})
}

var variableNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (a *App) createVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := request.FormValue("name")
	value := request.FormValue("value")
	isPassword := request.FormValue("is_password") == "1"
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
	if _, err := a.db.Exec("INSERT INTO variables (name, value, is_password, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", name, value, isPassword, now, now); err != nil {
		http.Error(response, "变量已存在或无法保存", http.StatusConflict)
		return
	}
	a.recordAudit("create_variable", name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
}

func (a *App) updateVariable(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	original := request.PathValue("name")
	name, value := request.FormValue("name"), request.FormValue("value")
	isPassword := request.FormValue("is_password") == "1"
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
	result, err := transaction.Exec("UPDATE variables SET name = ?, value = ?, is_password = ?, updated_at = ? WHERE name = ?", name, value, isPassword, time.Now().UTC().Unix(), original)
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
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
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
	http.Redirect(response, request, "/resources/variables", http.StatusSeeOther)
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
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
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
			Action: "/history/runs/start", Script: request.FormValue("script"), Arguments: request.FormValue("arguments"), Timeout: request.FormValue("timeout_seconds"), CSRFToken: current.csrfToken, Locale: resolveWebLocale(request),
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
	http.Redirect(response, request, "/history/runs/"+url.PathEscape(id), http.StatusSeeOther)
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
		Locale    webLocale
	}{Run: run, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
}

type runFilters struct {
	Query               string
	FromDate            string
	ToDate              string
	FromUnixNano        int64
	ToExclusiveUnixNano int64
	HasFromDate         bool
	HasToDate           bool
	HasActiveSelection  bool
	FocusSearch         bool
	ScheduleID          string
}

func exactUnixNano(value time.Time) (int64, bool) {
	unixNano := value.UnixNano()
	return unixNano, time.Unix(0, unixNano).UTC().Equal(value.UTC())
}

func parseRunFilters(values url.Values) (runFilters, error) {
	dateRange, err := parseLocalDateRange(values)
	if err != nil {
		return runFilters{}, err
	}
	filters := runFilters{
		Query:       strings.TrimSpace(values.Get("q")),
		FromDate:    dateRange.FromDate,
		ToDate:      dateRange.ToDate,
		HasFromDate: dateRange.HasFromDate,
		HasToDate:   dateRange.HasToDate,
		FocusSearch: values.Get("focus") == "search",
		ScheduleID:  strings.TrimSpace(values.Get("schedule_id")),
	}
	if filters.HasFromDate {
		var ok bool
		filters.FromUnixNano, ok = exactUnixNano(dateRange.From)
		if !ok {
			return runFilters{}, errInvalidDateRange
		}
	}
	if filters.HasToDate {
		var ok bool
		filters.ToExclusiveUnixNano, ok = exactUnixNano(dateRange.ToExclusive)
		if !ok {
			return runFilters{}, errInvalidDateRange
		}
	}
	filters.HasActiveSelection = filters.Query != "" || filters.ScheduleID != "" || filters.HasFromDate || filters.HasToDate
	return filters, nil
}

func (a *App) runsPage(response http.ResponseWriter, request *http.Request) {
	filters, err := parseRunFilters(request.URL.Query())
	if err != nil {
		key := "common.invalid_date_range"
		if errors.Is(err, errDateRangeOrder) {
			key = "common.invalid_date_order"
		}
		http.Error(response, webText(resolveWebLocale(request), key), http.StatusBadRequest)
		return
	}
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = runsTemplate.Execute(response, struct {
			Runs         []runmanager.Run
			Pagination   paginationView
			Filters      runFilters
			Locale       webLocale
			DeferredData bool
		}{Filters: filters, Locale: locale, DeferredData: true})
		return
	}
	managerQuery := filters.Query
	if filters.ScheduleID != "" {
		managerQuery = ""
	}
	managerFilters := runmanager.Filter{
		Query:                    managerQuery,
		ScheduleID:               filters.ScheduleID,
		CreatedFromUnixNano:      filters.FromUnixNano,
		CreatedBeforeUnixNano:    filters.ToExclusiveUnixNano,
		HasCreatedFromBoundary:   filters.HasFromDate,
		HasCreatedBeforeBoundary: filters.HasToDate,
	}
	total, err := a.runs.CountFiltered(managerFilters)
	if err != nil {
		http.Error(response, "无法读取运行记录："+err.Error(), http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	runs, err := a.runs.ListPageFiltered(managerFilters, listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "无法读取运行记录："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runsTemplate.Execute(response, struct {
		Runs         []runListItemView
		Pagination   paginationView
		Filters      runFilters
		Locale       webLocale
		DeferredData bool
	}{Runs: newRunListItemViews(runs), Pagination: pagination, Filters: filters, Locale: locale})
}

func (a *App) moveFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	source := request.FormValue("source")
	destination := request.FormValue("destination")
	sourceParent, _ := parentAndName(source)
	action := request.FormValue("conflict_action")
	if !validConflictAction(action) {
		http.Error(response, "同名文件处理方式无效", http.StatusBadRequest)
		return
	}
	if action == conflictActionSkip || pathpkg.Clean(source) == pathpkg.Clean(destination) {
		http.Redirect(response, request, filesURL(sourceParent), http.StatusSeeOther)
		return
	}
	if a.runs.ConflictsPath(source) {
		http.Error(response, "活动运行持有该脚本或其后代的运行租约", http.StatusConflict)
		return
	}
	destinationParent, destinationName := parentAndName(destination)
	if action == conflictActionRename {
		newName := strings.TrimSpace(request.FormValue("new_name"))
		if newName == "" {
			http.Error(response, "请输入新的文件名", http.StatusBadRequest)
			return
		}
		if err := managedfiles.ValidateName(newName); err != nil {
			http.Error(response, "新文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		destination = childPath(destinationParent, newName)
		destinationName = newName
	}
	_, targetErr := a.managed.Info(destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查移动目标："+targetErr.Error(), http.StatusBadRequest)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.managed.AvailableName(destinationParent, destinationName)
		if err != nil {
			http.Error(response, "无法生成可用名称："+err.Error(), http.StatusBadRequest)
			return
		}
		relatedPath := strings.HasPrefix(source+"/", destination+"/") || strings.HasPrefix(destination+"/", source+"/")
		a.renderFileConflict(response, request, fileConflictView{
			Action: "/resources/files/move", BackURL: filesURL(sourceParent),
			Source: source, Destination: destination, ItemPath: destination, SuggestedName: suggested,
			CanOverwrite: !relatedPath && !a.runs.ConflictsPath(destination),
		})
		return
	}
	if targetExists && action == conflictActionOverwrite && a.runs.ConflictsPath(destination) {
		http.Error(response, "活动运行正在使用同名目标，不能覆盖", http.StatusConflict)
		return
	}
	var displacedID string
	var displaced *managedfiles.Trashed
	if targetExists && action == conflictActionOverwrite {
		var err error
		displacedID, err = randomToken(18)
		if err != nil {
			http.Error(response, "无法创建覆盖事务", http.StatusInternalServerError)
			return
		}
		moved, err := a.managed.MoveToTrash(destination, displacedID)
		if err != nil {
			http.Error(response, "无法暂存同名目标："+err.Error(), http.StatusConflict)
			return
		}
		displaced = &moved
		if _, err := a.db.Exec(
			"INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, ?)",
			displacedID, moved.OriginalPath, moved.StoredName, time.Now().UTC().Unix(), moved.Size, moved.Directory,
		); err != nil {
			_ = a.managed.RestoreFromTrash(moved.StoredName, moved.OriginalPath)
			http.Error(response, "无法记录被覆盖的条目", http.StatusInternalServerError)
			return
		}
	}
	if err := a.managed.Move(source, destination); err != nil {
		if displaced != nil {
			_, _ = a.db.Exec("DELETE FROM trash_entries WHERE id = ?", displacedID)
			_ = a.managed.RestoreFromTrash(displaced.StoredName, displaced.OriginalPath)
		}
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
		if displaced != nil {
			_, _ = a.db.Exec("DELETE FROM trash_entries WHERE id = ?", displacedID)
			_ = a.managed.RestoreFromTrash(displaced.StoredName, displaced.OriginalPath)
		}
		http.Error(response, "无法同步更新引用："+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.checkpointWebMutation("move-entry", source+" -> "+destination); err != nil {
		http.Error(response, "条目已移动，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("move_entry", source+" -> "+destination, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, filesURL(destinationParent), http.StatusSeeOther)
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
	parent := pathpkg.Dir(relative)
	if parent == "." {
		parent = ""
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = textEditorTemplate.Execute(response, struct {
		Path, Content, Digest, CSRFToken, BackURL, ViewURL, DownloadURL, Action string
		Locale                                                                  webLocale
	}{
		Path: relative, Content: document.Content, Digest: document.Digest, CSRFToken: current.csrfToken,
		BackURL: filesURL(parent), ViewURL: routeFileURL("/resources/files/view/", relative), DownloadURL: routeFileURL("/resources/files/download/", relative), Action: routeFileURL("/resources/files/edit/", relative),
		Locale: resolveWebLocale(request),
	})
}

func (a *App) previewTextPage(response http.ResponseWriter, request *http.Request) {
	relative := request.PathValue("path")
	document, err := a.managed.ReadText(relative, 1<<20)
	if err != nil {
		http.Error(response, "无法预览文件："+err.Error(), http.StatusBadRequest)
		return
	}
	parent := pathpkg.Dir(relative)
	if parent == "." {
		parent = ""
	}
	markdown := strings.EqualFold(filepath.Ext(relative), ".md")
	highlightLanguage := highlightLanguageForPath(relative)
	title := webText(resolveWebLocale(request), "editor.preview_title")
	if markdown {
		title = webText(resolveWebLocale(request), "editor.markdown_preview_title")
	} else if highlightLanguage != "" {
		title = webText(resolveWebLocale(request), "editor.script_preview_title")
	}
	markdownBaseURL := "/resources/files/view/"
	if parent != "" {
		markdownBaseURL = routeFileURL("/resources/files/view/", parent) + "/"
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = textPreviewTemplate.Execute(response, struct {
		Path, Content, BackURL, EditURL, DownloadURL string
		LogURL                                       string
		Title, MarkdownBaseURL, HighlightLanguage    string
		Markdown                                     bool
		Locale                                       webLocale
	}{
		Path: relative, Content: document.Content, BackURL: filesURL(parent),
		EditURL: routeFileURL("/resources/files/edit/", relative), DownloadURL: routeFileURL("/resources/files/download/", relative),
		LogURL: "/resources/files/log?" + url.Values{"path": {relative}}.Encode(),
		Title:  title, Markdown: markdown, MarkdownBaseURL: markdownBaseURL, HighlightLanguage: highlightLanguage, Locale: resolveWebLocale(request),
	})
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
			Locale               webLocale
		}{Path: path, QuickRuns: quickCount, Schedules: scheduleCount, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
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
	http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
}

type trashView struct {
	ID           string
	OriginalPath string
	DeletedAt    time.Time
	Size         int64
	Directory    bool
}

func (a *App) trashPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = trashTemplate.Execute(response, struct {
			Entries      []trashView
			CSRFToken    string
			Pagination   paginationView
			Locale       webLocale
			DeferredData bool
		}{CSRFToken: current.csrfToken, Locale: locale, DeferredData: true})
		return
	}
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM trash_entries").Scan(&total); err != nil {
		http.Error(response, "无法读取回收站", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	rows, err := a.db.Query("SELECT id, original_path, deleted_at, size, is_directory FROM trash_entries ORDER BY deleted_at DESC LIMIT ? OFFSET ?", listPageSize, pagination.Start)
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
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = trashTemplate.Execute(response, struct {
		Entries      []trashView
		CSRFToken    string
		Pagination   paginationView
		Locale       webLocale
		DeferredData bool
	}{Entries: entries, CSRFToken: current.csrfToken, Pagination: pagination, Locale: locale})
}

func (a *App) restoreTrash(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	action := request.FormValue("conflict_action")
	if !validConflictAction(action) {
		http.Error(response, "同名文件处理方式无效", http.StatusBadRequest)
		return
	}
	if action == conflictActionSkip {
		http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
		return
	}
	id := request.FormValue("id")
	var original, stored string
	if err := a.db.QueryRow("SELECT original_path, stored_name FROM trash_entries WHERE id = ?", id).Scan(&original, &stored); err != nil {
		http.Error(response, "回收条目不存在", http.StatusNotFound)
		return
	}
	parent, originalName := parentAndName(original)
	destination := original
	if action == conflictActionRename {
		newName := strings.TrimSpace(request.FormValue("new_name"))
		if newName == "" {
			http.Error(response, "请输入新的文件名", http.StatusBadRequest)
			return
		}
		if err := managedfiles.ValidateName(newName); err != nil {
			http.Error(response, "新文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		destination = childPath(parent, newName)
	}
	_, targetErr := a.managed.Info(destination)
	targetExists := targetErr == nil
	if targetErr != nil && !os.IsNotExist(targetErr) {
		http.Error(response, "无法检查恢复目标："+targetErr.Error(), http.StatusConflict)
		return
	}
	if targetExists && action != conflictActionOverwrite {
		suggested, err := a.managed.AvailableName(parent, originalName)
		if action == conflictActionRename {
			_, requestedName := parentAndName(destination)
			suggested, err = a.managed.AvailableName(parent, requestedName)
		}
		if err != nil {
			http.Error(response, "无法生成可用名称："+err.Error(), http.StatusConflict)
			return
		}
		a.renderFileConflict(response, request, fileConflictView{
			Action: "/resources/trash/restore", BackURL: "/resources/trash", ID: id,
			ItemPath: destination, SuggestedName: suggested,
			CanOverwrite: !a.runs.ConflictsPath(destination),
		})
		return
	}
	if action == conflictActionOverwrite && targetExists && a.runs.ConflictsPath(destination) {
		http.Error(response, "活动运行正在使用同名目标，不能覆盖", http.StatusConflict)
		return
	}
	if err := a.commitTrashRestore(id, stored, destination, action == conflictActionOverwrite && targetExists); err != nil {
		http.Error(response, "无法恢复条目："+err.Error(), http.StatusConflict)
		return
	}
	if err := a.checkpointWebMutation("restore-trash", destination); err != nil {
		http.Error(response, "条目已恢复，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAudit("restore_trash", destination, "succeeded", request.RemoteAddr)
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
	http.Redirect(response, request, "/resources/trash", http.StatusSeeOther)
}

func (a *App) uploadFiles(response http.ResponseWriter, request *http.Request) {
	if err := diskspace.Require(a.managedRoot, diskspace.MinimumWritableBytes); err != nil {
		http.Error(response, err.Error(), http.StatusInsufficientStorage)
		return
	}
	locale := resolveWebLocale(request)
	request.Body = http.MaxBytesReader(response, request.Body, 2<<30)
	reader, err := request.MultipartReader()
	if err != nil {
		http.Error(response, "上传请求必须使用 multipart/form-data", http.StatusBadRequest)
		return
	}
	var csrfToken, relative string
	conflictAction := ""
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
				if string(value) == "yes" {
					conflictAction = conflictActionOverwrite
				}
			case "conflict_action":
				conflictAction = string(value)
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
		if !validConflictAction(conflictAction) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.invalid_conflict_action")})
			continue
		}
		targetInfo, targetErr := a.managed.Info(targetPath)
		targetExists := targetErr == nil
		if targetErr != nil && !os.IsNotExist(targetErr) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法检查同名文件：" + targetErr.Error()})
			continue
		}
		uploadName := filename
		replace := targetExists && conflictAction == conflictActionOverwrite
		if targetExists {
			switch conflictAction {
			case "", conflictActionSkip:
				_ = part.Close()
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.skipped"), Detail: webText(locale, "upload_results.kept_current")})
				a.recordAudit("upload_file", filename, "skipped", request.RemoteAddr)
				continue
			case conflictActionRename:
				uploadName, err = a.managed.AvailableName(relative, filename)
				if err != nil {
					_ = part.Close()
					results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法生成可用名称：" + err.Error()})
					continue
				}
				targetPath = pathpkg.Join(filepath.ToSlash(relative), uploadName)
			}
		}
		if replace && (!targetInfo.Mode().IsRegular() || a.runs.ConflictsPath(targetPath)) {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: webText(locale, "upload_results.cannot_overwrite")})
			a.recordAudit("upload_file", filename, "rejected", request.RemoteAddr)
			continue
		}
		storedID, idErr := randomToken(18)
		if idErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "无法创建上传事务"})
			continue
		}
		trashed, uploadErr := a.managed.Upload(relative, uploadName, part, 1<<30, replace, storedID)
		if uploadErr != nil {
			_ = part.Close()
			results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: uploadErr.Error()})
			a.recordAudit("upload_file", filename, "rejected", request.RemoteAddr)
			continue
		}
		_ = part.Close()
		if trashed != nil {
			_, err = a.db.Exec("INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, 0)", storedID, trashed.OriginalPath, trashed.StoredName, time.Now().UTC().Unix(), trashed.Size)
			if err != nil {
				_ = a.managed.RollbackTextSave(targetPath, storedID)
				results = append(results, uploadResult{Name: filename, Result: webText(locale, "upload_results.failed"), Detail: "替换已回滚：无法记录旧文件"})
				a.recordAudit("upload_file", filename, "failed", request.RemoteAddr)
				continue
			}
		}
		a.recordAudit("upload_file", uploadName, "succeeded", request.RemoteAddr)
		detail := webText(locale, "upload_results.saved")
		if uploadName != filename {
			detail = fmt.Sprintf(webText(locale, "upload_results.renamed"), uploadName)
		}
		results = append(results, uploadResult{Name: uploadName, Result: webText(locale, "upload_results.succeeded"), Detail: detail, Succeeded: true})
		succeeded++
	}
	if fileCount == 0 {
		http.Error(response, "未选择上传文件", http.StatusBadRequest)
		return
	}
	if succeeded > 0 {
		if err := a.checkpointWebMutation("upload", relative); err != nil {
			results = append(results, uploadResult{Name: "Version Protection", Result: webText(locale, "upload_results.failed"), Detail: "文件已上传，但 checkpoint 失败：" + err.Error()})
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if succeeded < fileCount || len(results) > fileCount {
		response.WriteHeader(http.StatusMultiStatus)
	}
	if err := uploadResultsTemplate.Execute(response, struct {
		Link    string
		Results []uploadResult
		Locale  webLocale
	}{Link: filesURL(relative), Results: results, Locale: locale}); err != nil {
		http.Error(response, "文件已上传，但版本保护检查点失败："+err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) filesPage(response http.ResponseWriter, request *http.Request) {
	relative := strings.Trim(request.PathValue("path"), "/")
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	sortField, direction := normalizeFileSort(request.URL.Query().Get("sort"), request.URL.Query().Get("direction"))
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if isDeferredDataShell(request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = filesTemplate.Execute(response, struct {
			CSRFToken, ManagedRoot, CurrentPath, Query, SortField, Direction string
			SortSummary, RootURL, SearchURL                                  string
			Locale                                                           webLocale
			DeferredData                                                     bool
		}{
			CSRFToken: current.csrfToken, ManagedRoot: a.managedRoot, CurrentPath: relative,
			Query: query, SortField: sortField, Direction: direction, SortSummary: fileSortSummary(locale, sortField, direction),
			RootURL: filesStateURL("", "", sortField, direction, 0), SearchURL: filesURL(relative),
			Locale: locale, DeferredData: true,
		})
		return
	}
	entries, err := a.managed.List(relative)
	if err != nil {
		http.Error(response, "无法读取受管根目录："+err.Error(), http.StatusInternalServerError)
		return
	}
	listing := prepareFileListing(entries, relative, query, sortField, direction)
	pagination := newPagination(request, len(listing))
	if pagination.HasPrevious {
		pagination.PreviousURL = filesStateURL(relative, query, sortField, direction, pagination.Page-1)
	}
	if pagination.HasNext {
		pagination.NextURL = filesStateURL(relative, query, sortField, direction, pagination.Page+1)
	}
	type fileView struct {
		managedfiles.Entry
		Path, BrowseURL, DownloadURL, EditURL, PreviewURL, ViewURL, QuickRunURL string
		LogURL                                                                  string
		Protection, IconClass                                                   string
		Runnable                                                                bool
		RecentArguments                                                         []string
		ArgumentListID                                                          string
		NameParts                                                               []fileNamePart
		CategoryLabel                                                           string
	}
	protectionState, _ := a.gitProtection.State()
	views := make([]fileView, 0, pagination.End-pagination.Start)
	for index, listed := range listing[pagination.Start:pagination.End] {
		entry, path := listed.Entry, listed.Path
		view := fileView{
			Entry: entry, Path: path, IconClass: fileCategoryIcon(listed.Category),
			NameParts: splitFileNameMatches(entry.Name, query), CategoryLabel: fileCategoryLabel(locale, listed.Category),
		}
		if entry.Kind == managedfiles.Directory {
			view.BrowseURL = filesStateURL(path, "", sortField, direction, 0)
		} else if entry.Kind == managedfiles.Regular {
			if protectionState.Enabled {
				view.Protection = a.gitProtection.ProtectionReason(path, entry.Size)
			}
			view.DownloadURL = routeFileURL("/resources/files/download/", path)
			switch listed.Category {
			case fileCategoryImage:
				view.PreviewURL = routeFileURL("/resources/files/preview/", path)
			case fileCategoryText, fileCategoryScript:
				view.ViewURL = routeFileURL("/resources/files/view/", path)
				view.EditURL = routeFileURL("/resources/files/edit/", path)
				view.LogURL = "/resources/files/log?" + url.Values{"path": {path}}.Encode()
				if listed.Category == fileCategoryScript {
					view.Runnable = true
					view.QuickRunURL = routeFileURL("/resources/files/quick-run/", path) + "?return_to=" + url.QueryEscape(request.URL.RequestURI())
					view.ArgumentListID = fmt.Sprintf("run-arguments-%d", index)
					rows, queryErr := a.db.Query(`SELECT arguments_template FROM runs WHERE script_path = ? AND TRIM(arguments_template) <> '' GROUP BY arguments_template ORDER BY MAX(created_at) DESC LIMIT 8`, path)
					if queryErr == nil {
						for rows.Next() {
							var arguments string
							if rows.Scan(&arguments) == nil {
								view.RecentArguments = append(view.RecentArguments, arguments)
							}
						}
						_ = rows.Close()
					}
				}
			}
		}
		views = append(views, view)
	}
	parentURL := ""
	if relative != "" {
		parent := pathpkg.Dir(relative)
		if parent == "." {
			parent = ""
		}
		parentURL = filesStateURL(parent, "", sortField, direction, 0)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = filesTemplate.Execute(response, struct {
		Entries             []fileView
		CSRFToken           string
		ManagedRoot         string
		CurrentPath         string
		Query               string
		SortField           string
		Direction           string
		SortSummary         string
		RootURL             string
		ClearURL            string
		SearchURL           string
		Pagination          paginationView
		CanToggleExecutable bool
		ParentURL           string
		VersionProtection   bool
		Locale              webLocale
		DeferredData        bool
	}{
		Entries: views, CSRFToken: current.csrfToken, ManagedRoot: a.managedRoot, CurrentPath: relative,
		Query: query, SortField: sortField, Direction: direction, SortSummary: fileSortSummary(locale, sortField, direction),
		RootURL: filesStateURL("", "", sortField, direction, 0), ClearURL: filesStateURL(relative, "", sortField, direction, 0),
		SearchURL:  filesURL(relative),
		Pagination: pagination, CanToggleExecutable: runtime.GOOS == "linux", ParentURL: parentURL,
		VersionProtection: protectionState.Enabled, Locale: locale,
	})
}

func isTextPreviewExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".json", ".yaml", ".yml", ".toml", ".ini", ".conf", ".cfg", ".log", ".csv", ".tsv", ".xml", ".html", ".css", ".js", ".ts", ".go", ".py", ".ps1", ".cmd", ".bat", ".sh", ".sql":
		return true
	default:
		return false
	}
}

func isScriptExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ps1", ".cmd", ".bat", ".sh", ".py":
		return true
	default:
		return false
	}
}

func highlightLanguageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ps1":
		return "powershell"
	case ".cmd", ".bat":
		return "dos"
	case ".sh":
		return "bash"
	case ".py":
		return "python"
	default:
		return ""
	}
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
		return "/resources/files/"
	}
	parts := strings.Split(pathpkg.Clean(filepath.ToSlash(relative)), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return "/resources/files/" + strings.Join(parts, "/") + "/"
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
		renderLoginFailure(response, request, http.StatusForbidden, request.FormValue("username"), "登录页面已过期，请重试")
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
		renderLoginFailure(response, request, http.StatusTooManyRequests, request.FormValue("username"), "登录尝试过于频繁，请稍后重试")
		return
	}

	var username, passwordHash string
	err = a.db.QueryRow("SELECT username, password_hash FROM admin WHERE id = 1").Scan(
		&username,
		&passwordHash,
	)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	if request.FormValue("username") != username || !verifyPassword(request.FormValue("password"), passwordHash) {
		a.recordLoginFailure(loginKeys...)
		a.recordAudit("login", "admin", "failed", request.RemoteAddr)
		renderLoginFailure(response, request, http.StatusUnauthorized, request.FormValue("username"), "用户名或密码错误")
		return
	}
	a.clearLoginFailures(loginKeys...)

	token, err := randomToken(32)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	sessionCSRF, err := randomToken(32)
	if err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
		return
	}
	now := time.Now().UTC()
	if _, err := a.db.Exec(
		"INSERT INTO sessions (token_hash, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		hashToken(token), sessionCSRF, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix(),
	); err != nil {
		renderLoginFailure(response, request, http.StatusInternalServerError, request.FormValue("username"), "暂时无法登录，请稍后重试")
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
	http.SetCookie(response, &http.Cookie{Name: loginCSRFCookieName, Path: "/", MaxAge: -1})
	a.recordAudit("login", "admin", "succeeded", request.RemoteAddr)
	completeLogin(response, request, "/monitor")
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
	csrfToken string
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
		SELECT sessions.csrf_token, sessions.last_seen_at, sessions.expires_at, admin.username
		FROM sessions CROSS JOIN admin
		WHERE sessions.token_hash = ? AND admin.id = 1`, hashToken(cookie.Value),
	).Scan(&current.csrfToken, &lastSeen, &expiresAt, &username)
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

func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, _, ok := a.loadSession(request)
		if !ok {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		cookie, _ := request.Cookie(sessionCookieName)
		now := time.Now().UTC()
		if !a.validation.Load() {
			_, _ = a.db.Exec("UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", now.Unix(), hashToken(cookie.Value))
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
	Locale    webLocale
}

func renderLoginPage(response http.ResponseWriter, request *http.Request, status int, username, errorMessage string) {
	token := ""
	if cookie, err := request.Cookie(loginCSRFCookieName); err == nil {
		token = cookie.Value
	}
	if token == "" {
		var err error
		token, err = randomToken(32)
		if err != nil {
			http.Error(response, "无法创建登录表单", http.StatusInternalServerError)
			return
		}
		http.SetCookie(response, &http.Cookie{
			Name:     loginCSRFCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecureRequest(request),
			SameSite: http.SameSiteStrictMode,
		})
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	locale := resolveWebLocale(request)
	response.Header().Set("Content-Language", string(locale))
	_ = loginTemplate.Execute(response, loginPageData{CSRFToken: token, Username: username, Error: errorMessage, Locale: locale})
}

func renderLoginFailure(response http.ResponseWriter, request *http.Request, status int, username, errorMessage string) {
	if !acceptsJSON(request) {
		renderLoginPage(response, request, status, username, errorMessage)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		http.Error(response, "无法创建登录表单", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(request),
		SameSite: http.SameSiteStrictMode,
	})
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": errorMessage, "csrf_token": token})
}

func completeLogin(response http.ResponseWriter, request *http.Request, destination string) {
	if !acceptsJSON(request) {
		http.Redirect(response, request, destination, http.StatusSeeOther)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(map[string]string{"redirect": destination})
}

func acceptsJSON(request *http.Request) bool {
	return strings.Contains(request.Header.Get("Accept"), "application/json")
}
