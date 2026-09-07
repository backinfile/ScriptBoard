package web

import (
	"net/http"
	"scriptboard/internal/config"
	"strings"
)

type embeddingContextKey struct{}

func validateEmbeddingConfig(origins []string) error { return config.ValidateFrameAncestors(origins) }

func (a *App) contentSecurityPolicy() string {
	ancestors := "'none'"
	if len(a.frameAncestors) > 0 {
		ancestors = strings.Join(a.frameAncestors, " ")
	}
	// Use the configured parents on every page, including nested custom tabs.
	return "default-src 'self'; object-src 'none'; frame-ancestors " + ancestors + "; base-uri 'none'; form-action 'self'"
}

func embeddingSameSite(request *http.Request, fallback http.SameSite) http.SameSite {
	// Embedded HTTPS login needs both the session and login challenges across sites.
	if enabled, _ := request.Context().Value(embeddingContextKey{}).(bool); enabled && isSecureRequest(request) {
		return http.SameSiteNoneMode
	}
	return fallback
}
