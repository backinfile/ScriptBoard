// Package mcpaccess owns the authentication boundary shared by the MCP
// protocol adapter and OAuth endpoints.
package mcpaccess

import (
	"encoding/json"
	"net/http"
	"strings"
)

const (
	ScopeObserve = "scriptboard.observe"
	ScopeExecute = "scriptboard.execute"
)

type HTTPBoundary struct{ canonicalExternalURL string }

func NewHTTPBoundary(canonicalExternalURL string) *HTTPBoundary {
	return &HTTPBoundary{canonicalExternalURL: strings.TrimRight(strings.TrimSpace(canonicalExternalURL), "/")}
}

func (boundary *HTTPBoundary) externalBase(request *http.Request) string {
	if boundary.canonicalExternalURL != "" {
		return boundary.canonicalExternalURL
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + request.Host
}

func (boundary *HTTPBoundary) ProtectedResourceMetadata(response http.ResponseWriter, request *http.Request) {
	base := boundary.externalBase(request)
	writeJSON(response, http.StatusOK, map[string]any{
		"resource": base + "/mcp", "authorization_servers": []string{base},
		"scopes_supported": []string{ScopeObserve, ScopeExecute}, "bearer_methods_supported": []string{"header"},
	})
}

func (boundary *HTTPBoundary) RequireOAuth(response http.ResponseWriter, request *http.Request) {
	base := boundary.externalBase(request)
	response.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource", scope="`+ScopeObserve+`"`)
	writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
