package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxRemoteWebsiteSources  = 20
	maxRemoteWebsiteResponse = 4 << 20
)

type websiteMonitorRemoteSource struct {
	ID, Label, Endpoint, TokenHint string
	CreatedAt, UpdatedAt           time.Time
}

type websiteMonitorRemoteSourceView struct {
	websiteMonitorRemoteSource
	Snapshot websiteMonitorListDataView
	Error    string
}

type remoteWebsiteMonitorResponse struct {
	OK            bool                       `json:"ok"`
	Action        string                     `json:"action"`
	SchemaVersion int                        `json:"schema_version"`
	Data          websiteMonitorListDataView `json:"data"`
}

func (a *App) createWebsiteMonitorRemoteSource(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "error.forbidden"), http.StatusForbidden)
		return
	}
	label := strings.TrimSpace(request.FormValue("label"))
	endpoint, err := normalizeRemoteWebsiteEndpoint(request.FormValue("endpoint"))
	key := strings.TrimSpace(request.FormValue("key"))
	if err != nil || label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) || !validExternalKeyShape(key) {
		http.Error(response, webText(locale, "website.remote.invalid"), http.StatusBadRequest)
		return
	}
	var count int
	if err := a.db.QueryRowContext(request.Context(), `SELECT COUNT(*) FROM website_monitor_remote_sources`).Scan(&count); err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	if count >= maxRemoteWebsiteSources {
		http.Error(response, webText(locale, "website.remote.limit"), http.StatusConflict)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	result, err := a.db.ExecContext(request.Context(), `INSERT INTO website_monitor_remote_sources
		(id, label, endpoint, token_hint, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (
			SELECT 1 FROM website_monitor_remote_sources WHERE label = ? COLLATE NOCASE
		)`, id, label, endpoint, externalKeyHint(key), now.Unix(), now.Unix(), label)
	if err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		http.Error(response, webText(locale, "website.remote.duplicate"), http.StatusConflict)
		return
	}
	if err := a.remoteWebsites.Store(request.Context(), id, endpoint, key); err != nil {
		_ = a.remoteWebsites.Delete(request.Context(), id)
		_, _ = a.db.ExecContext(request.Context(), `DELETE FROM website_monitor_remote_sources WHERE id = ?`, id)
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_remote_website_monitor_source", id, "succeeded")
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func (a *App) deleteWebsiteMonitorRemoteSource(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(locale, "error.forbidden"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	var exists int
	if err := a.db.QueryRowContext(request.Context(), `SELECT EXISTS(SELECT 1 FROM website_monitor_remote_sources WHERE id = ?)`, id).Scan(&exists); err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	if exists == 0 {
		http.NotFound(response, request)
		return
	}
	if err := a.remoteWebsites.Delete(request.Context(), id); err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	result, err := a.db.ExecContext(request.Context(), `DELETE FROM website_monitor_remote_sources WHERE id = ?`, id)
	if err != nil {
		http.Error(response, webText(locale, "error.internal"), http.StatusInternalServerError)
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		http.NotFound(response, request)
		return
	}
	a.recordAuditForRequest(request, "delete_remote_website_monitor_source", id, "succeeded")
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func (a *App) websiteMonitorRemoteSources(ctx context.Context, locale webLocale) ([]websiteMonitorRemoteSourceView, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, label, endpoint, token_hint, created_at, updated_at
		FROM website_monitor_remote_sources ORDER BY created_at, id LIMIT ?`, maxRemoteWebsiteSources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []websiteMonitorRemoteSourceView
	for rows.Next() {
		var view websiteMonitorRemoteSourceView
		var createdAt, updatedAt int64
		if err := rows.Scan(&view.ID, &view.Label, &view.Endpoint, &view.TokenHint, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		view.CreatedAt, view.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		result = append(result, view)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	fetchContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	slots := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for index := range result {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-fetchContext.Done():
				result[index].Error = webText(locale, "website.remote.unavailable")
				return
			}
			payload, err := a.remoteWebsites.Fetch(fetchContext, result[index].ID, string(locale))
			if err != nil {
				result[index].Error = webText(locale, "website.remote.unavailable")
				return
			}
			snapshot, err := decodeRemoteWebsiteMonitors(payload)
			if err != nil {
				result[index].Error = webText(locale, "website.remote.unavailable")
				return
			}
			result[index].Snapshot = snapshot
		}()
	}
	wait.Wait()
	return result, nil
}

func decodeRemoteWebsiteMonitors(content json.RawMessage) (websiteMonitorListDataView, error) {
	if len(content) > maxRemoteWebsiteResponse {
		return websiteMonitorListDataView{}, errors.New("remote website monitor response is too large")
	}
	var payload remoteWebsiteMonitorResponse
	if err := json.Unmarshal(content, &payload); err != nil || !payload.OK || payload.Action != "website_monitor" || payload.SchemaVersion != 1 {
		return websiteMonitorListDataView{}, errors.New("invalid remote website monitor response")
	}
	if payload.Data.Total < 0 || payload.Data.Total != len(payload.Data.Monitors) || len(payload.Data.Monitors) > 1000 {
		return websiteMonitorListDataView{}, errors.New("invalid remote website monitor count")
	}
	return payload.Data, nil
}

func normalizeRemoteWebsiteEndpoint(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) > 2048 || !utf8.ValidString(value) {
		return "", errors.New("invalid endpoint")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid endpoint")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/trigger/"), "/")
	if parsed.RawQuery != "" || !strings.HasPrefix(parsed.EscapedPath(), "/trigger/") || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid interface path")
	}
	return parsed.String(), nil
}

func validExternalKeyShape(key string) bool {
	if !strings.HasPrefix(key, "sbk_") || len(key) > 128 {
		return false
	}
	identity, secret, ok := strings.Cut(strings.TrimPrefix(key, "sbk_"), ".")
	return ok && len(identity) == 16 && len(secret) == 43 && isBase64URL(identity) && isBase64URL(secret)
}

func isBase64URL(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return value != ""
}

func externalKeyHint(key string) string {
	if len(key) < 4 {
		return ""
	}
	identity, _, _ := strings.Cut(strings.TrimPrefix(key, "sbk_"), ".")
	return "sbk_" + identity + ".••••" + key[len(key)-4:]
}
