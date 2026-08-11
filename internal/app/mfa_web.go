package app

import (
	"errors"
	"net/http"

	"scriptboard/internal/mfa"
)

type mfaPageData struct {
	Locale            webLocale
	CSRFToken         string
	Enabled           bool
	RecoveryRemaining int
	Enrollment        *mfa.Enrollment
	RecoveryCodes     []string
	Error             string
}

func (a *App) accountMFAPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	status, err := a.mfa.Status(current.userID)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	a.renderMFAPage(response, request, http.StatusOK, mfaPageData{Enabled: status.Enabled, RecoveryRemaining: status.RecoveryCodes})
}

func (a *App) beginMFAEnrollment(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	enrollment, err := a.mfa.Begin(current.userID, current.username)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, mfa.ErrAlreadyEnabled) {
			status = http.StatusConflict
		}
		a.recordAuditForRequest(request, "mfa_enrollment", current.username, "failed")
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), status)
		return
	}
	a.recordAuditForRequest(request, "mfa_enrollment", current.username, "started")
	a.renderMFAPage(response, request, http.StatusOK, mfaPageData{Enrollment: &enrollment})
}

func (a *App) confirmMFAEnrollment(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	codes, err := a.mfa.Confirm(current.userID, request.FormValue("mfa_code"))
	if err != nil {
		enrollment, _ := a.mfa.Begin(current.userID, current.username)
		a.recordAuditForRequest(request, "mfa_enrollment", current.username, "failed")
		a.renderMFAPage(response, request, http.StatusUnauthorized, mfaPageData{Enrollment: &enrollment, Error: webText(resolveWebLocale(request), "mfa.invalid_code")})
		return
	}
	a.recordAuditForRequest(request, "mfa_enrollment", current.username, "succeeded")
	if _, err := a.db.ExecContext(request.Context(), `DELETE FROM sessions WHERE user_id = ?`, current.userID); err != nil {
		_ = a.mfa.Reset(current.userID)
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	a.cancelAuthenticatedRequests(current.userID)
	expireSessionCookie(response, request)
	a.renderMFAPage(response, request, http.StatusOK, mfaPageData{RecoveryCodes: codes})
}

func (a *App) resetMFA(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	if _, err := a.db.ExecContext(request.Context(), `DELETE FROM sessions WHERE user_id = ?`, current.userID); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	if err := a.mfa.Reset(current.userID); err != nil {
		a.recordAuditForRequest(request, "mfa_reset", current.username, "failed")
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "mfa_reset", current.username, "succeeded")
	a.cancelAuthenticatedRequests(current.userID)
	expireSessionCookie(response, request)
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (a *App) renderMFAPage(response http.ResponseWriter, request *http.Request, status int, data mfaPageData) {
	current := request.Context().Value(sessionContextKey).(session)
	data.Locale = resolveWebLocale(request)
	data.CSRFToken = current.csrfToken
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = mfaTemplate.Execute(response, data)
}

func expireSessionCookie(response http.ResponseWriter, request *http.Request) {
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
}
