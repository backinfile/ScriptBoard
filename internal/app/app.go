package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

const initialPasswordFilename = "initial-admin-password"

const (
	sessionCookieName   = "scriptboard_session"
	loginCSRFCookieName = "scriptboard_login_csrf"
)

type contextKey string

const sessionContextKey contextKey = "session"

type Config struct {
	ManagedRoot string
	StateRoot   string
}

type App struct {
	db        *sql.DB
	stateRoot string
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

	application := &App{db: db, stateRoot: stateRoot}
	if err := application.initializeAdmin(stateRoot); err != nil {
		_ = db.Close()
		return nil, err
	}
	application.handler = application.routes(managedRoot)
	return application, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) Close() error {
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
	mux.Handle("GET /files/", a.requireSession(false, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte("<!doctype html><html lang=\"zh-CN\"><title>文件</title><body>文件</body></html>"))
	})))
	return mux
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
