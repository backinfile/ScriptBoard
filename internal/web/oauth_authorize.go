package web

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scriptboard/internal/identity"
	"scriptboard/internal/mcpaccess"
)

var oauthConsentTemplate = template.Must(template.New("oauth-consent").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Authorize agent · ScriptBoard</title></head><body><main><h1>Authorize agent</h1><p><strong>{{.ClientName}}</strong> requests: {{.Scope}}</p><p>Only approve clients and redirect addresses you recognize.</p><form method="post" action="/oauth/authorize">{{range $k,$v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}<input type="hidden" name="csrf_token" value="{{.CSRF}}"><button name="decision" value="approve" type="submit">Authorize</button><button name="decision" value="deny" type="submit">Deny</button></form></main></body></html>`))

func (a *App) oauthResource() string { return strings.TrimRight(a.canonicalExternalURL, "/") + "/mcp" }

func (a *App) oauthAuthorize(response http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		a.oauthAuthorizeGet(response, request)
		return
	}
	a.oauthAuthorizePost(response, request)
}
func (a *App) oauthAuthorizeGet(response http.ResponseWriter, request *http.Request) {
	current, _, ok := a.loadSession(request)
	if !ok {
		if target := request.URL.RequestURI(); strings.HasPrefix(target, "/oauth/authorize?") && len(target) <= 4096 {
			http.SetCookie(response, &http.Cookie{Name: oauthReturnCookieName, Value: base64.RawURLEncoding.EncodeToString([]byte(target)), Path: "/", MaxAge: 300, HttpOnly: true, Secure: isSecureRequest(request), SameSite: http.SameSiteLaxMode})
		}
		http.Redirect(response, request, "/login", http.StatusSeeOther)
		return
	}
	client, redirect, scopes, err := mcpaccess.ValidateAuthorizationRequest(a.mcpStore, request, a.oauthResource())
	if err != nil {
		http.Error(response, "invalid OAuth authorization request", http.StatusBadRequest)
		return
	}
	normalized, err := mcpaccess.NormalizeScopes(string(current.role), scopes)
	if err != nil {
		http.Error(response, "requested scope is not permitted", http.StatusForbidden)
		return
	}
	fields := map[string]string{}
	for _, name := range []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "resource"} {
		fields[name] = request.URL.Query().Get(name)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_ = oauthConsentTemplate.Execute(response, map[string]any{"ClientName": client.Name, "Scope": strings.Join(normalized, " "), "Fields": fields, "CSRF": current.csrfToken, "Redirect": redirect})
}
func (a *App) oauthAuthorizePost(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	redirect := request.FormValue("redirect_uri")
	state := request.FormValue("state")
	query := request.URL.Query()
	for _, name := range []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "resource"} {
		query.Set(name, request.FormValue(name))
	}
	copy := request.Clone(request.Context())
	copy.URL = &url.URL{RawQuery: query.Encode()}
	_, validatedRedirect, scopes, err := mcpaccess.ValidateAuthorizationRequest(a.mcpStore, copy, a.oauthResource())
	if err != nil || validatedRedirect != redirect {
		http.Error(response, "invalid OAuth authorization request", http.StatusBadRequest)
		return
	}
	if request.FormValue("decision") != "approve" {
		a.recordAuditForRequest(request, "mcp_oauth_consent", request.FormValue("client_id"), "denied")
		http.Redirect(response, request, oauthRedirect(redirect, "error", "access_denied", state), http.StatusSeeOther)
		return
	}
	normalized, err := mcpaccess.NormalizeScopes(string(current.role), scopes)
	if err != nil {
		http.Error(response, "requested scope is not permitted", http.StatusForbidden)
		return
	}
	for _, scope := range normalized {
		if scope == mcpaccess.ScopeExecute && !identity.RecentAuthenticationValid(current.reauthenticatedAt, time.Now().UTC()) {
			http.Error(response, "recent authentication is required for execute scope", http.StatusForbidden)
			return
		}
	}
	code, err := a.mcpStore.IssueCode(request.Context(), current.userID, request.FormValue("client_id"), redirect, request.FormValue("resource"), request.FormValue("code_challenge"), normalized)
	if err != nil {
		http.Error(response, "authorization could not be issued", http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "mcp_oauth_consent", request.FormValue("client_id"), "approved")
	http.Redirect(response, request, oauthRedirect(redirect, "code", code, state), http.StatusSeeOther)
}
func oauthRedirect(redirect, key, value, state string) string {
	target, err := url.Parse(redirect)
	if err != nil {
		return "/"
	}
	query := target.Query()
	query.Set(key, value)
	if state != "" {
		query.Set("state", state)
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func (a *App) revokeOwnAgentAuthorization(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	if err := a.mcpStore.RevokeAuthorization(request.Context(), current.userID, request.PathValue("id")); err != nil {
		http.Error(response, "agent connection not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "mcp_oauth_revoke", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/settings/account", http.StatusSeeOther)
}
