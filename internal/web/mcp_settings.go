package web

import (
	"net/http"
	"strings"

	"scriptboard/internal/mcpaccess"
)

type mcpSettingsData struct {
	CSRFToken          string
	Clients            []mcpaccess.ClientView
	Authorizations     []mcpaccess.AuthorizationView
	Locale             webLocale
	SettingsNavigation settingsNavigationData
}

func (a *App) mcpSettingsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	clients, err := a.mcpStore.Clients(request.Context())
	if err != nil {
		http.Error(response, "Unable to load MCP clients", http.StatusInternalServerError)
		return
	}
	authorizations, err := a.mcpStore.AllAuthorizations(request.Context())
	if err != nil {
		http.Error(response, "Unable to load MCP authorizations", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = mcpSettingsTemplate.Execute(response, mcpSettingsData{CSRFToken: current.csrfToken, Clients: clients, Authorizations: authorizations, Locale: resolveWebLocale(request), SettingsNavigation: newSettingsNavigation(current, resolveWebLocale(request), "mcp")})
}
func (a *App) createMCPClient(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	redirects := []string{}
	for _, line := range strings.Split(request.FormValue("redirect_uris"), "\n") {
		if value := strings.TrimSpace(line); value != "" {
			redirects = append(redirects, value)
		}
	}
	client, err := a.mcpStore.RegisterPredefinedClient(request.Context(), request.FormValue("client_id"), request.FormValue("name"), redirects)
	if err != nil {
		http.Error(response, "invalid OAuth client metadata", http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "mcp_oauth_client_create", client.ClientID, "succeeded")
	http.Redirect(response, request, "/settings/mcp", http.StatusSeeOther)
}
func (a *App) revokeMCPClient(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := a.mcpStore.RevokeClient(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "OAuth client not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "mcp_oauth_client_revoke", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/settings/mcp", http.StatusSeeOther)
}

func (a *App) revokeMCPAuthorization(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := a.mcpStore.RevokeAuthorizationByID(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "OAuth authorization not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "mcp_oauth_revoke", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/settings/mcp", http.StatusSeeOther)
}
