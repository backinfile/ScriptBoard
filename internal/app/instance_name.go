package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultApplicationName  = "ScriptBoard"
	maxApplicationNameRunes = 32
)

type instanceNameSettingsData struct {
	Locale             webLocale
	CSRFToken          string
	DisplayName        string
	Error              string
	Saved              bool
	SettingsNavigation settingsNavigationData
}

func (a *App) loadInstanceDisplayName(ctx context.Context) (string, error) {
	var name string
	err := a.db.QueryRowContext(ctx, `SELECT display_name FROM instance_settings WHERE singleton = 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultApplicationName, nil
	}
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultApplicationName, nil
	}
	return name, nil
}

func validateInstanceDisplayName(value string) (string, string) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxApplicationNameRunes {
		return value, "instance_name.too_long"
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return value, "instance_name.invalid"
		}
	}
	return value, ""
}

func (a *App) instanceNameSettingsPage(response http.ResponseWriter, request *http.Request) {
	name, err := a.loadInstanceDisplayName(request.Context())
	if err != nil {
		http.Error(response, "Unable to load site name", http.StatusInternalServerError)
		return
	}
	a.renderInstanceNameSettings(response, request, http.StatusOK, name, "")
}

func (a *App) updateInstanceName(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(response, "Unable to read site name", http.StatusBadRequest)
		return
	}
	name, errorKey := validateInstanceDisplayName(request.FormValue("display_name"))
	if errorKey != "" {
		a.renderInstanceNameSettings(response, request, http.StatusUnprocessableEntity, name, errorKey)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	if _, err := a.db.ExecContext(request.Context(), `INSERT INTO instance_settings
		(singleton, display_name, updated_at, updated_by_user_id) VALUES (1, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET display_name = excluded.display_name,
		updated_at = excluded.updated_at, updated_by_user_id = excluded.updated_by_user_id`,
		name, time.Now().UTC().Unix(), current.userID); err != nil {
		http.Error(response, "Unable to save site name", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "update_instance_display_name", name, "succeeded")
	http.Redirect(response, request, "/settings/name?saved=1", http.StatusSeeOther)
}

func (a *App) renderInstanceNameSettings(response http.ResponseWriter, request *http.Request, status int, name, errorKey string) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	data := instanceNameSettingsData{
		Locale: locale, CSRFToken: current.csrfToken, DisplayName: name,
		Saved:              request.URL.Query().Get("saved") == "1",
		SettingsNavigation: newSettingsNavigation(current, locale, "name"),
	}
	if errorKey != "" {
		data.Error = webText(locale, errorKey)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = instanceNameSettingsTemplate.Execute(response, data)
}
