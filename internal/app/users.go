package app

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type userView struct {
	ID        string
	Username  string
	Role      userRole
	Enabled   bool
	CreatedAt time.Time
}

type usersPageData struct {
	Users              []userView
	GeneratedPassword  string
	GeneratedUsername  string
	CSRFToken          string
	Locale             webLocale
	SettingsNavigation settingsNavigationData
}

func validUsername(username string) bool {
	return utf8.ValidString(username) &&
		utf8.RuneCountInString(username) >= 1 &&
		utf8.RuneCountInString(username) <= 64 &&
		!strings.ContainsAny(username, "\r\n\x00")
}

func (a *App) listUsers() ([]userView, error) {
	rows, err := a.db.Query(`SELECT id, username, role, enabled, created_at
		FROM users ORDER BY CASE role WHEN 'administrator' THEN 0 ELSE 1 END, LOWER(username)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []userView
	for rows.Next() {
		var user userView
		var createdAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &user.Enabled, &createdAt); err != nil {
			return nil, err
		}
		user.CreatedAt = time.Unix(createdAt, 0).UTC()
		users = append(users, user)
	}
	return users, rows.Err()
}

func (a *App) renderUsersPage(response http.ResponseWriter, request *http.Request, status int, generatedUsername, generatedPassword string) {
	users, err := a.listUsers()
	if err != nil {
		http.Error(response, "无法读取用户", http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	if generatedPassword != "" {
		response.Header().Set("Cache-Control", "no-store")
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = usersTemplate.Execute(response, usersPageData{
		Users: users, GeneratedUsername: generatedUsername, GeneratedPassword: generatedPassword,
		CSRFToken: current.csrfToken, Locale: resolveWebLocale(request),
		SettingsNavigation: newSettingsNavigation(current, resolveWebLocale(request), "users"),
	})
}

func (a *App) usersPage(response http.ResponseWriter, request *http.Request) {
	a.renderUsersPage(response, request, http.StatusOK, "", "")
}

func (a *App) editUserTask(response http.ResponseWriter, request *http.Request) {
	user, err := a.userByID(request.PathValue("id"))
	if userNotFound(err) {
		http.Error(response, webText(resolveWebLocale(request), "users.not_found"), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "users.read_failed"), http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "user-edit",
		Title:       user.Username,
		Description: webText(resolveWebLocale(request), "users.edit_description"),
		BackURL:     "/settings/users",
		Action:      "/settings/users/" + user.ID + "/update",
		User:        user,
	})
}

func (a *App) createUser(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	username := strings.TrimSpace(request.FormValue("username"))
	role := userRole(request.FormValue("role"))
	if !validUsername(username) {
		http.Error(response, "用户名必须为 1 至 64 个有效 Unicode 字符", http.StatusBadRequest)
		return
	}
	if !validAssignableRole(role) {
		http.Error(response, "用户角色无效", http.StatusBadRequest)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建用户", http.StatusInternalServerError)
		return
	}
	password, err := randomToken(24)
	if err != nil {
		http.Error(response, "无法创建用户", http.StatusInternalServerError)
		return
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		http.Error(response, "无法创建用户", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC().Unix()
	_, err = a.db.Exec(`INSERT INTO users
		(id, username, password_hash, role, enabled, auth_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 1, ?, ?)`,
		id, username, passwordHash, role, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "用户名已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法创建用户", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_user", username, "succeeded")
	a.renderUsersPage(response, request, http.StatusCreated, username, password)
}

func (a *App) setUserEnabled(response http.ResponseWriter, request *http.Request, enabled bool) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	user, err := a.userByID(request.PathValue("id"))
	if userNotFound(err) {
		http.Error(response, "用户不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(response, "无法读取用户", http.StatusInternalServerError)
		return
	}
	if user.Role == roleAdministrator {
		http.Error(response, "系统管理员不能停用或恢复", http.StatusForbidden)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	now := time.Now().UTC().Unix()
	if _, err := transaction.Exec(`UPDATE users SET enabled = ?, auth_version = auth_version + 1, updated_at = ? WHERE id = ?`,
		enabled, now, user.ID); err != nil {
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions WHERE user_id = ?", user.ID); err != nil {
		http.Error(response, "无法撤销用户会话", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	a.cancelAuthenticatedRequests(user.ID)
	action := "disable_user"
	if enabled {
		action = "enable_user"
	}
	a.recordAuditForRequest(request, action, user.Username, "succeeded")
	http.Redirect(response, request, "/settings/users", http.StatusSeeOther)
}

func (a *App) disableUser(response http.ResponseWriter, request *http.Request) {
	a.setUserEnabled(response, request, false)
}

func (a *App) enableUser(response http.ResponseWriter, request *http.Request) {
	a.setUserEnabled(response, request, true)
}

func (a *App) updateUser(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	user, err := a.userByID(request.PathValue("id"))
	if userNotFound(err) {
		http.Error(response, "用户不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(response, "无法读取用户", http.StatusInternalServerError)
		return
	}
	if user.Role == roleAdministrator {
		http.Error(response, "系统管理员角色不能修改", http.StatusForbidden)
		return
	}
	username := strings.TrimSpace(request.FormValue("username"))
	role := userRole(request.FormValue("role"))
	if !validUsername(username) {
		http.Error(response, "用户名必须为 1 至 64 个有效 Unicode 字符", http.StatusBadRequest)
		return
	}
	if !validAssignableRole(role) {
		http.Error(response, "用户角色无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	_, err = transaction.Exec(`UPDATE users
		SET username = ?, role = ?, auth_version = auth_version + 1, updated_at = ?
		WHERE id = ?`, username, role, time.Now().UTC().Unix(), user.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "用户名已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions WHERE user_id = ?", user.ID); err != nil {
		http.Error(response, "无法撤销用户会话", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法更新用户", http.StatusInternalServerError)
		return
	}
	a.cancelAuthenticatedRequests(user.ID)
	if username != user.Username {
		a.recordAuditForRequest(request, "rename_user", user.Username+" -> "+username, "succeeded")
	}
	if role != user.Role {
		a.recordAuditForRequest(request, "change_user_role", username+" ("+string(user.Role)+" -> "+string(role)+")", "succeeded")
	}
	http.Redirect(response, request, "/settings/users", http.StatusSeeOther)
}

func (a *App) resetUserPassword(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF token is invalid", http.StatusForbidden)
		return
	}
	user, err := a.userByID(request.PathValue("id"))
	if userNotFound(err) {
		http.Error(response, "user not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(response, "could not read user", http.StatusInternalServerError)
		return
	}
	if user.Role == roleAdministrator {
		http.Error(response, "the system administrator password cannot be reset here", http.StatusForbidden)
		return
	}

	password, err := randomToken(24)
	if err != nil {
		http.Error(response, "could not generate password", http.StatusInternalServerError)
		return
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		http.Error(response, "could not hash password", http.StatusInternalServerError)
		return
	}

	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "could not reset password", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec(`
		UPDATE users
		SET password_hash = ?, auth_version = auth_version + 1, updated_at = ?
		WHERE id = ? AND role <> ?
	`, passwordHash, time.Now().UTC().Unix(), user.ID, roleAdministrator)
	if err != nil {
		http.Error(response, "could not reset password", http.StatusInternalServerError)
		return
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		http.Error(response, "could not reset password", http.StatusConflict)
		return
	}
	if _, err := transaction.Exec("DELETE FROM sessions WHERE user_id = ?", user.ID); err != nil {
		http.Error(response, "could not revoke user sessions", http.StatusInternalServerError)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "could not reset password", http.StatusInternalServerError)
		return
	}

	a.cancelAuthenticatedRequests(user.ID)
	a.recordAuditForRequest(request, "reset_user_password", user.Username, "succeeded")
	a.renderUsersPage(response, request, http.StatusCreated, user.Username, password)
}

func (a *App) userByID(id string) (userView, error) {
	var user userView
	var createdAt int64
	err := a.db.QueryRow(`SELECT id, username, role, enabled, created_at FROM users WHERE id = ?`, id).
		Scan(&user.ID, &user.Username, &user.Role, &user.Enabled, &createdAt)
	if err != nil {
		return userView{}, err
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	return user, nil
}

func userNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
