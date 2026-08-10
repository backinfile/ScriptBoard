package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	updatepkg "scriptboard/internal/update"
)

type updatesPageData struct {
	Snapshot           updatepkg.Snapshot
	CSRFToken          string
	Locale             webLocale
	ActiveRuns         int
	LatestAsset        string
	LatestSize         string
	LatestPublished    time.Time
	BuiltAt            time.Time
	PreparedID         string
	Prepared           bool
	Capability         string
	SourceName         string
	Sources            []updateSourceData
	ShowSourceDrawer   bool
	CanRestartService  bool
	RestartRequested   bool
	SettingsNavigation settingsNavigationData
}

type updateSourceData struct {
	ID          string
	Name        string
	Description string
	Host        string
	Badge       string
	Icon        string
	Selected    bool
}

func (a *App) updatesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	snapshot := a.updates.Snapshot()
	locale := resolveWebLocale(request)
	data := updatesPageData{
		Snapshot: snapshot, CSRFToken: current.csrfToken,
		Locale: locale, ActiveRuns: a.runs.ActiveCount(),
		SettingsNavigation: newSettingsNavigation(current, locale, "updates"),
		ShowSourceDrawer:   request.URL.Query().Get("sources") == "1",
		CanRestartService:  a.requestRestart != nil,
		RestartRequested:   request.URL.Query().Get("restarting") == "1",
	}
	data.Sources, data.SourceName = updateSourcesForWeb(locale, a.updates.Sources(), snapshot.SourceID)
	switch snapshot.InstallMode {
	case "development":
		data.Capability = webText(locale, "updates.development_capability")
	case "portable":
		data.Capability = webText(locale, "updates.portable_capability")
	}
	data.BuiltAt, _ = time.Parse(time.RFC3339, snapshot.Build.BuiltAt)
	if snapshot.Latest != nil {
		if asset, ok := snapshot.Latest.AssetFor(runtime.GOOS, runtime.GOARCH); ok {
			data.LatestAsset = asset.Name
			data.LatestSize = humanBytes(uint64(asset.Size))
		}
		data.LatestPublished, _ = time.Parse(time.RFC3339, snapshot.Latest.PublishedAt)
	}
	if snapshot.Operation != nil && snapshot.Operation.Phase == updatepkg.PhasePrepared {
		data.Prepared = true
		data.PreparedID = snapshot.Operation.ID
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := updatesTemplate.Execute(response, data); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}
}

func updateSourcesForWeb(locale webLocale, descriptors []updatepkg.SourceDescriptor, selected string) ([]updateSourceData, string) {
	result := make([]updateSourceData, 0, len(descriptors))
	selectedName := ""
	for _, descriptor := range descriptors {
		option := updateSourceData{ID: descriptor.ID, Host: descriptor.Host, Selected: descriptor.ID == selected}
		switch descriptor.ID {
		case updatepkg.SourceGHProxy:
			option.Name = webText(locale, "updates.source_gh_proxy")
			option.Description = webText(locale, "updates.source_gh_proxy_description")
			option.Badge = webText(locale, "updates.source_public_proxy")
			option.Icon = "network"
		case updatepkg.SourceGHProxyNet:
			option.Name = webText(locale, "updates.source_ghproxy_net")
			option.Description = webText(locale, "updates.source_ghproxy_net_description")
			option.Badge = webText(locale, "updates.source_public_proxy")
			option.Icon = "zap"
		default:
			option.Name = webText(locale, "updates.source_github")
			option.Description = webText(locale, "updates.source_github_description")
			option.Badge = webText(locale, "updates.source_official")
			option.Icon = "globe-2"
		}
		if option.Selected {
			selectedName = option.Name
		}
		result = append(result, option)
	}
	if selectedName == "" && len(result) > 0 {
		result[0].Selected = true
		selectedName = result[0].Name
	}
	return result, selectedName
}

func (a *App) updateStatus(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(struct {
		updatepkg.Snapshot
		Validation bool   `json:"validation"`
		InstanceID string `json:"instance_id"`
	}{Snapshot: a.updates.Snapshot(), Validation: a.validation.Load(), InstanceID: a.instanceID})
}

func (a *App) restartService(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(locale, "updates.restart_confirm_error"), http.StatusForbidden)
		return
	}
	if a.requestRestart == nil {
		http.Error(response, webText(locale, "updates.restart_unavailable"), http.StatusConflict)
		return
	}
	if !a.restartRequested.CompareAndSwap(false, true) {
		http.Error(response, webText(locale, "updates.restart_pending"), http.StatusConflict)
		return
	}
	if err := a.requestRestart(); err != nil {
		a.restartRequested.Store(false)
		a.recordAuditForRequest(request, "service_restart_requested", "ScriptBoard", "failed")
		http.Error(response, webText(locale, "updates.restart_failed")+": "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	a.recordAuditForRequest(request, "service_restart_requested", "ScriptBoard", "accepted")
	if strings.Contains(request.Header.Get("Accept"), "application/json") {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(map[string]string{
			"instance_id": a.instanceID,
			"status_url":  "/settings/updates/status",
		})
		return
	}
	http.Redirect(response, request, "/settings/updates?restarting=1", http.StatusSeeOther)
}

func (a *App) checkUpdate(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "updates.csrf_error"), http.StatusForbidden)
		return
	}
	snapshot, err := a.updates.CheckFrom(request.Context(), true, request.FormValue("source_id"))
	if err != nil {
		a.recordAuditForRequest(request, "update_check_requested", "stable", "failed")
		status := http.StatusBadGateway
		if errors.Is(err, updatepkg.ErrUnknownSource) {
			status = http.StatusBadRequest
		}
		http.Error(response, webText(locale, "updates.check_failed")+": "+err.Error(), status)
		return
	}
	result := "current"
	if snapshot.UpdateAvailable && snapshot.Latest != nil {
		result = snapshot.Latest.Version
	}
	a.recordAuditForRequest(request, "update_check_requested", result, "succeeded")
	http.Redirect(response, request, "/settings/updates", http.StatusSeeOther)
}

func (a *App) prepareUpdate(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "updates.csrf_error"), http.StatusForbidden)
		return
	}
	operation, err := a.updates.Prepare(request.Context())
	if err != nil {
		a.recordAuditForRequest(request, "update_prepare_failed", "stable", "failed")
		http.Error(response, webText(locale, "updates.prepare_failed")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "update_prepare_started", operation.TargetVersion, "succeeded")
	http.Redirect(response, request, "/settings/updates", http.StatusSeeOther)
}

func (a *App) applyUpdate(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(locale, "updates.confirm_error"), http.StatusForbidden)
		return
	}
	operationID := request.FormValue("operation_id")
	active, entered := a.beginUpdateMaintenance()
	if !entered {
		http.Error(response, fmt.Sprintf(webText(locale, "updates.active_runs_error"), active), http.StatusConflict)
		return
	}
	operation, err := a.updates.Handoff(operationID)
	if err != nil {
		a.endUpdateMaintenance()
		a.recordAuditForRequest(request, "update_apply_requested", operationID, "failed")
		http.Error(response, webText(locale, "updates.handoff_failed")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.signalUpdateResults()
	a.recordAuditForRequest(request, "update_apply_requested", operation.TargetVersion, "succeeded")
	response.Header().Set("Connection", "close")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(response).Encode(map[string]string{
		"operation_id": operation.ID, "status_url": "/settings/updates/status",
	})
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
}
