package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/runmanager"
)

type externalKeyView struct {
	externaltrigger.Key
	Status, StatusText string
	EnabledEntries     int
	CanCopy            bool
}

type externalEntryView struct {
	externaltrigger.Entry
	TypeText, TargetText, PreviewURL string
}

type externalInvocationView struct {
	externaltrigger.Invocation
	ActionText, ResultText string
}

type externalInterfacesPageData struct {
	ActiveTab  string
	Keys       []externalKeyView
	Entries    map[string][]externalEntryView
	Requests   []externalInvocationView
	Filters    auditFilters
	Pagination paginationView
	CSRFToken  string
	Locale     webLocale
	Now        time.Time
}

type externalInterfaceFormData struct {
	Kind, Title, Description, BackURL, Action, CSRFToken string
	CallMethod, CallURL, CallBody, TypeText, TargetText  string
	PreviewURL, FormError                                string
	Locale                                               webLocale
	Key                                                  externaltrigger.Key
	Secret                                               string
	QuickRuns                                            []externalTargetOption
	Variables                                            []externalTargetOption
	Entry                                                externaltrigger.Entry
	LogConfig                                            externaltrigger.LogConfig
	UploadConfig                                         externaltrigger.UploadConfig
	QuickRunConfig                                       externaltrigger.QuickRunConfig
	VariableConfig                                       externaltrigger.VariableConfig
	EntryEnabled                                         bool
	Submitted                                            bool
	LogMessageLimitInput, UploadMaxBytesInput            string
	UploadExtensionsInput, VariableMinimumInput          string
	VariableMaximumInput, VariableMaxLengthInput         string
	VariableOptionsInput                                 string
	KeyLabelInput, KeyDurationInput                      string
	KeyFormSubmitted, KeyEnabledInput                    bool
}

type externalTargetOption struct{ Value, Label string }

func (a *App) externalInterfacesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	now := time.Now().UTC()
	activeTab := "interfaces"
	if request.URL.Query().Get("tab") == "activity" {
		activeTab = "activity"
	}
	filters, err := parseAuditFilters(request.URL.Query())
	if err != nil {
		key := "common.invalid_date_range"
		if errors.Is(err, errDateRangeOrder) {
			key = "common.invalid_date_order"
		}
		http.Error(response, webText(locale, key), http.StatusBadRequest)
		return
	}
	keys, err := a.externalTriggers.List(request.Context())
	if err != nil {
		http.Error(response, "Unable to read External Interfaces", http.StatusInternalServerError)
		return
	}
	invocationFilter := externaltrigger.InvocationFilter{
		Query: filters.Query, FromUnix: filters.FromUnix, ToExclusiveUnix: filters.ToExclusiveUnix,
		HasFromDate: filters.HasFromDate, HasToDate: filters.HasToDate,
	}
	total, err := a.externalTriggers.CountInvocations(request.Context(), invocationFilter)
	if err != nil {
		http.Error(response, "Unable to read External Interface activity", http.StatusInternalServerError)
		return
	}
	pagination := newPagination(request, total)
	invocations, err := a.externalTriggers.FindInvocations(request.Context(), invocationFilter, listPageSize, pagination.Start)
	if err != nil {
		http.Error(response, "Unable to read External Interface activity", http.StatusInternalServerError)
		return
	}
	requests := make([]externalInvocationView, 0, len(invocations))
	for _, invocation := range invocations {
		requests = append(requests, externalInvocationView{
			Invocation: invocation,
			ActionText: externalActionText(locale, invocation.ActionType),
			ResultText: webText(locale, "external.result."+invocation.Result),
		})
	}
	views := make([]externalKeyView, 0, len(keys))
	entries := make(map[string][]externalEntryView, len(keys))
	for _, key := range keys {
		view := externalKeyView{Key: key, Status: "disabled", StatusText: webText(locale, "external.disabled")}
		if _, secretErr := a.externalTriggers.KeySecret(key.ID); secretErr == nil {
			view.CanCopy = true
		}
		if key.Expired(now) {
			view.Status, view.StatusText = "expired", webText(locale, "external.expired")
		} else if key.Enabled {
			view.Status, view.StatusText = "enabled", webText(locale, "external.enabled")
		}
		for _, entry := range key.Entries {
			if entry.Enabled {
				view.EnabledEntries++
			}
			entryView := externalEntryView{
				Entry: entry, TypeText: externalActionText(locale, entry.Type), TargetText: externalTargetText(entry),
			}
			if entry.Type == externaltrigger.ActionLog {
				var config externaltrigger.LogConfig
				if entry.DecodeConfig(&config) == nil && config.File != "" {
					entryView.PreviewURL = routeFileURL("/resources/files/view", config.File)
				}
			}
			entries[key.ID] = append(entries[key.ID], entryView)
		}
		views = append(views, view)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := externalInterfacesTemplate.Execute(response, externalInterfacesPageData{
		ActiveTab: activeTab, Keys: views, Entries: entries, Requests: requests, Filters: filters, Pagination: pagination,
		CSRFToken: current.csrfToken, Locale: locale, Now: now,
	}); err != nil {
		http.Error(response, "Unable to render External Interfaces", http.StatusInternalServerError)
	}
}

func (a *App) copyExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	secret, err := a.externalTriggers.KeySecret(id)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "external.key_copy_unavailable"), http.StatusConflict)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	a.recordAuditForRequest(request, "copy_external_interface_key", id, "succeeded")
	_ = json.NewEncoder(response).Encode(map[string]string{"key": secret})
}

func externalActionText(locale webLocale, action externaltrigger.ActionType) string {
	return webText(locale, "external.action."+string(action))
}

func externalTargetText(entry externaltrigger.Entry) string {
	switch entry.Type {
	case externaltrigger.ActionLog:
		var config externaltrigger.LogConfig
		if entry.DecodeConfig(&config) == nil {
			return config.File
		}
	}
	return entry.Target
}

func (a *App) newExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "key-new", Title: webText(locale, "external.create_key_title"), Description: webText(locale, "external.create_key_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys", CSRFToken: current.csrfToken, Locale: locale,
	})
}

func (a *App) createExternalKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	expiresAt, err := externalExpiry(request.FormValue("duration"), time.Now().UTC())
	if err != nil {
		a.renderExternalKeySubmissionError(response, request, "key-new", externaltrigger.Key{}, webText(resolveWebLocale(request), "external.key_save_error"))
		return
	}
	key, _, err := a.externalTriggers.CreateKey(request.Context(), externaltrigger.CreateKeyInput{
		Label: request.FormValue("label"), Enabled: request.FormValue("enabled") == "1", ExpiresAt: expiresAt,
	})
	if err != nil {
		if errors.Is(err, externaltrigger.ErrKeyLabelExists) {
			a.renderExternalKeySubmissionError(response, request, "key-new", externaltrigger.Key{}, webText(resolveWebLocale(request), "external.key_name_exists"))
			return
		}
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "create_external_interface_key", key.ID, "succeeded")
	locale := resolveWebLocale(request)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusCreated)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "key-created", Title: webText(locale, "external.key_created"), Description: webText(locale, "external.key_created_description"),
		BackURL: "/config/external-interfaces", Locale: locale, Key: key,
	})
}

func (a *App) editExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "key-edit", Title: webText(locale, "external.edit_key"), Description: webText(locale, "external.edit_key_description"), BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys/" + key.ID, CSRFToken: current.csrfToken, Locale: locale, Key: key})
}

func (a *App) rotateExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "key-rotate", Title: webText(locale, "external.rotate"), Description: webText(locale, "external.rotate_drawer_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys/" + key.ID + "/rotate",
		CSRFToken: current.csrfToken, Locale: locale, Key: key,
	})
}

func (a *App) updateExternalKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	label := strings.TrimSpace(request.FormValue("label"))
	if label == "" || len([]byte(label)) > 128 || !utf8.ValidString(label) {
		a.renderExternalKeySubmissionError(response, request, "key-edit", key, webText(resolveWebLocale(request), "external.key_save_error"))
		return
	}
	expiresAt := key.ExpiresAt
	if duration := request.FormValue("duration"); duration != "keep" {
		expiresAt, err = externalExpiry(duration, time.Now().UTC())
		if err != nil {
			a.renderExternalKeySubmissionError(response, request, "key-edit", key, webText(resolveWebLocale(request), "external.key_save_error"))
			return
		}
	}
	if err := a.externalTriggers.UpdateKey(request.Context(), key.ID, label, expiresAt); err != nil {
		if errors.Is(err, externaltrigger.ErrKeyLabelExists) {
			a.renderExternalKeySubmissionError(response, request, "key-edit", key, webText(resolveWebLocale(request), "external.key_name_exists"))
			return
		}
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if key.Expired(time.Now().UTC()) && key.Enabled {
		if err := a.externalTriggers.SetKeyEnabled(request.Context(), key.ID, false); err != nil {
			http.Error(response, "Unable to close expired External Interface key", http.StatusInternalServerError)
			return
		}
	}
	a.recordAuditForRequest(request, "update_external_interface_key", key.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) renderExternalKeySubmissionError(response http.ResponseWriter, request *http.Request, kind string, key externaltrigger.Key, message string) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	title, description, action := webText(locale, "external.create_key_title"), webText(locale, "external.create_key_description"), "/config/external-interfaces/keys"
	if kind == "key-edit" {
		title, description, action = webText(locale, "external.edit_key"), webText(locale, "external.edit_key_description"), "/config/external-interfaces/keys/"+key.ID
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusUnprocessableEntity)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: kind, Title: title, Description: description, BackURL: "/config/external-interfaces", Action: action,
		CSRFToken: current.csrfToken, Locale: locale, Key: key, FormError: message,
		KeyFormSubmitted: true, KeyLabelInput: request.FormValue("label"), KeyDurationInput: request.FormValue("duration"), KeyEnabledInput: request.FormValue("enabled") == "1",
	})
}

func (a *App) toggleExternalKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	if err := a.externalTriggers.SetKeyEnabled(request.Context(), key.ID, !key.Enabled); err != nil {
		http.Error(response, "Unable to update External Interface key", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "toggle_external_interface_key", key.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) rotateExternalKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	key, _, err := a.externalTriggers.RotateKey(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "rotate_external_interface_key", key.ID, "succeeded")
	locale := resolveWebLocale(request)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusCreated)
	renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "key-rotated", Title: webText(locale, "external.key_rotated"), Description: webText(locale, "external.key_rotated_description"), BackURL: "/config/external-interfaces", Locale: locale, Key: key})
}

func (a *App) deleteExternalKey(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.externalTriggers.DeleteKey(request.Context(), id); err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "delete_external_interface_key", id, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func externalExpiry(duration string, now time.Time) (*time.Time, error) {
	var value time.Time
	switch duration {
	case "1h":
		value = now.Add(time.Hour)
	case "1d":
		value = now.Add(24 * time.Hour)
	case "7d":
		value = now.Add(7 * 24 * time.Hour)
	case "30d":
		value = now.Add(30 * 24 * time.Hour)
	case "never", "":
		return nil, nil
	default:
		return nil, errors.New("invalid key duration")
	}
	value = value.UTC()
	return &value, nil
}

func (a *App) newExternalEntryTask(response http.ResponseWriter, request *http.Request) {
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	quickRuns, variables, err := a.externalTargetOptions()
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "entry-new", Title: webText(locale, "external.add_function_title"), Description: webText(locale, "external.add_function_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys/" + key.ID + "/entries",
		CSRFToken: current.csrfToken, Locale: locale, Key: key, QuickRuns: quickRuns, Variables: variables, EntryEnabled: true,
	})
}

func (a *App) externalEntryDetail(response http.ResponseWriter, request *http.Request) {
	entry, err := a.externalTriggers.Entry(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	key, err := a.externalTriggers.Key(request.Context(), entry.KeyID)
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	locale := resolveWebLocale(request)
	current := request.Context().Value(sessionContextKey).(session)
	previewURL := ""
	if entry.Type == externaltrigger.ActionLog {
		var config externaltrigger.LogConfig
		if entry.DecodeConfig(&config) == nil && config.File != "" {
			previewURL = routeFileURL("/resources/files/view", config.File)
		}
	}
	callBodyKey := "external.call_body.quick_run"
	switch entry.Type {
	case externaltrigger.ActionLog:
		callBodyKey = "external.call_body.log"
	case externaltrigger.ActionUpload:
		callBodyKey = "external.call_body.upload"
	case externaltrigger.ActionVariable:
		callBodyKey = "external.call_body.variable"
	}
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "entry-detail", Title: entry.Label, Description: webText(locale, "external.function_details_description"),
		BackURL: "/config/external-interfaces", CSRFToken: current.csrfToken, Locale: locale, Key: key, Entry: entry, EntryEnabled: entry.Enabled,
		CallMethod: http.MethodPost, CallURL: "/trigger?name=" + url.QueryEscape(entry.Name), CallBody: webText(locale, callBodyKey),
		TypeText: externalActionText(locale, entry.Type), TargetText: externalTargetText(entry), PreviewURL: previewURL,
	})
}

func (a *App) editExternalEntryTask(response http.ResponseWriter, request *http.Request) {
	entry, err := a.externalTriggers.Entry(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	key, err := a.externalTriggers.Key(request.Context(), entry.KeyID)
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	quickRuns, variables, err := a.externalTargetOptions()
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	data := externalInterfaceFormData{Kind: "entry-edit", BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/entries/" + entry.ID, Key: key, Entry: entry, EntryEnabled: entry.Enabled, QuickRuns: quickRuns, Variables: variables}
	data.Locale = resolveWebLocale(request)
	data.Title, data.Description = webText(data.Locale, "external.edit_function"), webText(data.Locale, "external.edit_function_description")
	data.CSRFToken = request.Context().Value(sessionContextKey).(session).csrfToken
	switch entry.Type {
	case externaltrigger.ActionLog:
		_ = entry.DecodeConfig(&data.LogConfig)
	case externaltrigger.ActionUpload:
		_ = entry.DecodeConfig(&data.UploadConfig)
	case externaltrigger.ActionQuickRun:
		_ = entry.DecodeConfig(&data.QuickRunConfig)
	case externaltrigger.ActionVariable:
		_ = entry.DecodeConfig(&data.VariableConfig)
	}
	renderExternalInterfaceForm(response, data)
}

func (a *App) externalTargetOptions() ([]externalTargetOption, []externalTargetOption, error) {
	quickRows, err := a.db.Query("SELECT id, name FROM quick_runs ORDER BY name, id")
	if err != nil {
		return nil, nil, err
	}
	var quickRuns []externalTargetOption
	for quickRows.Next() {
		var option externalTargetOption
		if err := quickRows.Scan(&option.Value, &option.Label); err != nil {
			_ = quickRows.Close()
			return nil, nil, err
		}
		quickRuns = append(quickRuns, option)
	}
	if err := quickRows.Close(); err != nil {
		return nil, nil, err
	}
	variableRows, err := a.db.Query("SELECT name FROM variables WHERE is_password = 0 ORDER BY name")
	if err != nil {
		return nil, nil, err
	}
	defer variableRows.Close()
	var variables []externalTargetOption
	for variableRows.Next() {
		var option externalTargetOption
		if err := variableRows.Scan(&option.Value); err != nil {
			return nil, nil, err
		}
		option.Label = option.Value
		variables = append(variables, option)
	}
	return quickRuns, variables, variableRows.Err()
}

func (a *App) createExternalEntry(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	keyID := request.PathValue("id")
	key, err := a.externalTriggers.Key(request.Context(), keyID)
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	actionType := externaltrigger.ActionType(request.FormValue("action_type"))
	config, target, err := a.externalEntryConfig(request, actionType)
	if err != nil {
		a.renderExternalEntrySubmissionError(response, request, key)
		return
	}
	entry, err := a.externalTriggers.CreateEntry(request.Context(), externaltrigger.CreateEntryInput{
		KeyID: keyID, Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: actionType,
		Target: target, Enabled: request.FormValue("enabled") == "1", Config: config,
	})
	if err != nil {
		a.renderExternalEntrySubmissionError(response, request, key)
		return
	}
	a.recordAuditForRequest(request, "create_external_interface_entry", entry.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) renderExternalEntrySubmissionError(response http.ResponseWriter, request *http.Request, key externaltrigger.Key) {
	quickRuns, variables, err := a.externalTargetOptions()
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	locale := resolveWebLocale(request)
	data := externalInterfaceFormData{
		Kind: "entry-new", Title: webText(locale, "external.add_function_title"), Description: webText(locale, "external.add_function_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys/" + key.ID + "/entries",
		CSRFToken: request.Context().Value(sessionContextKey).(session).csrfToken, Locale: locale, Key: key,
		QuickRuns: quickRuns, Variables: variables, EntryEnabled: request.FormValue("enabled") == "1", Submitted: true,
		FormError:            webText(locale, "external.entry_save_error"),
		Entry:                externaltrigger.Entry{Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: externaltrigger.ActionType(request.FormValue("action_type"))},
		LogConfig:            externaltrigger.LogConfig{File: request.FormValue("log_file"), Category: request.FormValue("log_category")},
		UploadConfig:         externaltrigger.UploadConfig{Directory: request.FormValue("upload_directory"), ConflictPolicy: request.FormValue("upload_conflict")},
		QuickRunConfig:       externaltrigger.QuickRunConfig{QuickRunID: request.FormValue("quick_run_id")},
		VariableConfig:       externaltrigger.VariableConfig{VariableName: request.FormValue("variable_name"), Type: externaltrigger.VariableType(request.FormValue("variable_type")), Pattern: request.FormValue("variable_pattern"), AllowEmpty: request.FormValue("variable_allow_empty") == "1"},
		LogMessageLimitInput: request.FormValue("log_message_limit"), UploadMaxBytesInput: request.FormValue("upload_max_bytes"),
		UploadExtensionsInput: request.FormValue("upload_extensions"), VariableMinimumInput: request.FormValue("variable_minimum"),
		VariableMaximumInput: request.FormValue("variable_maximum"), VariableMaxLengthInput: request.FormValue("variable_max_length"),
		VariableOptionsInput: request.FormValue("variable_options"),
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusUnprocessableEntity)
	_ = externalInterfaceFormTemplate.Execute(response, data)
}

func (a *App) updateExternalEntry(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if _, err := a.externalTriggers.Entry(request.Context(), id); err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	actionType := externaltrigger.ActionType(request.FormValue("action_type"))
	config, target, err := a.externalEntryConfig(request, actionType)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := a.externalTriggers.UpdateEntry(request.Context(), externaltrigger.UpdateEntryInput{ID: id, Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: actionType, Target: target, Enabled: request.FormValue("enabled") == "1", Config: config})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "update_external_interface_entry", entry.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) toggleExternalEntry(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	entry, err := a.externalTriggers.Entry(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	if err := a.externalTriggers.SetEntryEnabled(request.Context(), entry.ID, !entry.Enabled); err != nil {
		http.Error(response, "Unable to update External Interface entry", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "toggle_external_interface_entry", entry.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) deleteExternalEntry(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.externalTriggers.DeleteEntry(request.Context(), id); err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "delete_external_interface_entry", id, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) externalEntryConfig(request *http.Request, actionType externaltrigger.ActionType) (any, string, error) {
	switch actionType {
	case externaltrigger.ActionLog:
		limit, err := strconv.Atoi(request.FormValue("log_message_limit"))
		if err != nil {
			return nil, "", errors.New("invalid log message limit")
		}
		path, err := a.files.PrepareAppendFile(strings.TrimSpace(request.FormValue("log_file")))
		if err != nil {
			return nil, "", fmt.Errorf("invalid log file: %w", err)
		}
		return externaltrigger.LogConfig{File: path, Category: strings.TrimSpace(request.FormValue("log_category")), MaxMessageBytes: limit}, path, nil
	case externaltrigger.ActionUpload:
		maximum, err := strconv.ParseInt(request.FormValue("upload_max_bytes"), 10, 64)
		if err != nil {
			return nil, "", errors.New("invalid upload byte limit")
		}
		directory := strings.TrimSpace(request.FormValue("upload_directory"))
		prepared, err := a.files.PrepareDirectory(directory)
		if err != nil {
			return nil, "", fmt.Errorf("invalid upload directory: %w", err)
		}
		directory = prepared.Path
		extensions := strings.FieldsFunc(request.FormValue("upload_extensions"), func(value rune) bool { return value == ',' || value == '\n' || value == '\r' })
		return externaltrigger.UploadConfig{Directory: directory, MaxBytes: maximum, Extensions: extensions, ConflictPolicy: request.FormValue("upload_conflict")}, directory, nil
	case externaltrigger.ActionQuickRun:
		id := strings.TrimSpace(request.FormValue("quick_run_id"))
		if _, err := a.loadQuickRun(id); err != nil {
			return nil, "", errors.New("quick run does not exist")
		}
		return externaltrigger.QuickRunConfig{QuickRunID: id}, id, nil
	case externaltrigger.ActionVariable:
		name := strings.TrimSpace(request.FormValue("variable_name"))
		var isPassword bool
		if err := a.db.QueryRow("SELECT is_password FROM variables WHERE name = ?", name).Scan(&isPassword); err != nil || isPassword {
			return nil, "", errors.New("variable does not exist or is password-protected")
		}
		config, err := parseExternalVariableConfig(request, name)
		return config, name, err
	default:
		return nil, "", errors.New("invalid action type")
	}
}

func parseExternalVariableConfig(request *http.Request, name string) (externaltrigger.VariableConfig, error) {
	config := externaltrigger.VariableConfig{VariableName: name, Type: externaltrigger.VariableType(request.FormValue("variable_type")), AllowEmpty: request.FormValue("variable_allow_empty") == "1"}
	switch config.Type {
	case externaltrigger.VariableBoolean:
	case externaltrigger.VariableInteger:
		fields := []struct {
			name        string
			destination **int64
		}{{"variable_minimum", &config.Minimum}, {"variable_maximum", &config.Maximum}}
		for _, field := range fields {
			if value := strings.TrimSpace(request.FormValue(field.name)); value != "" {
				parsed, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return config, errors.New("invalid integer constraint")
				}
				*field.destination = &parsed
			}
		}
	case externaltrigger.VariableEnum:
		config.Options = strings.FieldsFunc(request.FormValue("variable_options"), func(value rune) bool { return value == '\n' || value == '\r' })
		for index := range config.Options {
			config.Options[index] = strings.TrimSpace(config.Options[index])
		}
	case externaltrigger.VariableText:
		maximum, err := strconv.Atoi(request.FormValue("variable_max_length"))
		if err != nil {
			return config, errors.New("invalid text length")
		}
		config.MaxLength, config.Pattern = maximum, request.FormValue("variable_pattern")
	default:
		return config, errors.New("invalid variable type")
	}
	return config, nil
}

func renderExternalInterfaceForm(response http.ResponseWriter, data externalInterfaceFormData) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = externalInterfaceFormTemplate.Execute(response, data)
}

func (a *App) externalTrigger(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	token := ""
	if authorization := strings.TrimSpace(request.Header.Get("Authorization")); strings.HasPrefix(authorization, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	}
	key, entry, err := a.externalTriggers.Resolve(request.Context(), token, request.URL.Query().Get("name"))
	if err != nil {
		status, code := http.StatusUnauthorized, "invalid_key"
		if errors.Is(err, externaltrigger.ErrEntryNotFound) || errors.Is(err, externaltrigger.ErrEntryDisabled) {
			status, code = http.StatusNotFound, "entry_not_found"
		}
		writeExternalTriggerError(response, status, code)
		return
	}
	requestID, err := randomToken(18)
	if err != nil {
		writeExternalTriggerError(response, http.StatusInternalServerError, "action_failed")
		return
	}
	release, allowed := a.externalLimit.Acquire(key.ID)
	if !allowed {
		response.Header().Set("Retry-After", "60")
		_ = a.externalTriggers.RecordInvocation(request.Context(), externaltrigger.Invocation{ID: requestID, KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name, ActionType: entry.Type, Result: "rejected", HTTPStatus: http.StatusTooManyRequests, Source: request.RemoteAddr})
		a.recordAuditWithActor("external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name+" request="+requestID, "rejected", request.RemoteAddr, "", key.Label, userRole("external"))
		writeExternalTriggerError(response, http.StatusTooManyRequests, "rate_limited")
		return
	}
	defer release()
	started := time.Now()
	invocation := externaltrigger.Invocation{
		ID: requestID, OccurredAt: started.UTC(), KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name,
		ActionType: entry.Type, Result: "processing", Source: request.RemoteAddr,
	}
	execution := runRecordedExternalAction(
		func() error { return a.externalTriggers.RecordInvocation(request.Context(), invocation) },
		func() externalActionResult { return a.executeExternalAction(response, request, entry) },
		func(result externalActionResult) error {
			invocation.Result, invocation.HTTPStatus, invocation.Duration = result.result, result.status, time.Since(started)
			invocation.BytesReceived, invocation.RunID, invocation.Message = result.bytesReceived, result.runID, result.message
			return a.externalTriggers.CompleteInvocation(request.Context(), invocation)
		},
	)
	if !execution.Started {
		writeExternalTriggerError(response, http.StatusInternalServerError, "action_failed")
		return
	}
	result := execution.Result
	a.recordAuditWithActor("external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name+" request="+requestID, result.result, request.RemoteAddr, "", key.Label, userRole("external"))
	if result.status >= 400 {
		writeExternalTriggerError(response, result.status, result.code)
		return
	}
	payload := map[string]any{"ok": true, "request_id": requestID, "action": string(entry.Type)}
	if result.runID != "" {
		payload["run_id"] = result.runID
	}
	if result.filename != "" {
		payload["filename"] = result.filename
		payload["bytes_received"] = result.bytesReceived
	}
	response.WriteHeader(result.status)
	_ = json.NewEncoder(response).Encode(payload)
}

type externalActionResult struct {
	status        int
	result, code  string
	message       string
	runID         string
	filename      string
	bytesReceived int64
}

type recordedExternalActionExecution struct {
	Result      externalActionResult
	Started     bool
	RecordError error
}

func runRecordedExternalAction(begin func() error, execute func() externalActionResult, complete func(externalActionResult) error) recordedExternalActionExecution {
	if err := begin(); err != nil {
		return recordedExternalActionExecution{RecordError: err}
	}
	result := execute()
	return recordedExternalActionExecution{Result: result, Started: true, RecordError: complete(result)}
}

func (a *App) executeExternalAction(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry) externalActionResult {
	switch entry.Type {
	case externaltrigger.ActionLog:
		return a.executeExternalLog(response, request, entry)
	case externaltrigger.ActionUpload:
		return a.executeExternalUpload(response, request, entry)
	case externaltrigger.ActionQuickRun:
		return a.executeExternalQuickRun(response, request, entry)
	case externaltrigger.ActionVariable:
		return a.executeExternalVariable(response, request, entry)
	default:
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
}

func (a *App) executeExternalLog(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry) externalActionResult {
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		return externalFailure(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	var config externaltrigger.LogConfig
	if entry.DecodeConfig(&config) != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	message := request.URL.Query().Get("message")
	if request.Method == http.MethodPost {
		request.Body = http.MaxBytesReader(response, request.Body, 8<<10)
		if err := request.ParseForm(); err != nil {
			return externalFailure(http.StatusBadRequest, "invalid_request")
		}
		message = request.FormValue("message")
	}
	if !utf8.ValidString(message) || len([]byte(message)) > config.MaxMessageBytes || strings.IndexFunc(message, unicode.IsControl) >= 0 {
		return externalFailure(http.StatusBadRequest, "invalid_request")
	}
	record := fmt.Sprintf("%s\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), message)
	if config.Category != "" {
		record = fmt.Sprintf("%s\t[%s]\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), config.Category, message)
	}
	if err := a.files.AppendText(config.File, record); err != nil {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	return externalActionResult{status: http.StatusOK, result: "succeeded", message: message}
}

func (a *App) executeExternalUpload(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry) externalActionResult {
	if request.Method != http.MethodPost {
		return externalFailure(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	var config externaltrigger.UploadConfig
	if entry.DecodeConfig(&config) != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	request.Body = http.MaxBytesReader(response, request.Body, config.MaxBytes+(1<<20))
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		if request.MultipartForm != nil {
			_ = request.MultipartForm.RemoveAll()
		}
		var maximumError *http.MaxBytesError
		if errors.As(err, &maximumError) {
			return externalFailure(http.StatusRequestEntityTooLarge, "payload_too_large")
		}
		return externalFailure(http.StatusBadRequest, "invalid_request")
	}
	defer request.MultipartForm.RemoveAll()
	files := request.MultipartForm.File["file"]
	if len(files) != 1 || len(request.MultipartForm.File) != 1 {
		return externalFailure(http.StatusBadRequest, "invalid_request")
	}
	header := files[0]
	if header.Size > config.MaxBytes {
		return externalFailure(http.StatusRequestEntityTooLarge, "payload_too_large")
	}
	if !externalExtensionAllowed(header.Filename, config.Extensions) {
		return externalFailure(http.StatusBadRequest, "file_not_allowed")
	}
	_, disposition, dispositionErr := mime.ParseMediaType(header.Header.Get("Content-Disposition"))
	rawName := disposition["filename"]
	if dispositionErr != nil || rawName == "" || strings.ContainsAny(rawName, `/\`) {
		return externalFailure(http.StatusBadRequest, "file_not_allowed")
	}
	file, err := header.Open()
	if err != nil {
		return externalFailure(http.StatusBadRequest, "invalid_request")
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if name != header.Filename {
		return externalFailure(http.StatusBadRequest, "file_not_allowed")
	}
	if config.ConflictPolicy == "rename" {
		name, err = a.files.AvailableName(config.Directory, name)
	}
	if err == nil {
		_, err = a.files.Upload(config.Directory, name, io.LimitReader(file, config.MaxBytes+1), config.MaxBytes, false, "")
	}
	if err != nil {
		return externalFailure(http.StatusConflict, "upload_failed")
	}
	return externalActionResult{status: http.StatusCreated, result: "succeeded", message: name, filename: name, bytesReceived: header.Size}
}

func externalExtensionAllowed(name string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	name = strings.ToLower(name)
	for _, extension := range allowed {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

func (a *App) executeExternalQuickRun(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry) externalActionResult {
	if request.Method != http.MethodPost {
		return externalFailure(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1)
	if body, err := io.ReadAll(request.Body); err != nil || len(body) != 0 {
		return externalFailure(http.StatusBadRequest, "invalid_request")
	}
	var config externaltrigger.QuickRunConfig
	if entry.DecodeConfig(&config) != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	quick, err := a.loadQuickRun(config.QuickRunID)
	if err != nil {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	variables, err := a.loadVariables()
	if err != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	runID, err := a.runs.Start(runmanager.StartRequest{ScriptPath: quick.ScriptPath, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds, SourceType: "external/quick-run", SourceName: entry.Label, SourceID: entry.ID, Variables: variables})
	if err != nil {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	return externalActionResult{status: http.StatusAccepted, result: "accepted", runID: runID}
}

func (a *App) executeExternalVariable(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry) externalActionResult {
	if request.Method != http.MethodPost {
		return externalFailure(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	request.Body = http.MaxBytesReader(response, request.Body, 8<<10)
	var config externaltrigger.VariableConfig
	if entry.DecodeConfig(&config) != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	var raw any
	if strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		var payload struct {
			Value any `json:"value"`
		}
		if err := decoder.Decode(&payload); err != nil {
			return externalFailure(http.StatusBadRequest, "invalid_request")
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return externalFailure(http.StatusBadRequest, "invalid_request")
		}
		raw = payload.Value
	} else {
		if err := request.ParseForm(); err != nil {
			return externalFailure(http.StatusBadRequest, "invalid_request")
		}
		raw = request.FormValue("value")
	}
	value, err := externaltrigger.ValidateVariableValue(config, raw)
	if err != nil {
		return externalFailure(http.StatusBadRequest, "invalid_value")
	}
	result, err := a.db.ExecContext(request.Context(), "UPDATE variables SET value = ?, updated_at = ? WHERE name = ? AND is_password = 0", value, time.Now().UTC().Unix(), config.VariableName)
	if err != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	return externalActionResult{status: http.StatusOK, result: "succeeded"}
}

func externalFailure(status int, code string) externalActionResult {
	return externalActionResult{status: status, result: "rejected", code: code}
}

func writeExternalTriggerError(response http.ResponseWriter, status int, code string) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{"ok": false, "error": code})
}
