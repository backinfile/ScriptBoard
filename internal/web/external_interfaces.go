package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/identity"
	"scriptboard/internal/privatepath"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/uploadinbox"
	"scriptboard/internal/websitemonitor"
)

type externalKeyView struct {
	externaltrigger.Key
	Status, StatusText string
	EnabledEntries     int
}

type externalEntryView struct {
	externaltrigger.Entry
	TypeText, TargetText, PreviewURL, CallPath string
}

type externalGroupView struct {
	externaltrigger.Group
	Keys    []externalKeyView
	Entries []externalEntryView
}

type externalInvocationView struct {
	externaltrigger.Invocation
	ActionText, ResultText string
}

type externalLogFileView struct {
	EntryID, EntryName, EntryLabel, GroupLabel        string
	Path, FileName, PreviewURL, DownloadURL, CallPath string
	Size                                              int64
	ModifiedAt                                        time.Time
	Exists, Managed, Rotate, Archive                  bool
	ArchiveNumber                                     int
}

type externalLogGroupView struct {
	externaltrigger.Group
	Files []externalLogFileView
}

type externalInterfacesPageData struct {
	ActiveTab     string
	Groups        []externalGroupView
	Keys          []externalKeyView
	Entries       map[string][]externalEntryView
	Requests      []externalInvocationView
	LogFiles      []externalLogFileView
	LogGroups     []externalLogGroupView
	LogFileCount  int
	LogQuery      string
	Filters       auditFilters
	Pagination    paginationView
	CSRFToken     string
	Locale        webLocale
	Now           time.Time
	GlobalEnabled bool
}

type externalInterfaceFormData struct {
	Kind, Title, Description, BackURL, Action, CSRFToken string
	CallMethod, CallURL, CallBody, TypeText, TargetText  string
	PreviewURL, FormError                                string
	Locale                                               webLocale
	Key                                                  externaltrigger.Key
	Group                                                externaltrigger.Group
	Secret                                               string
	QuickRuns                                            []externalTargetOption
	Variables                                            []externalTargetOption
	Entry                                                externaltrigger.Entry
	LogConfig                                            externaltrigger.LogConfig
	UploadConfig                                         externaltrigger.UploadConfig
	QuickRunConfig                                       externaltrigger.QuickRunConfig
	VariableConfig                                       externaltrigger.VariableConfig
	EntryEnabled                                         bool
	RequireSignature                                     bool
	Submitted                                            bool
	LogMessageLimitInput, UploadMaxBytesInput            string
	LogTargetModeInput, LogMaxFileMBInput                string
	LogMaxBackupsInput                                   string
	LogRotateInput                                       bool
	UploadExtensionsInput, VariableMinimumInput          string
	VariableMaximumInput, VariableMaxLengthInput         string
	VariableOptionsInput                                 string
	KeyLabelInput, KeyDurationInput                      string
	KeyFormSubmitted, KeyEnabledInput                    bool
	GroupLabelInput, GroupCallNameInput                  string
	GroupFormSubmitted                                   bool
}

type externalTargetOption struct{ Value, Label string }

func (a *App) externalInterfacesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	now := time.Now().UTC()
	activeTab := "interfaces"
	switch request.URL.Query().Get("tab") {
	case "activity":
		activeTab = "activity"
	case "logs":
		activeTab = "logs"
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
	groups, err := a.externalTriggers.ListGroups(request.Context())
	if err != nil {
		http.Error(response, "Unable to read External Interfaces", http.StatusInternalServerError)
		return
	}
	globalEnabled, _, err := a.externalTriggers.GlobalEnabled(request.Context())
	if err != nil {
		http.Error(response, "Unable to read External Interface control", http.StatusInternalServerError)
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
	groupViews := make([]externalGroupView, 0, len(groups))
	logQuery := strings.TrimSpace(request.URL.Query().Get("q"))
	logFiles := make([]externalLogFileView, 0)
	logGroups := make([]externalLogGroupView, 0)
	for _, group := range groups {
		view := externalGroupView{Group: group}
		groupLogFiles := make([]externalLogFileView, 0)
		for _, key := range group.Keys {
			keyView := externalKeyView{Key: key, Status: "disabled", StatusText: webText(locale, "external.disabled")}
			if key.Expired(now) {
				keyView.Status, keyView.StatusText = "expired", webText(locale, "external.expired")
			} else if key.Enabled {
				keyView.Status, keyView.StatusText = "enabled", webText(locale, "external.enabled")
			}
			view.Keys = append(view.Keys, keyView)
		}
		for _, entry := range group.Entries {
			entryView := externalEntryView{
				Entry: entry, TypeText: externalActionText(locale, entry.Type), TargetText: externalTargetText(locale, entry), CallPath: externalCallPath(group.CallName, entry.Name),
			}
			if entry.Type == externaltrigger.ActionLog {
				var config externaltrigger.LogConfig
				if entry.DecodeConfig(&config) == nil && config.File != "" {
					entryView.PreviewURL = routeFileURL("/resources/files/view", config.File)
					if activeTab == "logs" {
						entryLogFiles := a.externalLogFiles(request.Context(), group, entry, config, logQuery)
						logFiles = append(logFiles, entryLogFiles...)
						groupLogFiles = append(groupLogFiles, entryLogFiles...)
					}
				}
			}
			view.Entries = append(view.Entries, entryView)
		}
		groupViews = append(groupViews, view)
		if len(groupLogFiles) > 0 {
			logGroups = append(logGroups, externalLogGroupView{Group: group, Files: groupLogFiles})
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := externalInterfacesTemplate.Execute(response, externalInterfacesPageData{
		ActiveTab: activeTab, Groups: groupViews, Requests: requests, LogFiles: logFiles, LogGroups: logGroups, LogFileCount: len(logFiles), LogQuery: logQuery, Filters: filters, Pagination: pagination,
		CSRFToken: current.csrfToken, Locale: locale, Now: now, GlobalEnabled: globalEnabled,
	}); err != nil {
		http.Error(response, "Unable to render External Interfaces", http.StatusInternalServerError)
	}
}

func (a *App) externalLogFiles(ctx context.Context, group externaltrigger.Group, entry externaltrigger.Entry, config externaltrigger.LogConfig, query string) []externalLogFileView {
	configured := externalLogFileView{
		EntryID: entry.ID, EntryName: entry.Name, EntryLabel: entry.Label, GroupLabel: group.Label,
		Path: config.File, FileName: filepath.Base(config.File), CallPath: externalCallPath(group.CallName, entry.Name), Managed: config.Managed, Rotate: config.Rotate,
	}
	files := make([]externalLogFileView, 0, 1+config.MaxBackups)
	if info, _, err := a.hostInfo(ctx, config.File); err == nil && info.Mode().IsRegular() {
		configured.Exists, configured.Size, configured.ModifiedAt = true, info.Size(), info.ModTime()
		configured.PreviewURL = routeFileURL("/resources/files/view", config.File)
		configured.DownloadURL = routeFileURL("/resources/files/download", config.File)
	}
	if externalLogMatches(configured, query) {
		files = append(files, configured)
	}
	if !config.Rotate {
		return files
	}
	for archiveNumber := 1; archiveNumber <= config.MaxBackups; archiveNumber++ {
		archivePath := config.File + "." + strconv.Itoa(archiveNumber)
		info, _, err := a.hostInfo(ctx, archivePath)
		if err != nil || !info.Mode().IsRegular() {
			break
		}
		archive := configured
		archive.Path, archive.FileName = archivePath, filepath.Base(archivePath)
		archive.Exists, archive.Archive, archive.ArchiveNumber = true, true, archiveNumber
		archive.Size, archive.ModifiedAt = info.Size(), info.ModTime()
		archive.PreviewURL = routeFileURL("/resources/files/view", archivePath)
		archive.DownloadURL = routeFileURL("/resources/files/download", archivePath)
		if externalLogMatches(archive, query) {
			files = append(files, archive)
		}
	}
	return files
}

func externalLogMatches(file externalLogFileView, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(file.GroupLabel), query) ||
		strings.Contains(strings.ToLower(file.EntryLabel), query) ||
		strings.Contains(strings.ToLower(file.EntryName), query) ||
		strings.Contains(strings.ToLower(file.Path), query)
}

func (a *App) setExternalGlobalControl(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	rawEnabled := request.FormValue("enabled")
	if (rawEnabled != "0" && rawEnabled != "1") || request.FormValue("confirmed") != "yes" {
		http.Error(response, "Invalid External Interface control", http.StatusBadRequest)
		return
	}
	enabled := rawEnabled == "1"
	if err := a.externalTriggers.SetGlobalEnabled(request.Context(), enabled); err != nil {
		http.Error(response, "Unable to update External Interface control", http.StatusInternalServerError)
		return
	}
	target := "disabled"
	if enabled {
		target = "enabled"
	}
	a.recordAuditForRequest(request, "set_external_interface_global_control", target, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) externalGlobalControlTask(response http.ResponseWriter, request *http.Request) {
	rawEnabled := request.URL.Query().Get("enabled")
	if rawEnabled != "0" && rawEnabled != "1" {
		http.Error(response, "Invalid External Interface control", http.StatusBadRequest)
		return
	}
	enabled := rawEnabled == "1"
	locale := resolveWebLocale(request)
	titleKey := "external.pause_confirm_title"
	descriptionKey := "external.pause_confirm_description"
	if enabled {
		titleKey = "external.resume_confirm_title"
		descriptionKey = "external.resume_confirm_description"
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "external-global-control", Title: webText(locale, titleKey),
		Description: webText(locale, descriptionKey), BackURL: "/config/external-interfaces",
		Action: "/config/external-interfaces/control", Enabled: enabled,
	})
}

func externalActionText(locale webLocale, action externaltrigger.ActionType) string {
	return webText(locale, "external.action."+string(action))
}

func externalCallPath(groupCallName, entryName string) string {
	return "/trigger/" + url.PathEscape(groupCallName) + "/" + url.PathEscape(entryName)
}

func externalTargetText(locale webLocale, entry externaltrigger.Entry) string {
	switch entry.Type {
	case externaltrigger.ActionLog:
		var config externaltrigger.LogConfig
		if entry.DecodeConfig(&config) == nil {
			return config.File
		}
	case externaltrigger.ActionWebsiteMonitor:
		return webText(locale, "external.website_monitor_target")
	}
	return entry.Target
}

func (a *App) newExternalGroupTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "group-new", Title: webText(locale, "external.create_group"), Description: webText(locale, "external.create_group_description"), BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/groups", CSRFToken: current.csrfToken, Locale: locale})
}

func (a *App) createExternalGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	group, err := a.externalTriggers.CreateGroup(request.Context(), request.FormValue("label"), request.FormValue("call_name"))
	if err != nil {
		a.renderExternalGroupSubmissionError(response, request, "group-new", externaltrigger.Group{}, webText(resolveWebLocale(request), "external.group_save_error"))
		return
	}
	a.recordAuditForRequest(request, "create_external_interface_group", group.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) editExternalGroupTask(response http.ResponseWriter, request *http.Request) {
	group, err := a.externalTriggers.Group(request.Context(), request.PathValue("groupID"))
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "group-edit", Title: webText(locale, "external.edit_group"), Description: webText(locale, "external.edit_group_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/groups/" + group.ID,
		CSRFToken: current.csrfToken, Locale: locale, Group: group,
	})
}

func (a *App) updateExternalGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	group, err := a.externalTriggers.UpdateGroup(request.Context(), request.PathValue("groupID"), request.FormValue("label"), request.FormValue("call_name"))
	if err != nil {
		a.renderExternalGroupSubmissionError(response, request, "group-edit", externaltrigger.Group{ID: request.PathValue("groupID")}, webText(resolveWebLocale(request), "external.group_save_error"))
		return
	}
	a.recordAuditForRequest(request, "update_external_interface_group", group.ID, "succeeded")
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) renderExternalGroupSubmissionError(response http.ResponseWriter, request *http.Request, kind string, group externaltrigger.Group, message string) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	title, description, action := webText(locale, "external.create_group"), webText(locale, "external.create_group_description"), "/config/external-interfaces/groups"
	if kind == "group-edit" {
		title, description, action = webText(locale, "external.edit_group"), webText(locale, "external.edit_group_description"), "/config/external-interfaces/groups/"+group.ID
	}
	response.WriteHeader(http.StatusUnprocessableEntity)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: kind, Title: title, Description: description, BackURL: "/config/external-interfaces", Action: action,
		CSRFToken: current.csrfToken, Locale: locale, Group: group, FormError: message,
		GroupFormSubmitted: true, GroupLabelInput: request.FormValue("label"), GroupCallNameInput: request.FormValue("call_name"),
	})
}

func (a *App) newExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	var group externaltrigger.Group
	var err error
	if groupID := request.PathValue("groupID"); groupID != "" {
		group, err = a.externalTriggers.Group(request.Context(), groupID)
	}
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	action := "/config/external-interfaces/keys"
	if group.ID != "" {
		action = "/config/external-interfaces/groups/" + group.ID + "/keys"
	}
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "key-new", Title: webText(locale, "external.create_key_title"), Description: webText(locale, "external.create_key_description"),
		BackURL: "/config/external-interfaces", Action: action, CSRFToken: current.csrfToken, Locale: locale, Group: group,
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
	groupID := request.PathValue("groupID")
	if groupID == "" {
		groupID, err = a.ensureLegacyExternalGroup(request.Context())
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	key, secret, err := a.externalTriggers.CreateKey(request.Context(), externaltrigger.CreateKeyInput{
		GroupID: groupID, Label: request.FormValue("label"), Enabled: request.FormValue("enabled") == "1", ExpiresAt: expiresAt,
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
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusCreated)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "key-created", Title: webText(locale, "external.key_created"), Description: webText(locale, "external.key_created_description"),
		BackURL: "/config/external-interfaces", Locale: locale, Group: externaltrigger.Group{ID: key.GroupID}, Key: key, Secret: secret,
	})
}

func (a *App) ensureLegacyExternalGroup(ctx context.Context) (string, error) {
	var id string
	err := a.db.QueryRowContext(ctx, `SELECT id FROM external_trigger_groups WHERE label = 'Legacy interfaces' COLLATE NOCASE`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	group, err := a.externalTriggers.CreateGroup(ctx, "Legacy interfaces", "legacy")
	if errors.Is(err, externaltrigger.ErrGroupNameExists) {
		err = a.db.QueryRowContext(ctx, `SELECT id FROM external_trigger_groups WHERE label = 'Legacy interfaces' COLLATE NOCASE`).Scan(&id)
		return id, err
	}
	return group.ID, err
}

func (a *App) editExternalKeyTask(response http.ResponseWriter, request *http.Request) {
	key, err := a.externalTriggers.Key(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "key-edit", Title: webText(locale, "external.edit_key"), Description: webText(locale, "external.edit_key_description"), BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/keys/" + key.ID, CSRFToken: current.csrfToken, Locale: locale, Group: externaltrigger.Group{ID: key.GroupID}, Key: key})
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
		CSRFToken: current.csrfToken, Locale: locale, Group: externaltrigger.Group{ID: key.GroupID}, Key: key,
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
	var group externaltrigger.Group
	if kind == "key-new" {
		if groupID := request.PathValue("groupID"); groupID != "" {
			if loaded, err := a.externalTriggers.Group(request.Context(), groupID); err == nil {
				group = loaded
				action = "/config/external-interfaces/groups/" + group.ID + "/keys"
			}
		}
	}
	if kind == "key-edit" {
		title, description, action = webText(locale, "external.edit_key"), webText(locale, "external.edit_key_description"), "/config/external-interfaces/keys/"+key.ID
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusUnprocessableEntity)
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: kind, Title: title, Description: description, BackURL: "/config/external-interfaces", Action: action,
		CSRFToken: current.csrfToken, Locale: locale, Group: group, Key: key, FormError: message,
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
	key, secret, err := a.externalTriggers.RotateKey(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface key not found", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "rotate_external_interface_key", key.ID, "succeeded")
	locale := resolveWebLocale(request)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusCreated)
	renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "key-rotated", Title: webText(locale, "external.key_rotated"), Description: webText(locale, "external.key_rotated_description"), BackURL: "/config/external-interfaces", Locale: locale, Group: externaltrigger.Group{ID: key.GroupID}, Key: key, Secret: secret})
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
	groupID := request.PathValue("groupID")
	var legacyKey externaltrigger.Key
	var err error
	if groupID == "" {
		legacyKey, err = a.externalTriggers.Key(request.Context(), request.PathValue("id"))
		groupID = legacyKey.GroupID
	}
	group, groupErr := a.externalTriggers.Group(request.Context(), groupID)
	if err == nil {
		err = groupErr
	}
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	quickRuns, variables, err := a.externalTargetOptions(request.Context())
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "entry-new", Title: webText(locale, "external.add_function_title"), Description: webText(locale, "external.add_function_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/groups/" + group.ID + "/entries",
		CSRFToken: current.csrfToken, Locale: locale, Group: group, QuickRuns: quickRuns, Variables: variables, LogConfig: externaltrigger.LogConfig{Managed: true}, EntryEnabled: true, RequireSignature: true,
	})
}

func (a *App) externalEntryDetail(response http.ResponseWriter, request *http.Request) {
	entry, err := a.externalTriggers.Entry(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	group, err := a.externalTriggers.Group(request.Context(), entry.GroupID)
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	var key externaltrigger.Key
	if len(group.Keys) > 0 {
		key = group.Keys[0]
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
	case externaltrigger.ActionWebsiteMonitor:
		callBodyKey = "external.call_body.website_monitor"
	}
	callMethod := http.MethodPost
	if entry.Type == externaltrigger.ActionWebsiteMonitor {
		callMethod = http.MethodGet
	}
	renderExternalInterfaceForm(response, externalInterfaceFormData{
		Kind: "entry-detail", Title: entry.Label, Description: webText(locale, "external.function_details_description"),
		BackURL: "/config/external-interfaces", CSRFToken: current.csrfToken, Locale: locale, Group: group, Key: key, Entry: entry, EntryEnabled: entry.Enabled, RequireSignature: entry.RequireSignature,
		CallMethod: callMethod, CallURL: externalCallPath(group.CallName, entry.Name), CallBody: webText(locale, callBodyKey),
		TypeText: externalActionText(locale, entry.Type), TargetText: externalTargetText(locale, entry), PreviewURL: previewURL,
	})
}

func (a *App) editExternalEntryTask(response http.ResponseWriter, request *http.Request) {
	entry, err := a.externalTriggers.Entry(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "External Interface entry not found", http.StatusNotFound)
		return
	}
	group, err := a.externalTriggers.Group(request.Context(), entry.GroupID)
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	quickRuns, variables, err := a.externalTargetOptions(request.Context())
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	data := externalInterfaceFormData{Kind: "entry-edit", BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/entries/" + entry.ID, Group: group, Entry: entry, EntryEnabled: entry.Enabled, RequireSignature: entry.RequireSignature, QuickRuns: quickRuns, Variables: variables}
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
	case externaltrigger.ActionWebsiteMonitor:
	}
	renderExternalInterfaceForm(response, data)
}

func (a *App) externalTargetOptions(ctx context.Context) ([]externalTargetOption, []externalTargetOption, error) {
	quickRows, err := a.db.Query("SELECT id, name, script_path, script_sha256 FROM quick_runs WHERE locked = 1 AND script_sha256 != '' ORDER BY name, id")
	if err != nil {
		return nil, nil, err
	}
	var quickRuns []externalTargetOption
	for quickRows.Next() {
		var option externalTargetOption
		var scriptPath, scriptSHA256 string
		if err := quickRows.Scan(&option.Value, &option.Label, &scriptPath, &scriptSHA256); err != nil {
			_ = quickRows.Close()
			return nil, nil, err
		}
		prepared, err := a.hostPrepareScript(ctx, scriptPath)
		if err != nil || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(scriptSHA256)) != 1 {
			continue
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
	groupID := request.PathValue("groupID")
	legacyKeyID := ""
	if groupID == "" {
		legacyKeyID = request.PathValue("id")
		legacyKey, keyErr := a.externalTriggers.Key(request.Context(), legacyKeyID)
		if keyErr != nil {
			http.Error(response, "External Interface key not found", http.StatusNotFound)
			return
		}
		groupID = legacyKey.GroupID
	}
	group, err := a.externalTriggers.Group(request.Context(), groupID)
	if err != nil {
		http.Error(response, "External Interface group not found", http.StatusNotFound)
		return
	}
	actionType := externaltrigger.ActionType(request.FormValue("action_type"))
	config, err := a.externalEntryConfig(request, actionType)
	if err != nil {
		a.renderExternalEntrySubmissionError(response, request, group)
		return
	}
	entry, secret, err := a.externalTriggers.CreateEntry(request.Context(), externaltrigger.CreateEntryInput{
		GroupID: groupID, KeyID: legacyKeyID, Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: actionType,
		Enabled: request.FormValue("enabled") == "1", RequireSignature: request.FormValue("require_signature") == "1", Config: config,
	})
	if err != nil {
		a.renderExternalEntrySubmissionError(response, request, group)
		return
	}
	a.recordAuditForRequest(request, "create_external_interface_entry", entry.ID, "succeeded")
	if legacyKeyID != "" {
		key, _ := a.externalTriggers.Key(request.Context(), legacyKeyID)
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusCreated)
		renderExternalInterfaceForm(response, externalInterfaceFormData{Kind: "key-rotated", Title: webText(resolveWebLocale(request), "external.key_rotated"), Description: webText(resolveWebLocale(request), "external.key_rotated_description"), BackURL: "/config/external-interfaces", Locale: resolveWebLocale(request), Key: key, Secret: secret})
		return
	}
	http.Redirect(response, request, "/config/external-interfaces", http.StatusSeeOther)
}

func (a *App) renderExternalEntrySubmissionError(response http.ResponseWriter, request *http.Request, group externaltrigger.Group) {
	quickRuns, variables, err := a.externalTargetOptions(request.Context())
	if err != nil {
		http.Error(response, "Unable to read action targets", http.StatusInternalServerError)
		return
	}
	locale := resolveWebLocale(request)
	data := externalInterfaceFormData{
		Kind: "entry-new", Title: webText(locale, "external.add_function_title"), Description: webText(locale, "external.add_function_description"),
		BackURL: "/config/external-interfaces", Action: "/config/external-interfaces/groups/" + group.ID + "/entries",
		CSRFToken: request.Context().Value(sessionContextKey).(session).csrfToken, Locale: locale, Group: group,
		QuickRuns: quickRuns, Variables: variables, EntryEnabled: request.FormValue("enabled") == "1", RequireSignature: request.FormValue("require_signature") == "1", Submitted: true,
		FormError:            webText(locale, "external.entry_save_error"),
		Entry:                externaltrigger.Entry{Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: externaltrigger.ActionType(request.FormValue("action_type"))},
		LogConfig:            externaltrigger.LogConfig{File: request.FormValue("log_file"), Managed: request.FormValue("log_target_mode") == "managed", Category: request.FormValue("log_category")},
		UploadConfig:         externaltrigger.UploadConfig{Directory: request.FormValue("upload_directory"), ConflictPolicy: request.FormValue("upload_conflict")},
		QuickRunConfig:       externaltrigger.QuickRunConfig{QuickRunID: request.FormValue("quick_run_id")},
		VariableConfig:       externaltrigger.VariableConfig{VariableName: request.FormValue("variable_name"), Type: externaltrigger.VariableType(request.FormValue("variable_type")), Pattern: request.FormValue("variable_pattern"), AllowEmpty: request.FormValue("variable_allow_empty") == "1"},
		LogMessageLimitInput: request.FormValue("log_message_limit"), LogTargetModeInput: request.FormValue("log_target_mode"),
		LogMaxFileMBInput: request.FormValue("log_max_file_mb"), LogMaxBackupsInput: request.FormValue("log_max_backups"), LogRotateInput: request.FormValue("log_rotate") == "1",
		UploadMaxBytesInput:   request.FormValue("upload_max_bytes"),
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
	config, err := a.externalEntryConfig(request, actionType)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := a.externalTriggers.UpdateEntry(request.Context(), externaltrigger.UpdateEntryInput{ID: id, Name: strings.TrimSpace(request.FormValue("name")), Label: request.FormValue("label"), Type: actionType, Enabled: request.FormValue("enabled") == "1", RequireSignature: request.FormValue("require_signature") == "1", Config: config})
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

func (a *App) externalEntryConfig(request *http.Request, actionType externaltrigger.ActionType) (any, error) {
	switch actionType {
	case externaltrigger.ActionLog:
		limit, err := strconv.Atoi(request.FormValue("log_message_limit"))
		if err != nil {
			return nil, errors.New("invalid log message limit")
		}
		managed := request.FormValue("log_target_mode") == "managed"
		rawPath := strings.TrimSpace(request.FormValue("log_file"))
		if managed {
			groupID := request.PathValue("groupID")
			if groupID == "" {
				if existing, loadErr := a.externalTriggers.Entry(request.Context(), request.PathValue("id")); loadErr == nil {
					groupID = existing.GroupID
				}
			}
			if groupID == "" {
				return nil, errors.New("invalid managed log target")
			}
			rawPath = filepath.Join(filepath.Dir(a.stateRoot), "scriptboard-external-"+groupID+"-"+strings.TrimSpace(request.FormValue("name"))+".log")
		}
		path, err := a.hostPrepareAppend(request.Context(), rawPath)
		if err != nil {
			return nil, fmt.Errorf("invalid log file: %w", err)
		}
		config := externaltrigger.LogConfig{File: path, Managed: managed, Category: strings.TrimSpace(request.FormValue("log_category")), MaxMessageBytes: limit}
		if request.FormValue("log_rotate") == "1" {
			maxMB, sizeErr := strconv.ParseInt(request.FormValue("log_max_file_mb"), 10, 64)
			backups, countErr := strconv.Atoi(request.FormValue("log_max_backups"))
			if sizeErr != nil || countErr != nil || maxMB < 1 || maxMB > 1024 || backups < 1 || backups > 100 {
				return nil, errors.New("invalid log rotation policy")
			}
			config.Rotate, config.MaxFileBytes, config.MaxBackups = true, maxMB<<20, backups
		}
		return config, nil
	case externaltrigger.ActionUpload:
		maximum, err := strconv.ParseInt(request.FormValue("upload_max_bytes"), 10, 64)
		if err != nil {
			return nil, errors.New("invalid upload byte limit")
		}
		directory := strings.TrimSpace(request.FormValue("upload_directory"))
		prepared, err := a.hostPrepareDirectory(request.Context(), directory)
		if err != nil {
			return nil, fmt.Errorf("invalid upload directory: %w", err)
		}
		directory = prepared.Path
		extensions := strings.FieldsFunc(request.FormValue("upload_extensions"), func(value rune) bool { return value == ',' || value == '\n' || value == '\r' })
		return externaltrigger.UploadConfig{Directory: directory, MaxBytes: maximum, Extensions: extensions, ConflictPolicy: request.FormValue("upload_conflict")}, nil
	case externaltrigger.ActionQuickRun:
		id := strings.TrimSpace(request.FormValue("quick_run_id"))
		quick, err := a.loadQuickRun(id)
		if err != nil {
			return nil, errors.New("quick run does not exist")
		}
		prepared, err := a.hostPrepareScript(request.Context(), quick.ScriptPath)
		if err != nil || !quick.Locked || quick.ScriptSHA256 == "" || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(quick.ScriptSHA256)) != 1 {
			return nil, errors.New("quick run must be locked and republished with its current script digest")
		}
		return externaltrigger.QuickRunConfig{QuickRunID: id, Revision: quick.Revision, ScriptSHA256: quick.ScriptSHA256}, nil
	case externaltrigger.ActionVariable:
		name := strings.TrimSpace(request.FormValue("variable_name"))
		var isPassword bool
		if err := a.db.QueryRow("SELECT is_password FROM variables WHERE name = ?", name).Scan(&isPassword); err != nil || isPassword {
			return nil, errors.New("variable does not exist or is password-protected")
		}
		config, err := parseExternalVariableConfig(request, name)
		return config, err
	case externaltrigger.ActionWebsiteMonitor:
		return externaltrigger.WebsiteMonitorConfig{}, nil
	default:
		return nil, errors.New("invalid action type")
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
	groupName, name := request.PathValue("group"), request.PathValue("name")
	authRelease, allowed := a.externalAuthLimit.AcquireSource(externalLimitSource(request.RemoteAddr))
	if !allowed {
		response.Header().Set("Retry-After", "60")
		writeExternalTriggerError(response, http.StatusTooManyRequests, "rate_limited")
		return
	}
	defer authRelease()
	key, entry, err := a.externalTriggers.ResolveScoped(request.Context(), token, groupName, name)
	if err != nil {
		result := "invalid_key"
		if errors.Is(err, externaltrigger.ErrEntryNotFound) || errors.Is(err, externaltrigger.ErrEntryDisabled) {
			result = "entry_not_found"
		}
		a.recordAuditWithRequestActor(request, "external_trigger_auth", "entry_sha256="+hashToken(groupName+"\x00"+name), result, request.RemoteAddr, "", "", identity.Role("external"))
		writeExternalTriggerError(response, http.StatusUnauthorized, "invalid_key")
		return
	}
	requestID, _ := request.Context().Value(requestIDContextKey).(string)
	if requestID == "" {
		requestID, err = randomToken(18)
		if err != nil {
			writeExternalTriggerError(response, http.StatusInternalServerError, "action_failed")
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey, requestID))
	}
	release, allowed := a.externalLimit.Acquire(externaltrigger.LimitSubject{KeyID: key.ID, Source: externalLimitSource(request.RemoteAddr), Action: entry.Type})
	if !allowed {
		response.Header().Set("Retry-After", "60")
		_ = a.externalTriggers.RecordInvocation(request.Context(), externaltrigger.Invocation{ID: requestID, KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name, ActionType: entry.Type, Result: "rejected", HTTPStatus: http.StatusTooManyRequests, Source: request.RemoteAddr})
		a.recordAuditWithRequestActor(request, "external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name, "rejected", request.RemoteAddr, "", key.Label, identity.Role("external"))
		writeExternalTriggerError(response, http.StatusTooManyRequests, "rate_limited")
		return
	}
	defer release()
	globalEnabled, _, controlErr := a.externalTriggers.GlobalEnabled(request.Context())
	if controlErr != nil || !globalEnabled {
		response.Header().Set("Retry-After", "60")
		_ = a.externalTriggers.RecordInvocation(request.Context(), externaltrigger.Invocation{
			ID: requestID, KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name,
			ActionType: entry.Type, Result: "rejected", HTTPStatus: http.StatusServiceUnavailable, Source: request.RemoteAddr,
		})
		a.recordAuditWithRequestActor(request, "external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name, "rejected", request.RemoteAddr, "", key.Label, identity.Role("external"))
		writeExternalTriggerError(response, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if entry.RequireSignature {
		bodyLength, bodySHA256, cleanupBody, bodyErr := a.stageExternalSignedBody(request, entry)
		if bodyErr != nil {
			status, code := http.StatusBadRequest, "invalid_request"
			if errors.Is(bodyErr, errExternalSignedBodyTooLarge) {
				status, code = http.StatusRequestEntityTooLarge, "payload_too_large"
			}
			_ = a.externalTriggers.RecordInvocation(request.Context(), externaltrigger.Invocation{
				ID: requestID, KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name,
				ActionType: entry.Type, Result: "rejected", HTTPStatus: status, Source: request.RemoteAddr,
			})
			a.recordAuditWithRequestActor(request, "external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name, "rejected", request.RemoteAddr, "", key.Label, identity.Role("external"))
			writeExternalTriggerError(response, status, code)
			return
		}
		defer cleanupBody()
		timestampRaw := request.Header.Get("X-ScriptBoard-Timestamp")
		timestamp, timestampErr := strconv.ParseInt(timestampRaw, 10, 64)
		if timestampErr != nil || timestampRaw != strconv.FormatInt(timestamp, 10) || a.externalTriggers.VerifyAndConsumeSignature(
			request.Context(), key.ID, token, timestamp, request.Header.Get("X-ScriptBoard-Nonce"), request.Method,
			request.URL.RequestURI(), request.Header.Get("Content-Type"), bodyLength, bodySHA256, request.Header.Get("X-ScriptBoard-Signature"),
		) != nil {
			_ = a.externalTriggers.RecordInvocation(request.Context(), externaltrigger.Invocation{
				ID: requestID, KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name,
				ActionType: entry.Type, Result: "rejected", HTTPStatus: http.StatusUnauthorized, Source: request.RemoteAddr,
			})
			a.recordAuditWithRequestActor(request, "external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name, "rejected", request.RemoteAddr, "", key.Label, identity.Role("external"))
			writeExternalTriggerError(response, http.StatusUnauthorized, "invalid_key")
			return
		}
	}
	started := time.Now()
	invocation := externaltrigger.Invocation{
		ID: requestID, OccurredAt: started.UTC(), KeyID: key.ID, KeyLabel: key.Label, EntryID: entry.ID, EntryName: entry.Name,
		ActionType: entry.Type, Result: "processing", Source: request.RemoteAddr,
	}
	execution := runRecordedExternalAction(
		func() error { return a.externalTriggers.RecordInvocation(request.Context(), invocation) },
		func() externalActionResult { return a.executeExternalAction(response, request, entry, token) },
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
	if execution.RecordError != nil {
		a.recordAuditWithRequestActor(request, "external_trigger_completion_deferred", "request="+requestID+" entry="+entry.Name, "deferred", request.RemoteAddr, "", key.Label, identity.Role("external"))
	}
	a.recordAuditWithRequestActor(request, "external_trigger_"+string(entry.Type), "key="+key.ID+" entry="+entry.Name, result.result, request.RemoteAddr, "", key.Label, identity.Role("external"))
	if result.status >= 400 {
		writeExternalTriggerError(response, result.status, result.code)
		return
	}
	payload := map[string]any{"ok": true, "request_id": requestID, "action": string(entry.Type)}
	if result.payload != nil {
		payload["data"] = result.payload
		payload["schema_version"] = 1
	}
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

var errExternalSignedBodyTooLarge = errors.New("external signed request body is too large")

func (a *App) stageExternalSignedBody(request *http.Request, entry externaltrigger.Entry) (int64, string, func(), error) {
	limit, err := externalSignedBodyLimit(entry)
	if err != nil {
		return 0, "", func() {}, err
	}
	directory := filepath.Join(a.stateRoot, "inbox", "external-bodies")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return 0, "", func() {}, err
	}
	if err := privatepath.ProtectDirectory(directory); err != nil {
		return 0, "", func() {}, err
	}
	file, err := os.CreateTemp(directory, "request-*")
	if err != nil {
		return 0, "", func() {}, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return 0, "", func() {}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(request.Body, limit+1))
	_ = request.Body.Close()
	if copyErr != nil {
		cleanup()
		var maximumError *http.MaxBytesError
		if errors.As(copyErr, &maximumError) {
			return 0, "", func() {}, errExternalSignedBodyTooLarge
		}
		return 0, "", func() {}, copyErr
	}
	if written > limit {
		cleanup()
		return 0, "", func() {}, errExternalSignedBodyTooLarge
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return 0, "", func() {}, err
	}
	request.Body = file
	request.ContentLength = written
	return written, hex.EncodeToString(hash.Sum(nil)), cleanup, nil
}

func externalSignedBodyLimit(entry externaltrigger.Entry) (int64, error) {
	switch entry.Type {
	case externaltrigger.ActionLog, externaltrigger.ActionVariable:
		return 8 << 10, nil
	case externaltrigger.ActionQuickRun, externaltrigger.ActionWebsiteMonitor:
		return 1, nil
	case externaltrigger.ActionUpload:
		var config externaltrigger.UploadConfig
		if err := entry.DecodeConfig(&config); err != nil {
			return 0, err
		}
		return config.MaxBytes + (1 << 20), nil
	default:
		return 0, errors.New("external action has no signed body policy")
	}
}

func externalLimitSource(address string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(strings.TrimSpace(address))
}

type externalActionResult struct {
	status        int
	result, code  string
	message       string
	runID         string
	filename      string
	bytesReceived int64
	payload       any
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

func (a *App) executeExternalAction(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry, token string) externalActionResult {
	switch entry.Type {
	case externaltrigger.ActionLog:
		return a.executeExternalLog(response, request, entry, token)
	case externaltrigger.ActionUpload:
		return a.executeExternalUpload(response, request, entry)
	case externaltrigger.ActionQuickRun:
		return a.executeExternalQuickRun(response, request, entry)
	case externaltrigger.ActionVariable:
		return a.executeExternalVariable(response, request, entry)
	case externaltrigger.ActionWebsiteMonitor:
		return a.executeExternalWebsiteMonitor(request)
	default:
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
}

func (a *App) executeExternalWebsiteMonitor(request *http.Request) externalActionResult {
	if request.Method != http.MethodGet {
		return externalFailure(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	locale := resolveWebLocale(request)
	snapshot := a.newWebsiteMonitorListDataView(request.Context(), monitors, monitors, locale)
	return externalActionResult{status: http.StatusOK, result: "succeeded", payload: snapshot}
}

func (a *App) executeExternalLog(response http.ResponseWriter, request *http.Request, entry externaltrigger.Entry, token string) externalActionResult {
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
	requestID, _ := request.Context().Value(requestIDContextKey).(string)
	var appendErr error
	if a.hostFilesBackend != nil {
		appendErr = a.hostAppendExternalLog(request.Context(), requestID, token, entry.ID, entry.Name, message)
	} else {
		record := fmt.Sprintf("%s\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), message)
		if config.Category != "" {
			record = fmt.Sprintf("%s\t[%s]\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), config.Category, message)
		}
		if config.Rotate {
			appendErr = a.files.AppendRotatingText(config.File, record, config.MaxFileBytes, config.MaxBackups)
		} else {
			appendErr = a.files.AppendText(config.File, record)
		}
	}
	if appendErr != nil {
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
	pending, err := a.uploadInbox.Receive(uploadinbox.Input{
		EntryID: entry.ID, OriginalName: name, TargetDirectory: config.Directory, ConflictPolicy: config.ConflictPolicy,
	}, io.LimitReader(file, config.MaxBytes+1), config.MaxBytes)
	if err != nil {
		return externalFailure(http.StatusConflict, "upload_failed")
	}
	return externalActionResult{
		status: http.StatusAccepted, result: "accepted", message: name, filename: name, bytesReceived: header.Size,
		payload: map[string]any{"inbox_id": pending.ID, "sha256": pending.SHA256, "state": "pending_review"},
	}
}

func externalExtensionAllowed(name string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	name = strings.ToLower(filepath.Base(name))
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if filepath.Ext(stem) != "" {
		return false
	}
	switch extension {
	case ".bat", ".cmd", ".com", ".desktop", ".dll", ".exe", ".html", ".htm", ".js", ".lnk", ".msi", ".ps1", ".service", ".sh", ".svg", ".url", ".vbs", ".wsf":
		return false
	}
	for _, extension := range allowed {
		if strings.EqualFold(filepath.Ext(name), extension) {
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
	if !quick.Locked || quick.Revision != config.Revision || subtle.ConstantTimeCompare([]byte(quick.ScriptSHA256), []byte(config.ScriptSHA256)) != 1 {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	prepared, err := a.hostPrepareScript(request.Context(), quick.ScriptPath)
	if err != nil || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(config.ScriptSHA256)) != 1 {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	workingDirectory, err := a.hostPrepareDirectory(request.Context(), prepared.Directory)
	if err != nil {
		return externalFailure(http.StatusConflict, "target_unavailable")
	}
	variables, err := a.loadVariables()
	if err != nil {
		return externalFailure(http.StatusInternalServerError, "action_failed")
	}
	runID, err := a.runs.Start(runmanager.StartRequest{ScriptPath: quick.ScriptPath, ExpectedDigest: config.ScriptSHA256, DisallowOverlap: true, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds, SourceType: "external/quick-run", SourceName: entry.Label, SourceID: entry.ID, Variables: variables, PreparedScript: &prepared, PreparedDirectory: &workingDirectory})
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
