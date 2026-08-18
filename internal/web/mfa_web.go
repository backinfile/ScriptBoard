package web

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"scriptboard/internal/mfa"
	"scriptboard/internal/passkey"
)

type mfaPageData struct {
	Locale            webLocale
	CSRFToken         string
	Enabled           bool
	RecoveryRemaining int
	Enrollment        *mfa.Enrollment
	QRCode            template.HTML
	RecoveryCodes     []string
	Error             string
	Passkeys          []passkey.CredentialView
}

func (a *App) accountMFAPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	status, err := a.mfa.Status(current.userID)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	passkeys, err := a.passkeys.List(current.userID)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
		return
	}
	a.renderMFAPage(response, request, http.StatusOK, mfaPageData{Enabled: status.Enabled, RecoveryRemaining: status.RecoveryCodes, Passkeys: passkeys})
}

func (a *App) beginMFAEnrollment(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	enrollment, err := beginMFAWithContext(request.Context(), a.mfa, current.userID, current.username)
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
	codes, err := confirmMFAWithContext(request.Context(), a.mfa, current.userID, request.FormValue("mfa_code"))
	if err != nil {
		enrollment, _ := beginMFAWithContext(request.Context(), a.mfa, current.userID, current.username)
		a.recordAuditForRequest(request, "mfa_enrollment", current.username, "failed")
		a.renderMFAPage(response, request, http.StatusUnauthorized, mfaPageData{Enrollment: &enrollment, Error: webText(resolveWebLocale(request), "mfa.invalid_code")})
		return
	}
	a.recordAuditForRequest(request, "mfa_enrollment", current.username, "succeeded")
	if _, err := a.db.ExecContext(request.Context(), `DELETE FROM sessions WHERE user_id = ?`, current.userID); err != nil {
		_ = resetMFAWithContext(request.Context(), a.mfa, current.userID)
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
	if err := resetMFAWithContext(request.Context(), a.mfa, current.userID); err != nil {
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
	if data.Enrollment != nil {
		qrSVG, err := renderMFAEnrollmentQRCode(data.Enrollment.URI)
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusInternalServerError)
			return
		}
		data.QRCode = qrSVG
	}
	data.Locale = resolveWebLocale(request)
	data.CSRFToken = current.csrfToken
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = mfaTemplate.Execute(response, data)
}

func renderMFAEnrollmentQRCode(uri string) (template.HTML, error) {
	code, err := qrcode.New(uri, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bitmap := code.Bitmap()
	if len(bitmap) == 0 {
		return "", errors.New("empty MFA QR code")
	}
	var path strings.Builder
	for y, row := range bitmap {
		for x, dark := range row {
			if dark {
				path.WriteString("M")
				path.WriteString(strconv.Itoa(x))
				path.WriteString(" ")
				path.WriteString(strconv.Itoa(y))
				path.WriteString("h1v1h-1z")
			}
		}
	}
	size := strconv.Itoa(len(bitmap))
	// Every byte below is fixed markup or an integer derived from the QR bitmap;
	// the enrollment URI is never interpolated into HTML.
	svg := `<svg data-mfa-qr viewBox="0 0 ` + size + ` ` + size + `" role="img" aria-label="TOTP authenticator setup QR code" xmlns="http://www.w3.org/2000/svg"><rect width="100%" height="100%" fill="#fff"/><path d="` + path.String() + `" fill="#000"/></svg>`
	return template.HTML(svg), nil
}

func expireSessionCookie(response http.ResponseWriter, request *http.Request) {
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
}
