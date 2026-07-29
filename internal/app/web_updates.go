package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	updatepkg "scriptboard/internal/update"
)

type updatesPageData struct {
	Snapshot        updatepkg.Snapshot
	CSRFToken       string
	Locale          webLocale
	ActiveRuns      int
	LatestAsset     string
	LatestSize      string
	LatestPublished time.Time
	BuiltAt         time.Time
	PreparedID      string
	Prepared        bool
	Capability      string
}

func (a *App) updatesPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	snapshot := a.updates.Snapshot()
	locale := resolveWebLocale(request)
	data := updatesPageData{
		Snapshot: snapshot, CSRFToken: current.csrfToken,
		Locale: locale, ActiveRuns: a.runs.ActiveCount(),
	}
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

func (a *App) updateStatus(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(struct {
		updatepkg.Snapshot
		Validation bool `json:"validation"`
	}{Snapshot: a.updates.Snapshot(), Validation: a.validation.Load()})
}

func (a *App) checkUpdate(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "updates.csrf_error"), http.StatusForbidden)
		return
	}
	snapshot, err := a.updates.Check(request.Context(), true)
	if err != nil {
		a.recordAudit("update_check_requested", "stable", "failed", request.RemoteAddr)
		http.Error(response, webText(locale, "updates.check_failed")+": "+err.Error(), http.StatusBadGateway)
		return
	}
	result := "current"
	if snapshot.UpdateAvailable && snapshot.Latest != nil {
		result = snapshot.Latest.Version
	}
	a.recordAudit("update_check_requested", result, "succeeded", request.RemoteAddr)
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
		a.recordAudit("update_prepare_failed", "stable", "failed", request.RemoteAddr)
		http.Error(response, webText(locale, "updates.prepare_failed")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("update_prepare_started", operation.TargetVersion, "succeeded", request.RemoteAddr)
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
		a.recordAudit("update_apply_requested", operationID, "failed", request.RemoteAddr)
		http.Error(response, webText(locale, "updates.handoff_failed")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("update_apply_requested", operation.TargetVersion, "succeeded", request.RemoteAddr)
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
