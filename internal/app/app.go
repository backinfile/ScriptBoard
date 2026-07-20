package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"

	"scriptboard/internal/managedfiles"
	"scriptboard/internal/runmanager"
)

const initialPasswordFilename = "initial-admin-password"

const (
	sessionCookieName   = "scriptboard_session"
	loginCSRFCookieName = "scriptboard_login_csrf"
)

type contextKey string

const sessionContextKey contextKey = "session"

type Config struct {
	ManagedRoot     string
	StateRoot       string
	RunTimeoutGrace time.Duration
}

type App struct {
	db        *sql.DB
	stateRoot string
	managed   *managedfiles.Store
	runs      *runmanager.Manager
	handler   http.Handler
}

func Open(config Config) (*App, error) {
	managedRoot, stateRoot, err := prepareRoots(config.ManagedRoot, config.StateRoot)
	if err != nil {
		return nil, err
	}

	db, err := openDatabase(filepath.Join(stateRoot, "app.db"))
	if err != nil {
		return nil, err
	}

	application := &App{db: db, stateRoot: stateRoot, managed: managedfiles.Open(managedRoot)}
	if err := application.initializeAdmin(stateRoot); err != nil {
		_ = db.Close()
		return nil, err
	}
	timeoutGrace := config.RunTimeoutGrace
	if timeoutGrace <= 0 {
		timeoutGrace = 30 * time.Second
	}
	application.runs = runmanager.New(db, application.managed, stateRoot, timeoutGrace)
	application.handler = application.routes(managedRoot)
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) Close() error {
	if a.runs != nil {
		a.runs.Close()
	}
	return a.db.Close()
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
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
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化 SQLite: %w", err)
		}
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
	mux.HandleFunc("GET /login", func(response http.ResponseWriter, request *http.Request) {
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
			Secure:   request.TLS != nil,
			SameSite: http.SameSiteStrictMode,
		})
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = loginTemplate.Execute(response, struct{ CSRFToken string }{CSRFToken: token})
	})
	mux.HandleFunc("POST /login", a.login)
	mux.Handle("GET /settings/account", a.requireSession(true, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = accountTemplate.Execute(response, struct{ CSRFToken string }{CSRFToken: current.csrfToken})
	})))
	mux.Handle("POST /settings/account", a.requireSession(true, http.HandlerFunc(a.changePassword)))
	mux.Handle("GET /files/{path...}", a.requireSession(false, http.HandlerFunc(a.filesPage)))
	mux.Handle("POST /files/mkdir", a.requireSession(false, http.HandlerFunc(a.createDirectory)))
	mux.Handle("POST /files/upload", a.requireSession(false, http.HandlerFunc(a.uploadFiles)))
	mux.Handle("GET /files/download/{path...}", a.requireSession(false, http.HandlerFunc(a.downloadFile)))
	mux.Handle("POST /files/delete", a.requireSession(false, http.HandlerFunc(a.deleteFile)))
	mux.Handle("POST /files/move", a.requireSession(false, http.HandlerFunc(a.moveFile)))
	mux.Handle("GET /trash", a.requireSession(false, http.HandlerFunc(a.trashPage)))
	mux.Handle("POST /trash/restore", a.requireSession(false, http.HandlerFunc(a.restoreTrash)))
	mux.Handle("POST /trash/purge", a.requireSession(false, http.HandlerFunc(a.purgeTrash)))
	mux.Handle("GET /files/edit/{path...}", a.requireSession(false, http.HandlerFunc(a.editTextPage)))
	mux.Handle("POST /files/edit/{path...}", a.requireSession(false, http.HandlerFunc(a.saveText)))
	mux.Handle("POST /runs/start", a.requireSession(false, http.HandlerFunc(a.startRun)))
	mux.Handle("GET /runs/{id}", a.requireSession(false, http.HandlerFunc(a.runDetails)))
	mux.Handle("POST /runs/{id}/stop", a.requireSession(false, http.HandlerFunc(a.stopRun)))
	mux.Handle("GET /runs/{id}/events", a.requireSession(false, http.HandlerFunc(a.runEvents)))
	mux.Handle("GET /variables", a.requireSession(false, http.HandlerFunc(a.variablesPage)))
	mux.Handle("POST /variables", a.requireSession(false, http.HandlerFunc(a.createVariable)))
	mux.Handle("POST /runs/{id}/quick-run", a.requireSession(false, http.HandlerFunc(a.saveQuickRun)))
	mux.Handle("GET /quick-runs", a.requireSession(false, http.HandlerFunc(a.quickRunsPage)))
	mux.Handle("POST /quick-runs/{id}/start", a.requireSession(false, http.HandlerFunc(a.startQuickRun)))
	return mux
}

type quickRunView struct {
	ID                string
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	TimeoutSeconds    int
}

func (a *App) saveQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "Quick Run 名称无效", http.StatusBadRequest)
		return
	}
	source, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "来源 Run 不存在", http.StatusNotFound)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建 Quick Run", http.StatusInternalServerError)
		return
	}
	var sortOrder int
	_ = a.db.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs").Scan(&sortOrder)
	if _, err := a.db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, arguments_template, timeout_seconds, source_run_id, sort_order, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, source.ScriptPath, source.ArgumentsTemplate, source.TimeoutSeconds, source.ID, sortOrder, time.Now().UTC().Unix(),
	); err != nil {
		http.Error(response, "无法保存 Quick Run", http.StatusInternalServerError)
		return
	}
	a.recordAudit("create_quick_run", id, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/quick-runs", http.StatusSeeOther)
}

func (a *App) quickRunsPage(response http.ResponseWriter, request *http.Request) {
	rows, err := a.db.Query("SELECT id, name, script_path, arguments_template, timeout_seconds FROM quick_runs ORDER BY sort_order, created_at")
	if err != nil {
		http.Error(response, "无法读取 Quick Run", http.StatusInternalServerError)
		return
	}
	var quickRuns []quickRunView
	for rows.Next() {
		var quick quickRunView
		if err := rows.Scan(&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取 Quick Run", http.StatusInternalServerError)
			return
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
		http.Error(response, "Quick Run 不存在", http.StatusNotFound)
		return
	}
	variables, err := a.loadVariables()
	if err != nil {
		http.Error(response, "无法读取 Variable", http.StatusInternalServerError)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath: quick.ScriptPath, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds,
		SourceType: "admin/quick-run", Variables: variables,
	})
	if err != nil {
		http.Error(response, "无法启动 Quick Run："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("start_quick_run", quick.ID, "accepted", request.RemoteAddr)
	http.Redirect(response, request, "/runs/"+url.PathEscape(id), http.StatusSeeOther)
}

func (a *App) runEvents(response http.ResponseWriter, request *http.Request) {
	lastSequence := int64(0)
	if value := request.Header.Get("Last-Event-ID"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			lastSequence = parsed
		}
	}
	run, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "Run 不存在", http.StatusNotFound)
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
			payload, _ := json.Marshal(map[string]any{"source": event.Source, "text": event.Data, "time": event.Time})
			_, _ = fmt.Fprintf(response, "id: %d\nevent: output\ndata: %s\n\n", event.Sequence, payload)
			lastSequence = event.Sequence
		}
		flusher.Flush()
		if run.Status != "starting" && run.Status != "running" && run.Status != "stopping" && run.Status != "timing_out" {
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
		http.Error(response, "无法读取 Variable", http.StatusInternalServerError)
		return
	}
	var variables []variableView
	for rows.Next() {
		var variable variableView
		if err := rows.Scan(&variable.Name, &variable.Value); err != nil {
			_ = rows.Close()
			http.Error(response, "无法读取 Variable", http.StatusInternalServerError)
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
		http.Error(response, "Variable 名称或值无效", http.StatusBadRequest)
		return
	}
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&count); err != nil || count >= 1000 {
		http.Error(response, "Variable 数量已达到上限", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Unix()
	if _, err := a.db.Exec("INSERT INTO variables (name, value, created_at, updated_at) VALUES (?, ?, ?, ?)", name, value, now, now); err != nil {
		http.Error(response, "Variable 已存在或无法保存", http.StatusConflict)
		return
	}
	a.recordAudit("create_variable", name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/variables", http.StatusSeeOther)
}

func (a *App) stopRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.runs.Stop(id); err != nil {
		http.Error(response, "无法停止 Run："+err.Error(), http.StatusConflict)
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
		http.Error(response, "无法读取 Variable", http.StatusInternalServerError)
		return
	}
	id, err := a.runs.Start(runmanager.StartRequest{
		ScriptPath:        request.FormValue("script"),
		ArgumentsTemplate: request.FormValue("arguments"),
		SourceType:        "admin/manual",
		TimeoutSeconds:    timeoutSeconds,
		Variables:         variables,
	})
	if err != nil {
		a.recordAudit("start_run", request.FormValue("script"), "rejected", request.RemoteAddr)
		http.Error(response, "无法启动 Script："+err.Error(), http.StatusBadRequest)
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
			http.Error(response, "Run 不存在", http.StatusNotFound)
			return
		}
		http.Error(response, "无法读取 Run："+err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runTemplate.Execute(response, struct {
		Run       runmanager.Run
		CSRFToken string
	}{Run: run, CSRFToken: current.csrfToken})
}

func (a *App) moveFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	source := request.FormValue("source")
	destination := request.FormValue("destination")
	if a.runs.ConflictsPath(source) {
		http.Error(response, "活动 Run 持有该 Script 或其后代的 Run Lease", http.StatusConflict)
		return
	}
	if err := a.managed.Move(source, destination); err != nil {
		http.Error(response, "无法移动条目："+err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAudit("move_entry", source+" -> "+destination, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(filepath.ToSlash(destination))
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
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
		return
	}
	relative := request.PathValue("path")
	if a.runs.ConflictsPath(relative) {
		http.Error(response, "活动 Run 持有该 Script 的 Run Lease", http.StatusConflict)
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

func (a *App) deleteFile(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if a.runs.ConflictsPath(request.FormValue("path")) {
		http.Error(response, "活动 Run 持有该 Script 或其后代的 Run Lease", http.StatusConflict)
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
	a.recordAudit("restore_trash", original, "succeeded", request.RemoteAddr)
	parent := pathpkg.Dir(filepath.ToSlash(original))
	if parent == "." {
		parent = ""
	}
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func (a *App) purgeTrash(response http.ResponseWriter, request *http.Request) {
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
	request.Body = http.MaxBytesReader(response, request.Body, 2<<30)
	reader, err := request.MultipartReader()
	if err != nil {
		http.Error(response, "上传请求必须使用 multipart/form-data", http.StatusBadRequest)
		return
	}
	var csrfToken, relative string
	fileCount := 0
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
		if err := a.managed.Upload(relative, filename, part, 1<<30); err != nil {
			_ = part.Close()
			http.Error(response, "上传失败："+err.Error(), http.StatusBadRequest)
			return
		}
		_ = part.Close()
		a.recordAudit("upload_file", filename, "succeeded", request.RemoteAddr)
	}
	if fileCount == 0 {
		http.Error(response, "未选择上传文件", http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, filesURL(relative), http.StatusSeeOther)
}

func (a *App) filesPage(response http.ResponseWriter, request *http.Request) {
	relative := strings.Trim(request.PathValue("path"), "/")
	entries, err := a.managed.List(relative)
	if err != nil {
		http.Error(response, "无法读取受管根目录："+err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = filesTemplate.Execute(response, struct {
		Entries     []managedfiles.Entry
		CSRFToken   string
		CurrentPath string
	}{Entries: entries, CSRFToken: current.csrfToken, CurrentPath: relative})
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
	newPassword := request.FormValue("new_password")
	if newPassword != request.FormValue("confirm_password") {
		http.Error(response, "两次输入的新密码不一致", http.StatusBadRequest)
		return
	}
	if !utf8.ValidString(newPassword) || utf8.RuneCountInString(newPassword) < 12 || len([]byte(newPassword)) > 256 || newPassword == username {
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
	if _, err := transaction.Exec("UPDATE admin SET password_hash = ?, must_change_password = 0 WHERE id = 1", newHash); err != nil {
		http.Error(response, "无法保存新密码", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions"); err != nil {
		http.Error(response, "无法撤销 Session", http.StatusInternalServerError)
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
	a.recordAudit("change_password", "admin", "succeeded", request.RemoteAddr)
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) login(response http.ResponseWriter, request *http.Request) {
	csrfCookie, err := request.Cookie(loginCSRFCookieName)
	if err != nil || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(request.FormValue("csrf_token"))) != 1 {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
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
		http.Error(response, "无法验证管理员", http.StatusInternalServerError)
		return
	}
	if request.FormValue("username") != username || !verifyPassword(request.FormValue("password"), passwordHash) {
		a.recordAudit("login", "admin", "failed", request.RemoteAddr)
		http.Error(response, "用户名或密码错误", http.StatusUnauthorized)
		return
	}

	token, err := randomToken(32)
	if err != nil {
		http.Error(response, "无法创建 Session", http.StatusInternalServerError)
		return
	}
	sessionCSRF, err := randomToken(32)
	if err != nil {
		http.Error(response, "无法创建 Session", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	if _, err := a.db.Exec(
		"INSERT INTO sessions (token_hash, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		hashToken(token), sessionCSRF, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix(),
	); err != nil {
		http.Error(response, "无法保存 Session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   request.TLS != nil,
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

type session struct {
	csrfToken  string
	mustChange bool
}

func validSessionCSRF(request *http.Request) bool {
	current, ok := request.Context().Value(sessionContextKey).(session)
	return ok && subtle.ConstantTimeCompare([]byte(current.csrfToken), []byte(request.FormValue("csrf_token"))) == 1
}

func (a *App) requireSession(allowPasswordChange bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		var current session
		var lastSeen, expiresAt int64
		err = a.db.QueryRow(`
			SELECT sessions.csrf_token, sessions.last_seen_at, sessions.expires_at, admin.must_change_password
			FROM sessions CROSS JOIN admin
			WHERE sessions.token_hash = ? AND admin.id = 1`, hashToken(cookie.Value),
		).Scan(&current.csrfToken, &lastSeen, &expiresAt, &current.mustChange)
		now := time.Now().UTC()
		if err != nil || now.Unix() >= expiresAt || now.Sub(time.Unix(lastSeen, 0)) >= 12*time.Hour {
			if err == nil {
				_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(cookie.Value))
			}
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
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

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>登录 · ScriptBoard</title></head>
<body><main><h1>登录</h1>
<form method="post" action="/login">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>用户名 <input name="username" autocomplete="username" required></label>
<label>密码 <input name="password" type="password" autocomplete="current-password" required></label>
<button type="submit">登录</button>
</form></main></body>
</html>`))

var accountTemplate = template.Must(template.New("account").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>修改密码 · ScriptBoard</title></head>
<body><main><h1>修改密码</h1>
<form method="post" action="/settings/account">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>当前密码 <input name="current_password" type="password" autocomplete="current-password" required></label>
<label>新密码 <input name="new_password" type="password" autocomplete="new-password" required></label>
<label>确认新密码 <input name="confirm_password" type="password" autocomplete="new-password" required></label>
<button type="submit">保存新密码</button>
</form></main></body>
</html>`))

var filesTemplate = template.Must(template.New("files").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>文件 · ScriptBoard</title></head>
<body><main><h1>文件</h1>
<form method="post" action="/files/mkdir"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label>目录名 <input name="name" required></label><button type="submit">新建目录</button></form>
<form method="post" action="/files/upload" enctype="multipart/form-data"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label>文件 <input name="files" type="file" multiple required></label><button type="submit">上传</button></form>
<table><thead><tr><th>名称</th><th>类型</th><th>大小</th><th>修改时间</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{.Name}}</td><td>{{if eq .Kind "directory"}}目录{{else if eq .Kind "regular"}}文件{{else}}受限{{end}}</td><td>{{.Size}}</td><td>{{.ModifiedAt}}</td></tr>
{{else}}<tr><td colspan="4">目录为空</td></tr>{{end}}
</tbody></table></main></body>
</html>`))

var trashTemplate = template.Must(template.New("trash").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>回收站 · ScriptBoard</title></head>
<body><main><h1>回收站</h1><table><thead><tr><th>原路径</th><th>删除时间</th><th>大小</th><th>操作</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{.OriginalPath}}</td><td>{{.DeletedAt}}</td><td>{{.Size}}</td><td><form method="post" action="/trash/restore"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">恢复</button></form><form method="post" action="/trash/purge"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">永久清理</button></form></td></tr>
{{else}}<tr><td colspan="4">回收站为空</td></tr>{{end}}
</tbody></table></main></body></html>`))

var textEditorTemplate = template.Must(template.New("text-editor").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>编辑 {{.Path}} · ScriptBoard</title></head>
<body><main><h1>编辑 {{.Path}}</h1><form method="post" action="/files/edit/{{.Path}}">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="digest" value="{{.Digest}}">
<textarea name="content" required>{{.Content}}</textarea><button type="submit">保存</button>
</form></main></body></html>`))

var runTemplate = template.Must(template.New("run").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>Run {{.Run.ID}} · ScriptBoard</title></head>
<body><main><h1>Run {{.Run.ID}}</h1><dl><dt>Script</dt><dd>{{.Run.ScriptPath}}</dd><dt>状态</dt><dd>{{.Run.Status}}</dd><dt>执行器</dt><dd>{{.Run.Executor}}</dd></dl>
{{if .Run.Error}}<p>{{.Run.Error}}</p>{{end}}{{if or (eq .Run.Status "running") (eq .Run.Status "stopping")}}<form method="post" action="/runs/{{.Run.ID}}/stop"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit">{{if eq .Run.Status "stopping"}}强制停止{{else}}停止{{end}}</button></form>{{end}}<form method="post" action="/runs/{{.Run.ID}}/quick-run"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>Quick Run 名称 <input name="name" required></label><button type="submit">保存 Quick Run</button></form><pre>{{range .Run.Events}}<span data-sequence="{{.Sequence}}" data-source="{{.Source}}">{{.Data}}</span>{{end}}</pre>
</main></body></html>`))

var quickRunsTemplate = template.Must(template.New("quick-runs").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>Quick Run · ScriptBoard</title></head><body><main><h1>Quick Run</h1>
<table><thead><tr><th>名称</th><th>Script</th><th>参数</th><th>操作</th></tr></thead><tbody>{{range .QuickRuns}}<tr><td>{{.Name}}</td><td>{{.ScriptPath}}</td><td>{{.ArgumentsTemplate}}</td><td><form method="post" action="/quick-runs/{{.ID}}/start"><input type="hidden" name="csrf_token" value="{{$.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><button type="submit">启动</button></form></td></tr>{{else}}<tr><td colspan="4">暂无 Quick Run</td></tr>{{end}}</tbody></table>
</main></body></html>`))

var variablesTemplate = template.Must(template.New("variables").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>Variable · ScriptBoard</title></head>
<body><main><h1>Variable</h1><form method="post" action="/variables"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label>名称 <input name="name" required></label><label>值 <textarea name="value"></textarea></label><button type="submit">创建</button></form>
<table><thead><tr><th>名称</th><th>值</th></tr></thead><tbody>{{range .Variables}}<tr><td>{{.Name}}</td><td>{{.Value}}</td></tr>{{else}}<tr><td colspan="2">暂无 Variable</td></tr>{{end}}</tbody></table>
</main></body></html>`))
