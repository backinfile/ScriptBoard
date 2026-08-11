package app

import (
	"database/sql"
	"math"
	"net/http"
	"strconv"
	"time"
)

func (a *App) stepUpTask(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	returnTo := safeStepUpReturnTo(request.URL.Query().Get("return_to"))
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "step-up",
		Title:       webText(resolveWebLocale(request), "step_up.title"),
		Description: webText(resolveWebLocale(request), "step_up.description"),
		BackURL:     returnTo,
		Action:      "/auth/step-up",
		ReturnTo:    returnTo,
	})
}

func (a *App) stepUp(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	current := request.Context().Value(sessionContextKey).(session)
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	returnTo := safeStepUpReturnTo(request.FormValue("return_to"))
	keys := []string{
		a.loginRateKey("step-up-ip", request.RemoteAddr),
		a.loginRateKey("step-up-account", current.userID),
	}
	if retryAfter := a.loginRetryAfter(keys...); retryAfter > 0 {
		response.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		a.recordAuditForRequest(request, "step_up_authentication", current.username, "rate_limited")
		a.renderStepUpFailure(response, request, http.StatusTooManyRequests, returnTo, "step_up.rate_limited")
		return
	}
	var passwordHash string
	if err := a.db.QueryRowContext(request.Context(), `SELECT password_hash FROM users WHERE id = ? AND enabled = 1`, current.userID).Scan(&passwordHash); err != nil {
		if err == sql.ErrNoRows {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		http.Error(response, webText(resolveWebLocale(request), "step_up.unavailable"), http.StatusInternalServerError)
		return
	}
	if !verifyPasswordContext(request.Context(), request.FormValue("current_password"), passwordHash) {
		a.recordLoginFailure(keys...)
		a.recordAuditForRequest(request, "step_up_authentication", current.username, "failed")
		a.renderStepUpFailure(response, request, http.StatusUnauthorized, returnTo, "step_up.failed")
		return
	}
	now := time.Now().UTC().Unix()
	result, err := a.db.ExecContext(request.Context(), `UPDATE sessions
		SET authentication_assurance = 1, reauthenticated_at = ?
		WHERE token_hash = ? AND user_id = ? AND auth_version = ?`, now, current.tokenHash, current.userID, current.authVersion)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "step_up.unavailable"), http.StatusInternalServerError)
		return
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		http.Error(response, webText(resolveWebLocale(request), "step_up.unavailable"), http.StatusConflict)
		return
	}
	a.clearLoginFailures(keys...)
	a.recordAuditForRequest(request, "step_up_authentication", current.username, "succeeded")
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}

func (a *App) renderStepUpFailure(response http.ResponseWriter, request *http.Request, status int, returnTo, messageKey string) {
	locale := resolveWebLocale(request)
	a.renderTaskPageStatus(response, request, status, taskPageData{
		Kind:        "step-up",
		Title:       webText(locale, "step_up.title"),
		Description: webText(locale, "step_up.description"),
		BackURL:     returnTo,
		Action:      "/auth/step-up",
		ReturnTo:    returnTo,
		Error:       webText(locale, messageKey),
	})
}
