package web

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/identity"
)

const setupTokenFilename = "initialization-token"
const setupTokenLifetime = 24 * time.Hour

func (a *App) setupPending() (bool, error) {
	var pending bool
	err := a.db.QueryRow("SELECT EXISTS(SELECT 1 FROM administrator_setup)").Scan(&pending)
	return pending, err
}

// A restart rotates unfinished setup credentials; existing installations have no pending row.
func (a *App) refreshSetupToken() error {
	pending, err := a.setupPending()
	if err != nil {
		return err
	}
	path := filepath.Join(a.stateRoot, "secrets", setupTokenFilename)
	if !pending {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return err
	}
	_, err = a.db.Exec("UPDATE administrator_setup SET token_hash = ?, expires_at = ? WHERE singleton = 1", hashToken(token), time.Now().Add(setupTokenLifetime).Unix())
	return err
}

// SetupURL exposes the local initialization link only to the process launching this instance.
func (a *App) SetupURL(baseURL string) (string, error) {
	pending, err := a.setupPending()
	if err != nil || !pending {
		return "", err
	}
	token, err := os.ReadFile(filepath.Join(a.stateRoot, "secrets", setupTokenFilename))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseURL, "/") + "/setup#token=" + strings.TrimSpace(string(token)), nil
}

func (a *App) redirectPendingSetup(w http.ResponseWriter, r *http.Request) bool {
	pending, err := a.setupPending()
	if err != nil {
		http.Error(w, webText(resolveWebLocale(r), "setup.unavailable"), http.StatusServiceUnavailable)
		return true
	}
	if pending {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}
	return pending
}

func (a *App) setupPage(w http.ResponseWriter, r *http.Request) {
	pending, err := a.setupPending()
	if err != nil {
		http.Error(w, webText(resolveWebLocale(r), "setup.unavailable"), http.StatusServiceUnavailable)
		return
	}
	if !pending {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var username string
	if err := a.db.QueryRow("SELECT username FROM users WHERE role = 'administrator'").Scan(&username); err != nil {
		http.Error(w, webText(resolveWebLocale(r), "setup.unavailable"), http.StatusServiceUnavailable)
		return
	}
	a.renderSetup(w, r, http.StatusOK, username, "")
}

func (a *App) renderSetup(w http.ResponseWriter, r *http.Request, status int, username, message string) {
	renderAuthenticationPage(w, r, status, loginPageData{Setup: true, SetupToken: r.FormValue("token"), StartupCredentialsApplied: a.startupCredentialsApplied, Username: username, Error: message})
}

func (a *App) completeSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	reset := setRequestReadDeadline(w, unauthenticatedFormReadTimeout)
	defer reset()
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginRequestBytes)
	defer removeMultipartForm(r)
	locale := resolveWebLocale(r)
	fail := func(status int, key string) {
		a.renderSetup(w, r, status, r.FormValue("username"), webText(locale, key))
	}
	if err := parseRequestForm(r, maxLoginRequestBytes); err != nil {
		fail(http.StatusBadRequest, "setup.invalid_form")
		return
	}
	cookie, err := r.Cookie(loginCSRFCookieName)
	if err != nil || cookie.Value == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.FormValue("csrf_token"))) != 1 {
		fail(http.StatusForbidden, "setup.invalid_form")
		return
	}
	key := a.loginRateKey("setup", loginRemoteHost(r))
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-r.Context().Done():
		return
	}
	if delay := a.loginRetryAfter(key); delay > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(delay.Seconds())+1))
		fail(http.StatusTooManyRequests, "setup.rate_limit")
		return
	}
	var expected string
	var expires int64
	err = a.db.QueryRow("SELECT token_hash, expires_at FROM administrator_setup WHERE singleton = 1").Scan(&expected, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	supplied := hashToken(strings.TrimSpace(r.FormValue("token")))
	if expires <= time.Now().Unix() || subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) != 1 {
		a.recordLoginFailure(key)
		a.recordAuditForRequest(r, "admin_setup", "administrator", "invalid_token")
		fail(http.StatusForbidden, "setup.invalid_token")
		return
	}
	username, password := strings.TrimSpace(r.FormValue("username")), r.FormValue("password")
	if !validUsername(username) {
		fail(http.StatusBadRequest, "setup.invalid_username")
		return
	}
	if password != r.FormValue("confirm_password") {
		fail(http.StatusBadRequest, "setup.password_mismatch")
		return
	}
	if validatePasswordPolicy(password, username) != nil {
		fail(http.StatusBadRequest, "setup.password_policy")
		return
	}
	hash, err := hashPassword(password)
	if err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	defer tx.Rollback()
	// Consuming the credential and setting the account share a transaction, including concurrent submissions.
	result, err := tx.Exec("DELETE FROM administrator_setup WHERE singleton = 1 AND token_hash = ? AND expires_at > ?", expected, time.Now().Unix())
	if err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		fail(http.StatusConflict, "setup.completed")
		return
	}
	if _, err = tx.Exec("UPDATE users SET username = ?, password_hash = ?, auth_version = auth_version + 1, updated_at = ? WHERE role = 'administrator'", username, hash, time.Now().Unix()); err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	if _, err = tx.Exec("DELETE FROM sessions"); err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	var authVersion int64
	if err = tx.QueryRow("SELECT auth_version FROM users WHERE id = 'administrator'").Scan(&authVersion); err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(http.StatusServiceUnavailable, "setup.unavailable")
		return
	}
	// Database consumption is authoritative even if the local file cannot be removed until restart.
	if err := os.Remove(filepath.Join(a.stateRoot, "secrets", setupTokenFilename)); err != nil && !os.IsNotExist(err) {
		a.recordAuditForRequest(r, "admin_setup_cleanup", "administrator", "failed")
	}
	a.clearLoginFailures(key)
	a.recordAuditForRequest(r, "admin_setup", username, "succeeded")
	a.finishLogin(w, r, "administrator", username, identity.RoleAdministrator, authVersion, 1)
}
