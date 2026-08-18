package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scriptboard/internal/identity"
)

const inlineStepUpHeader = "X-ScriptBoard-Step-Up"

type stepUpChallenge struct {
	Method         string `json:"method"`
	MFAEnabled     bool   `json:"mfa_enabled"`
	PasskeyEnabled bool   `json:"passkey_enabled"`
	CSRFToken      string `json:"csrf_token"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Error          string `json:"error,omitempty"`
}

func (a *App) requirePermission(required identity.Permission, next http.Handler) http.Handler {
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !identity.Allows(current.role, required) {
			if ok {
				a.recordAuditForRequest(request, "authorization_denied", request.Method+" "+request.URL.Path, "blocked")
			}
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	}))
	return declaredRouteHandler{auth: routeAuthSession, permission: required, handler: protected}
}

func (a *App) requireStepUp(required identity.Permission, next http.Handler) http.Handler {
	return a.requireRecentAuthentication(required, next)
}

func (a *App) requireRecentAuthentication(required identity.Permission, next http.Handler) http.Handler {
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !identity.Allows(current.role, required) {
			if ok {
				a.recordAuditForRequest(request, "authorization_denied", request.Method+" "+request.URL.Path, "blocked")
			}
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		challenge, err := a.currentStepUpChallenge(request, current)
		if err != nil {
			http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
			return
		}
		requiredAssurance := 1
		if challenge.Method == "second_factor" {
			requiredAssurance = 2
		}
		if current.authenticationAssurance < requiredAssurance || !identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			returnTo := stepUpReturnTarget(request)
			if request.Header.Get(inlineStepUpHeader) == "dialog" {
				response.Header().Set("Cache-Control", "no-store")
				response.Header().Set("Content-Type", "application/json; charset=utf-8")
				response.WriteHeader(http.StatusPreconditionRequired)
				_ = json.NewEncoder(response).Encode(challenge)
				return
			}
			location := "/auth/step-up?" + url.Values{"return_to": {returnTo}}.Encode()
			response.Header().Set("Cache-Control", "no-store")
			http.Redirect(response, request, location, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(response, request)
	}))
	return declaredRouteHandler{auth: routeAuthSession, permission: required, stepUp: true, handler: protected}
}

func (a *App) currentStepUpChallenge(request *http.Request, current session) (stepUpChallenge, error) {
	mfaStatus, err := a.mfa.Status(current.userID)
	if err != nil {
		return stepUpChallenge{}, err
	}
	passkeyUser, err := a.passkeys.User(current.userID, current.username)
	if err != nil {
		return stepUpChallenge{}, err
	}
	locale := resolveWebLocale(request)
	method := "password"
	if mfaStatus.Enabled || len(passkeyUser.Credentials) > 0 {
		method = "second_factor"
	}
	return stepUpChallenge{
		Method: method, MFAEnabled: mfaStatus.Enabled, PasskeyEnabled: len(passkeyUser.Credentials) > 0,
		CSRFToken: current.csrfToken, Title: webText(locale, "step_up.title"), Description: webText(locale, "step_up.description"),
	}, nil
}
func stepUpReturnTarget(request *http.Request) string {
	if referer := strings.TrimSpace(request.Referer()); referer != "" {
		if parsed, err := url.Parse(referer); err == nil && strings.EqualFold(parsed.Host, request.Host) {
			if target := safeStepUpReturnTo(parsed.RequestURI()); target != "/monitor" {
				return target
			}
		}
	}
	for _, candidate := range []struct{ prefix, target string }{
		{"/settings/users", "/settings/users"},
		{"/settings/name", "/settings/name"},
		{"/config/external-interfaces", "/config/external-interfaces"},
		{"/monitor/security", "/monitor/security"},
		{"/settings/updates", "/settings/updates"},
		{"/settings/state-backups", "/settings/state-backups"},
		{"/settings/ai", "/settings/ai"},
		{"/resources/databases", "/resources/databases"},
		{"/resources/inbox", "/resources/inbox"},
		{"/resources/files", "/resources/files"},
	} {
		if strings.HasPrefix(request.URL.Path, candidate.prefix) {
			return candidate.target
		}
	}
	return "/monitor"
}

func safeStepUpReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\x00") {
		return "/monitor"
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" ||
		strings.HasPrefix(parsed.Path, "//") || strings.ContainsAny(parsed.Path, "\\\r\n\x00") || strings.HasPrefix(parsed.Path, "/auth/step-up") {
		return "/monitor"
	}
	return parsed.RequestURI()
}
