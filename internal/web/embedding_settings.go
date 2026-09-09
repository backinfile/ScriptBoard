package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type embeddingOriginsContextKey struct{}

type embeddingSettingsData struct {
	Locale             webLocale
	CSRFToken          string
	Mode               string
	Origins            string
	Error              string
	Saved              bool
	SettingsNavigation settingsNavigationData
}

func (a *App) loadEmbeddingOrigins(ctx context.Context) ([]string, error) {
	var encoded string
	err := a.db.QueryRowContext(ctx, `SELECT frame_ancestors FROM embedding_settings WHERE singleton = 1`).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return a.frameAncestors, nil
	}
	if err != nil {
		return nil, err
	}
	var origins []string
	if err := json.Unmarshal([]byte(encoded), &origins); err != nil {
		return nil, err
	}
	return origins, validateEmbeddingConfig(origins)
}

func (a *App) embeddingSettingsPage(response http.ResponseWriter, request *http.Request) {
	origins, _ := request.Context().Value(embeddingOriginsContextKey{}).([]string)
	mode := "deny"
	if len(origins) > 0 {
		mode = "specific"
	}
	for _, origin := range origins {
		if origin == "*" {
			mode = "all"
			origins = nil
			break
		}
	}
	a.renderEmbeddingSettings(response, request, http.StatusOK, mode, strings.Join(origins, "\n"), "")
}

func (a *App) updateEmbeddingSettings(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	mode, raw := request.FormValue("mode"), request.FormValue("origins")
	origins := []string{}
	errorKey := ""
	switch mode {
	case "deny":
	case "all":
		origins = []string{"*"}
	case "specific":
		for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
			origin := strings.TrimSpace(line)
			if origin == "" {
				continue
			}
			if origin == "*" {
				errorKey = "embedding.invalid_origins"
			}
			origins = append(origins, origin)
		}
		if len(origins) == 0 || len(origins) > 64 || len(raw) > 16384 || validateEmbeddingConfig(origins) != nil {
			errorKey = "embedding.invalid_origins"
		}
	default:
		errorKey = "embedding.invalid_mode"
	}
	if errorKey != "" {
		a.renderEmbeddingSettings(response, request, http.StatusUnprocessableEntity, mode, raw, errorKey)
		return
	}
	encoded, _ := json.Marshal(origins)
	current := request.Context().Value(sessionContextKey).(session)
	if _, err := a.db.ExecContext(request.Context(), `INSERT INTO embedding_settings
  (singleton, frame_ancestors, updated_at, updated_by_user_id) VALUES (1, ?, ?, ?)
  ON CONFLICT(singleton) DO UPDATE SET frame_ancestors = excluded.frame_ancestors,
  updated_at = excluded.updated_at, updated_by_user_id = excluded.updated_by_user_id`,
		string(encoded), time.Now().UTC().Unix(), current.userID); err != nil {
		http.Error(response, "Unable to save embedding settings", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "update_embedding_settings", string(encoded), "succeeded")
	http.Redirect(response, request, "/settings/embedding?saved=1", http.StatusSeeOther)
}

func (a *App) renderEmbeddingSettings(response http.ResponseWriter, request *http.Request, status int, mode, origins, errorKey string) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	data := embeddingSettingsData{Locale: locale, CSRFToken: current.csrfToken, Mode: mode, Origins: origins,
		Saved: request.URL.Query().Get("saved") == "1", SettingsNavigation: newSettingsNavigation(current, locale, "embedding")}
	if errorKey != "" {
		data.Error = webText(locale, errorKey)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = embeddingSettingsTemplate.Execute(response, data)
}
