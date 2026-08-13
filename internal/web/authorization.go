package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"scriptboard/internal/identity"
)

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
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !identity.Allows(current.role, required) {
			if ok {
				a.recordAuditForRequest(request, "authorization_denied", request.Method+" "+request.URL.Path, "blocked")
			}
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		if current.authenticationAssurance < 1 || !identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			returnTo := stepUpReturnTarget(request)
			location := "/auth/step-up?" + url.Values{"return_to": {returnTo}}.Encode()
			response.Header().Set("Cache-Control", "no-store")
			http.Redirect(response, request, location, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(response, request)
	}))
	return declaredRouteHandler{auth: routeAuthSession, permission: required, stepUp: true, handler: protected}
}

func (a *App) requireAAL2StepUp(required identity.Permission, next http.Handler) http.Handler {
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !identity.Allows(current.role, required) {
			if ok {
				a.recordAuditForRequest(request, "authorization_denied", request.Method+" "+request.URL.Path, "blocked")
			}
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		if current.authenticationAssurance < 2 || !identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			returnTo := stepUpReturnTarget(request)
			if current.authenticationAssurance < 2 {
				status, statusErr := a.mfa.Status(current.userID)
				passkeyUser, passkeyErr := a.passkeys.User(current.userID, current.username)
				if statusErr != nil || passkeyErr != nil {
					http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
					return
				}
				if !status.Enabled && len(passkeyUser.Credentials) == 0 {
					response.Header().Set("Cache-Control", "no-store")
					http.Redirect(response, request, "/settings/account/mfa", http.StatusSeeOther)
					return
				}
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

func (a *App) requireSecondFactorIfEnabled(required identity.Permission, next http.Handler) http.Handler {
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !identity.Allows(current.role, required) {
			if ok {
				a.recordAuditForRequest(request, "authorization_denied", request.Method+" "+request.URL.Path, "blocked")
			}
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		mfaStatus, mfaErr := a.mfa.Status(current.userID)
		passkeyUser, passkeyErr := a.passkeys.User(current.userID, current.username)
		if mfaErr != nil || passkeyErr != nil {
			http.Error(response, webText(resolveWebLocale(request), "mfa.unavailable"), http.StatusServiceUnavailable)
			return
		}
		if !mfaStatus.Enabled && len(passkeyUser.Credentials) == 0 {
			next.ServeHTTP(response, request)
			return
		}
		if current.authenticationAssurance < 2 || !identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			returnTo := stepUpReturnTarget(request)
			location := "/auth/step-up?" + url.Values{"mode": {"second-factor"}, "return_to": {returnTo}}.Encode()
			response.Header().Set("Cache-Control", "no-store")
			http.Redirect(response, request, location, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(response, request)
	}))
	return declaredRouteHandler{auth: routeAuthSession, permission: required, stepUp: true, handler: protected}
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
