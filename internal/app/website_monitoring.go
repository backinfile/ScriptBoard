package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/secretredaction"
	"scriptboard/internal/websitemonitor"
)

type websiteMonitorPageView struct {
	ID                    string
	Name                  string
	URL                   string
	Scope                 websitemonitor.Scope
	ScopeLabel            string
	Kind                  websitemonitor.Kind
	KindLabel             string
	MethodLabel           string
	State                 websitemonitor.State
	StateLabel            string
	LatestLabel           string
	LatestSummary         string
	LatestTechnical       string
	LatencyLabel          string
	LastCheckedAt         time.Time
	CheckedToken          int64
	FrequencyLabel        string
	TimeoutLabel          string
	TLSAttention          bool
	SecurityTone          string
	SecurityTitle         string
	SecurityDescription   string
	Certificate           websitemonitor.Certificate
	SortOrder             int
	Availability          []websiteMonitorAvailabilityBucketView
	FailureCount          int
	NextCheckAt           time.Time
	IncidentStartedAt     time.Time
	IncidentDuration      time.Duration
	IncidentDurationLabel string
	CanMoveUp             bool
	CanMoveDown           bool
}

type websiteMonitorCounts struct {
	Up        int `json:"up"`
	Verifying int `json:"verifying"`
	Down      int `json:"down"`
	Paused    int `json:"paused"`
}

type websiteMonitorListView struct {
	Monitors      []websiteMonitorPageView
	Alerts        []websiteMonitorPageView
	Counts        websiteMonitorCounts
	Locale        webLocale
	State         websitemonitor.State
	Scope         websitemonitor.Scope
	CSRFToken     string
	Total         int
	NeedsCare     int
	HasFilters    bool
	HasAny        bool
	Reorder       bool
	CanManage     bool
	DataURL       string
	RefreshURL    string
	RemoteSources []websiteMonitorRemoteSourceView
}

type websiteMonitorFormView struct {
	Config               websitemonitor.Config
	Errors               map[string]string
	Locale               webLocale
	CSRFToken            string
	Editing              bool
	ID                   string
	FrequencySeconds     int64
	TimeoutSeconds       int64
	ExpectedStatusesText string
	RequestHeadersText   string
}

type websiteMonitorNginxView struct {
	ConfigPath    string
	Preview       *websitemonitor.NginxPreview
	Error         string
	ImportedCount int
	Success       string
	Locale        webLocale
	CSRFToken     string
}

type websiteMonitorListDataView struct {
	Monitors  []websiteMonitorPageView `json:"monitors"`
	Alerts    []websiteMonitorPageView `json:"alerts"`
	Counts    websiteMonitorCounts     `json:"counts"`
	Total     int                      `json:"total"`
	NeedsCare int                      `json:"needsCare"`
}

type websiteMonitorDetailDataView struct {
	websiteMonitorPageView
	DetailAvailability  []websiteMonitorAvailabilityBucketView
	AvailabilityPercent float64
	AvailabilityLabel   string
	AverageLatency      time.Duration
	AverageLatencyLabel string
	P95Latency          time.Duration
	P95LatencyLabel     string
	TotalChecks         int
	SuccessfulChecks    int
	FailedChecks        int
	IncidentCount       int
	RecentChecks        []websitemonitor.Evidence
	CurrentIncident     *websitemonitor.IncidentSnapshot
	Incidents           []websitemonitor.Incident
}

type websiteMonitorAvailabilityBucketView struct {
	StartedAt        time.Time
	EndedAt          time.Time
	State            websitemonitor.Availability
	Tone             string
	Provisional      bool
	Title            string
	TotalChecks      int
	SuccessfulChecks int
	FailedChecks     int
}

type websiteMonitorDetailView struct {
	Monitor             websiteMonitorPageView
	Raw                 websitemonitor.Monitor
	StatusRuleLabel     string
	Snapshot            websitemonitor.DetailSnapshot
	DetailAvailability  []websiteMonitorAvailabilityBucketView
	AvailabilityPercent float64
	AvailabilityLabel   string
	AverageLatency      time.Duration
	AverageLatencyLabel string
	P95Latency          time.Duration
	P95LatencyLabel     string
	TotalChecks         int
	SuccessfulChecks    int
	FailedChecks        int
	IncidentCount       int
	RecentChecks        []websitemonitor.Evidence
	CurrentIncident     *websitemonitor.IncidentSnapshot
	Incidents           []websitemonitor.Incident
	Locale              webLocale
	CSRFToken           string
	CanManage           bool
}

type websiteMonitorAPIError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"error"`
}

func (a *App) websiteMonitorList(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	state := websitemonitor.State(request.URL.Query().Get("state"))
	if !validWebsiteStateFilter(state) {
		http.Error(response, webText(locale, "website.error.invalid_state_filter"), http.StatusBadRequest)
		return
	}
	scope := websitemonitor.Scope(request.URL.Query().Get("scope"))
	if !validWebsiteScopeFilter(scope) {
		http.Error(response, webText(locale, "website.error.invalid_scope_filter"), http.StatusBadRequest)
		return
	}
	monitors, err := a.websiteMonitorsForPage(request.Context(), state, scope)
	if err != nil {
		http.Error(response, webText(locale, "website.error.read_monitors")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	all, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, webText(locale, "website.error.summarize_monitors")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	remoteSources, err := a.websiteMonitorRemoteSources(request.Context(), locale)
	if err != nil {
		http.Error(response, webText(locale, "website.remote.read_failed"), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	view := websiteMonitorListView{
		Locale: locale, State: state, Scope: scope, CSRFToken: current.csrfToken,
		Total: len(monitors), HasFilters: state != "" || scope != "",
		HasAny: len(all) > 0, CanManage: roleAllows(current.role, permissionManageOperations),
		DataURL: "/monitor/websites/data", RefreshURL: request.URL.RequestURI(),
		RemoteSources: remoteSources,
	}
	view.Reorder = view.CanManage && request.URL.Query().Get("reorder") == "1"
	query := request.URL.Query()
	query.Del("reorder")
	if encoded := query.Encode(); encoded != "" {
		view.DataURL += "?" + encoded
	}
	snapshot := a.newWebsiteMonitorListDataView(request.Context(), monitors, all, locale)
	view.Monitors = snapshot.Monitors
	view.Alerts = snapshot.Alerts
	view.Counts = snapshot.Counts
	view.NeedsCare = snapshot.NeedsCare
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = websiteMonitorListTemplate.Execute(response, view)
}

func (a *App) websiteMonitorData(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	state := websitemonitor.State(request.URL.Query().Get("state"))
	scope := websitemonitor.Scope(request.URL.Query().Get("scope"))
	if !validWebsiteStateFilter(state) || !validWebsiteScopeFilter(scope) {
		writeWebsiteMonitorJSONError(response, http.StatusBadRequest,
			string(websitemonitor.ErrorInvalidInput), webText(locale, "website.error.invalid_filter"), "")
		return
	}
	monitors, err := a.websiteMonitorsForPage(request.Context(), state, scope)
	if err != nil {
		writeWebsiteMonitorJSONError(response, http.StatusInternalServerError,
			"read_failed", webText(locale, "website.error.read_monitors"), "")
		return
	}
	all, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		writeWebsiteMonitorJSONError(response, http.StatusInternalServerError,
			"read_failed", webText(locale, "website.error.summarize_monitors"), "")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(
		a.newWebsiteMonitorListDataView(request.Context(), monitors, all, locale),
	)
}

func (a *App) newWebsiteMonitorListDataView(
	ctx context.Context,
	monitors []websitemonitor.Monitor,
	all []websitemonitor.Monitor,
	locale webLocale,
) websiteMonitorListDataView {
	result := websiteMonitorListDataView{
		Monitors: make([]websiteMonitorPageView, 0, len(monitors)),
		Total:    len(monitors),
	}
	fullIndex := make(map[string]int, len(all))
	for index, monitor := range all {
		fullIndex[monitor.ID] = index
	}
	viewByID := make(map[string]websiteMonitorPageView, len(monitors))
	for _, monitor := range monitors {
		item := a.newWebsiteMonitorPageView(ctx, monitor, locale)
		if index, ok := fullIndex[monitor.ID]; ok {
			item.CanMoveUp = index > 0
			item.CanMoveDown = index < len(all)-1
		}
		result.Monitors = append(result.Monitors, item)
		viewByID[monitor.ID] = item
	}
	for index, monitor := range all {
		item, ok := viewByID[monitor.ID]
		if !ok {
			item = a.newWebsiteMonitorPageView(ctx, monitor, locale)
			item.CanMoveUp = index > 0
			item.CanMoveDown = index < len(all)-1
		}
		switch monitor.State {
		case websitemonitor.StateUp:
			result.Counts.Up++
		case websitemonitor.StatePending, websitemonitor.StateVerifying:
			result.Counts.Verifying++
		case websitemonitor.StateDown:
			result.Counts.Down++
			if incident, err := a.websiteMonitor.CurrentIncident(ctx, monitor.ID); err == nil && incident != nil {
				item.IncidentStartedAt = incident.StartedAt
				item.IncidentDuration = incident.Duration
				item.IncidentDurationLabel = websiteElapsedLabel(locale, incident.Duration)
			}
			result.Alerts = append(result.Alerts, item)
		case websitemonitor.StatePaused:
			result.Counts.Paused++
		}
	}
	result.NeedsCare = result.Counts.Verifying + result.Counts.Down
	return result
}

func (a *App) websiteMonitorCreateTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	config := websitemonitor.Config{
		Scope:             websitemonitor.ScopeExternal,
		Kind:              websitemonitor.KindHTTP,
		Frequency:         time.Minute,
		Timeout:           10 * time.Second,
		HTTPMethod:        http.MethodGet,
		HTTPSuccessMode:   websitemonitor.HTTPSuccessRange,
		WebSocketSuccess:  websitemonitor.WebSocketHandshake,
		PingPayloadFormat: websitemonitor.PayloadNone,
	}
	renderWebsiteMonitorForm(response, http.StatusOK,
		newWebsiteMonitorFormView(config, nil, resolveWebLocale(request), current.csrfToken, "", false))
}

func (a *App) websiteMonitorEditTask(response http.ResponseWriter, request *http.Request) {
	monitor, err := a.websiteMonitor.Get(request.Context(), request.PathValue("id"))
	if err != nil || monitor.DeletedAt != nil {
		http.NotFound(response, request)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	renderWebsiteMonitorForm(response, http.StatusOK,
		newWebsiteMonitorFormView(monitor.Config, nil, resolveWebLocale(request), current.csrfToken, monitor.ID, true))
}

func (a *App) websiteMonitorNginxTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	view := websiteMonitorNginxView{
		Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
	}
	if imported, err := strconv.Atoi(request.URL.Query().Get("imported")); err == nil &&
		imported > 0 && imported <= 100 {
		view.ImportedCount = imported
		view.Success = "imported"
	}
	renderWebsiteMonitorNginx(response, http.StatusOK, view)
}

func (a *App) scanWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusForbidden, "csrf",
				webText(resolveWebLocale(request), "website.error.csrf"), "")
			return
		}
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	configPath := strings.TrimSpace(request.FormValue("config_path"))
	preview, err := a.websiteMonitor.ScanNginx(request.Context(), websitemonitor.NginxScanRequest{ConfigPath: configPath})
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if err != nil {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity,
				string(websitemonitor.ErrorInvalidInput), err.Error(), "config_path")
			return
		}
		renderWebsiteMonitorNginx(response, http.StatusUnprocessableEntity, websiteMonitorNginxView{
			ConfigPath: configPath, Error: secretredaction.String(err.Error()), Locale: locale, CSRFToken: current.csrfToken,
		})
		return
	}
	if websiteMonitorWantsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(preview)
		return
	}
	renderWebsiteMonitorNginx(response, http.StatusOK, websiteMonitorNginxView{
		ConfigPath: configPath, Preview: &preview, Locale: locale, CSRFToken: current.csrfToken,
	})
}

func (a *App) importWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusForbidden, "csrf",
				webText(resolveWebLocale(request), "website.error.csrf"), "")
			return
		}
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	_ = request.ParseForm()
	configPath := strings.TrimSpace(request.FormValue("config_path"))
	imported, err := a.websiteMonitor.ImportNginx(request.Context(), websitemonitor.NginxImportRequest{
		Scan:    websitemonitor.NginxScanRequest{ConfigPath: configPath},
		Digests: request.Form["digest"],
	})
	if err != nil {
		if websiteMonitorWantsJSON(request) {
			code, field := websiteMonitorOperationError(err, websitemonitor.ErrorConflict)
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity, code, err.Error(), field)
			return
		}
		current := request.Context().Value(sessionContextKey).(session)
		preview, _ := a.websiteMonitor.ScanNginx(request.Context(), websitemonitor.NginxScanRequest{ConfigPath: configPath})
		renderWebsiteMonitorNginx(response, http.StatusUnprocessableEntity, websiteMonitorNginxView{
			ConfigPath: configPath, Preview: &preview, Error: secretredaction.String(err.Error()),
			Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
		})
		return
	}
	a.recordAuditForRequest(request, "import_nginx_website_monitors", fmt.Sprintf("%d monitors", len(imported)), "succeeded")
	if websiteMonitorWantsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(response).Encode(struct {
			ImportedCount int                      `json:"importedCount"`
			Monitors      []websitemonitor.Monitor `json:"monitors"`
			RedirectURL   string                   `json:"redirectURL"`
		}{
			ImportedCount: len(imported),
			Monitors:      imported, RedirectURL: "/monitor/websites",
		})
		return
	}
	http.Redirect(response, request,
		fmt.Sprintf("/monitor/websites/nginx?imported=%d", len(imported)),
		http.StatusSeeOther,
	)
}

func (a *App) createWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	config, fieldErrors := websiteMonitorConfigFromRequest(request)
	if len(fieldErrors) > 0 {
		if acceptsJSON(request) {
			field, message := firstWebsiteMonitorFieldError(fieldErrors)
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity, string(websitemonitor.ErrorInvalidInput), message, field)
			return
		}
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, resolveWebLocale(request), current.csrfToken, "", false))
		return
	}
	monitor, err := a.websiteMonitor.Create(request.Context(), config)
	if err != nil {
		if acceptsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity, string(websitemonitor.ErrorInvalidInput), err.Error(), "form")
			return
		}
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, resolveWebLocale(request), current.csrfToken, "", false))
		return
	}
	a.recordAuditForRequest(request, "create_website_monitor", websiteMonitorAuditTarget(monitor.Config), "succeeded")
	response.Header().Set(assistantResourceIDHeader, monitor.ID)
	http.Redirect(response, request, "/monitor/websites/"+monitor.ID, http.StatusSeeOther)
}

func (a *App) updateWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	config, fieldErrors := websiteMonitorConfigFromRequest(request)
	if len(fieldErrors) > 0 {
		if acceptsJSON(request) {
			field, message := firstWebsiteMonitorFieldError(fieldErrors)
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity, string(websitemonitor.ErrorInvalidInput), message, field)
			return
		}
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, resolveWebLocale(request), current.csrfToken, id, true))
		return
	}
	monitor, err := a.websiteMonitor.Update(request.Context(), id, config)
	if err != nil {
		if acceptsJSON(request) {
			code, field := websiteMonitorOperationError(err, websitemonitor.ErrorInvalidInput)
			writeWebsiteMonitorJSONError(response, http.StatusUnprocessableEntity, code, err.Error(), field)
			return
		}
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, resolveWebLocale(request), current.csrfToken, id, true))
		return
	}
	a.recordAuditForRequest(request, "update_website_monitor", websiteMonitorAuditTarget(monitor.Config), "succeeded")
	http.Redirect(response, request, "/monitor/websites/"+monitor.ID, http.StatusSeeOther)
}

func websiteMonitorAuditTarget(config websitemonitor.Config) string {
	target := config.Name
	if config.SkipTLSVerification {
		target += " tls_verification_disabled_until=" + config.TLSVerificationDisabledUntil.UTC().Format(time.RFC3339)
	}
	return target
}

func firstWebsiteMonitorFieldError(fieldErrors map[string]string) (string, string) {
	for _, field := range []string{"frequency_seconds", "timeout_seconds", "name", "url", "expected_statuses", "form"} {
		if message := strings.TrimSpace(fieldErrors[field]); message != "" {
			return field, message
		}
	}
	for field, message := range fieldErrors {
		return field, message
	}
	return "form", "Website monitor configuration is invalid"
}

func (a *App) websiteMonitorDetail(response http.ResponseWriter, request *http.Request) {
	snapshot, err := a.websiteMonitor.DetailSnapshot(request.Context(), request.PathValue("id"))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			http.Error(response, webText(resolveWebLocale(request), "website.error.request_canceled"), http.StatusRequestTimeout)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, webText(resolveWebLocale(request), "website.error.read_monitors")+": "+err.Error(),
			http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	details := a.newWebsiteMonitorDetailDataView(request.Context(), snapshot, locale)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = websiteMonitorDetailTemplate.Execute(response, websiteMonitorDetailView{
		Monitor: details.websiteMonitorPageView, Raw: snapshot.Monitor, Snapshot: snapshot,
		StatusRuleLabel:     websiteHTTPStatusRuleLabel(locale, snapshot.Monitor.Config),
		DetailAvailability:  details.DetailAvailability,
		AvailabilityPercent: details.AvailabilityPercent,
		AvailabilityLabel:   details.AvailabilityLabel,
		AverageLatency:      details.AverageLatency, AverageLatencyLabel: details.AverageLatencyLabel,
		P95Latency: details.P95Latency, P95LatencyLabel: details.P95LatencyLabel,
		TotalChecks: details.TotalChecks, SuccessfulChecks: details.SuccessfulChecks,
		FailedChecks: details.FailedChecks, IncidentCount: details.IncidentCount,
		RecentChecks: details.RecentChecks, CurrentIncident: details.CurrentIncident,
		Incidents: details.Incidents, Locale: locale, CSRFToken: current.csrfToken,
		CanManage: roleAllows(current.role, permissionManageOperations),
	})
}

func websiteHTTPStatusRuleLabel(locale webLocale, config websitemonitor.Config) string {
	switch config.HTTPSuccessMode {
	case websitemonitor.HTTPSuccessExact:
		return websitemonitor.FormatHTTPStatusRanges(websitemonitor.ExpectedHTTPStatusRanges(config))
	case websitemonitor.HTTPSuccessAnyResponse:
		return webText(locale, "website.form.any_http_response")
	default:
		return "200–399"
	}
}

func (a *App) websiteMonitorDetailData(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	snapshot, err := a.websiteMonitor.DetailSnapshot(request.Context(), request.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		code := "read_failed"
		message := webText(locale, "website.error.read_monitors")
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
			code = string(websitemonitor.ErrorNotFound)
			message = http.StatusText(http.StatusNotFound)
		} else if errors.Is(err, context.Canceled) {
			status = http.StatusRequestTimeout
			code = "request_canceled"
			message = webText(locale, "website.error.request_canceled")
		}
		writeWebsiteMonitorJSONError(response, status, code, message, "")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(
		a.newWebsiteMonitorDetailDataView(request.Context(), snapshot, locale),
	)
}

func (a *App) newWebsiteMonitorDetailDataView(
	ctx context.Context,
	snapshot websitemonitor.DetailSnapshot,
	locale webLocale,
) websiteMonitorDetailDataView {
	result := websiteMonitorDetailDataView{
		websiteMonitorPageView: a.newWebsiteMonitorPageView(ctx, snapshot.Monitor, locale),
		AvailabilityPercent:    snapshot.AvailabilityPercent,
		AverageLatency:         snapshot.AverageLatency,
		P95Latency:             snapshot.P95Latency,
		TotalChecks:            snapshot.TotalChecks,
		SuccessfulChecks:       snapshot.SuccessfulChecks,
		FailedChecks:           snapshot.FailedChecks,
		IncidentCount:          snapshot.IncidentCount,
		RecentChecks:           append([]websitemonitor.Evidence(nil), snapshot.RecentChecks...),
		Incidents:              append([]websitemonitor.Incident(nil), snapshot.Incidents...),
	}
	if snapshot.CurrentIncident != nil {
		currentIncident := *snapshot.CurrentIncident
		currentIncident.Summary = localizeWebsiteMonitorSummary(locale, currentIncident.Summary)
		result.CurrentIncident = &currentIncident
	}
	if snapshot.TotalChecks == 0 {
		result.AvailabilityLabel = "—"
		result.AverageLatencyLabel = "—"
		result.P95LatencyLabel = "—"
	} else {
		result.AvailabilityLabel = fmt.Sprintf("%.2f%%", snapshot.AvailabilityPercent)
		result.AverageLatencyLabel = fmt.Sprintf("%d ms", snapshot.AverageLatency.Milliseconds())
		result.P95LatencyLabel = fmt.Sprintf("%d ms", snapshot.P95Latency.Milliseconds())
	}
	for index := range result.RecentChecks {
		result.RecentChecks[index].Summary =
			localizeWebsiteMonitorSummary(locale, result.RecentChecks[index].Summary)
	}
	for index := range result.Incidents {
		result.Incidents[index].Summary =
			localizeWebsiteMonitorSummary(locale, result.Incidents[index].Summary)
	}
	result.DetailAvailability = make(
		[]websiteMonitorAvailabilityBucketView, 0, len(snapshot.Availability),
	)
	for _, bucket := range snapshot.Availability {
		result.DetailAvailability = append(
			result.DetailAvailability,
			newWebsiteMonitorAvailabilityBucketView(
				locale,
				bucket,
				20*time.Minute,
				snapshot.Monitor.Latest.CheckedAt,
				snapshot.Monitor.State,
			),
		)
	}
	return result
}

func (a *App) checkWebsiteMonitorNow(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusForbidden, "csrf",
				webText(locale, "website.error.csrf"), "")
			return
		}
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.CheckNow(request.Context(), request.PathValue("id")); err != nil {
		if websiteMonitorWantsJSON(request) {
			code := string(websitemonitor.ErrorConflict)
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				code = string(websitemonitor.ErrorNotFound)
				status = http.StatusNotFound
			}
			writeWebsiteMonitorJSONError(response, status, code,
				webText(locale, "website.error.check_now")+": "+err.Error(), "")
			return
		}
		http.Error(response, webText(locale, "website.error.check_now")+": "+err.Error(), http.StatusConflict)
		return
	}
	if websiteMonitorWantsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(map[string]string{
			"status": "checking", "monitorID": request.PathValue("id"),
		})
		return
	}
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) pauseWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusForbidden, "csrf",
				webText(locale, "website.error.csrf"), "")
			return
		}
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Pause(request.Context(), request.PathValue("id")); err != nil {
		if websiteMonitorWantsJSON(request) {
			code := string(websitemonitor.ErrorConflict)
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				code = string(websitemonitor.ErrorNotFound)
				status = http.StatusNotFound
			}
			writeWebsiteMonitorJSONError(response, status, code,
				webText(locale, "website.error.pause")+": "+err.Error(), "")
			return
		}
		http.Error(response, webText(locale, "website.error.pause")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "pause_website_monitor", request.PathValue("id"), "succeeded")
	if websiteMonitorWantsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]string{
			"status": "paused", "monitorID": request.PathValue("id"),
		})
		return
	}
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) resumeWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		if websiteMonitorWantsJSON(request) {
			writeWebsiteMonitorJSONError(response, http.StatusForbidden, "csrf",
				webText(locale, "website.error.csrf"), "")
			return
		}
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Resume(request.Context(), request.PathValue("id")); err != nil {
		if websiteMonitorWantsJSON(request) {
			code := string(websitemonitor.ErrorConflict)
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				code = string(websitemonitor.ErrorNotFound)
				status = http.StatusNotFound
			}
			writeWebsiteMonitorJSONError(response, status, code,
				webText(locale, "website.error.resume")+": "+err.Error(), "")
			return
		}
		http.Error(response, webText(locale, "website.error.resume")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "resume_website_monitor", request.PathValue("id"), "succeeded")
	if websiteMonitorWantsJSON(request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(map[string]string{
			"status": "checking", "monitorID": request.PathValue("id"),
		})
		return
	}
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) moveWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	direction := 0
	switch request.FormValue("direction") {
	case "up":
		direction = -1
	case "down":
		direction = 1
	}
	if err := a.websiteMonitor.Move(request.Context(), request.PathValue("id"), direction); err != nil {
		http.Error(response, webText(locale, "website.error.move")+": "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "reorder_website_monitors", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/monitor/websites?reorder=1", http.StatusSeeOther)
}

func (a *App) reorderWebsiteMonitors(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, webText(locale, "website.error.invalid_order"), http.StatusBadRequest)
		return
	}
	if err := a.websiteMonitor.Reorder(request.Context(), request.Form["id"]); err != nil {
		http.Error(response, webText(locale, "website.error.save_order")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "reorder_website_monitors", fmt.Sprintf("%d monitors", len(request.Form["id"])), "succeeded")
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) deleteWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(locale, "website.error.delete_confirmation"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Delete(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, webText(locale, "website.error.delete")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_website_monitor", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func renderWebsiteMonitorForm(response http.ResponseWriter, status int, view websiteMonitorFormView) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = websiteMonitorFormTemplate.Execute(response, view)
}

func newWebsiteMonitorFormView(config websitemonitor.Config, fieldErrors map[string]string, locale webLocale, csrfToken, id string, editing bool) websiteMonitorFormView {
	return websiteMonitorFormView{
		Config:               config,
		Errors:               fieldErrors,
		Locale:               locale,
		CSRFToken:            csrfToken,
		Editing:              editing,
		ID:                   id,
		FrequencySeconds:     int64(config.Frequency / time.Second),
		TimeoutSeconds:       int64(config.Timeout / time.Second),
		ExpectedStatusesText: websitemonitor.FormatHTTPStatusRanges(websitemonitor.ExpectedHTTPStatusRanges(config)),
		RequestHeadersText:   websitemonitor.FormatRequestHeaders(config.RequestHeaders),
	}
}

func renderWebsiteMonitorNginx(response http.ResponseWriter, status int, view websiteMonitorNginxView) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = websiteMonitorNginxTemplate.Execute(response, view)
}

func websiteMonitorWantsJSON(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept")), "application/json")
}

func writeWebsiteMonitorJSONError(
	response http.ResponseWriter,
	status int,
	code string,
	message string,
	field string,
) {
	payload := websiteMonitorAPIError{}
	payload.Error.Code = code
	payload.Error.Message = message
	payload.Error.Field = field
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func websiteMonitorOperationError(err error, fallback websitemonitor.ErrorCode) (string, string) {
	var operationError *websitemonitor.OperationError
	if errors.As(err, &operationError) {
		return string(operationError.Code), operationError.Field
	}
	return string(fallback), ""
}

func websiteMonitorConfigFromRequest(request *http.Request) (websitemonitor.Config, map[string]string) {
	_ = request.ParseForm()
	locale := resolveWebLocale(request)
	config := websitemonitor.Config{
		Name:                strings.TrimSpace(request.FormValue("name")),
		Scope:               websitemonitor.Scope(request.FormValue("scope")),
		Kind:                websitemonitor.Kind(request.FormValue("kind")),
		URL:                 strings.TrimSpace(request.FormValue("url")),
		SkipTLSVerification: request.FormValue("verify_tls") != "1",
		Source:              "manual",
	}
	errors := map[string]string{}
	if headers, err := websitemonitor.ParseRequestHeaders(request.FormValue("request_headers")); err != nil {
		errors["request_headers"] = webText(locale, "website.error.request_headers")
	} else {
		config.RequestHeaders = headers
	}
	if config.Kind == websitemonitor.KindHTTP {
		config.HTTPMethod = request.FormValue("http_method")
		config.HTTPContentType = request.FormValue("http_content_type")
		config.HTTPBody = request.FormValue("http_body")
		config.HTTPSuccessMode = websitemonitor.HTTPSuccessMode(request.FormValue("http_success_mode"))
		config.ResponseKeyword = request.FormValue("response_keyword")
		config.DisableRedirects = request.FormValue("follow_redirects") != "1"
	} else if config.Kind == websitemonitor.KindWebSocket {
		config.WebSocketSuccess = websitemonitor.WebSocketSuccess(request.FormValue("websocket_success"))
		if config.WebSocketSuccess == websitemonitor.WebSocketPingPong {
			config.PingPayloadFormat = websitemonitor.PayloadFormat(request.FormValue("ping_payload_format"))
			config.PingPayload = request.FormValue("ping_payload")
		} else if config.WebSocketSuccess == websitemonitor.WebSocketAnyMessage ||
			config.WebSocketSuccess == websitemonitor.WebSocketMatchingMessage {
			config.SendType = websitemonitor.MessageType(request.FormValue("send_type"))
			config.SendPayload = request.FormValue("send_payload")
			config.ReceiveType = websitemonitor.MessageType(request.FormValue("receive_type"))
			config.ExpectedMessage = request.FormValue("expected_message")
		}
	}
	if value, err := strconv.Atoi(request.FormValue("frequency_seconds")); err == nil {
		config.Frequency = time.Duration(value) * time.Second
		if value != 30 && value != 60 && value != 300 && value != 900 {
			errors["frequency_seconds"] = webText(locale, "website.error.frequency_choice")
		}
	} else {
		errors["frequency_seconds"] = webText(locale, "website.error.frequency_invalid")
	}
	if value, err := strconv.Atoi(request.FormValue("timeout_seconds")); err == nil {
		config.Timeout = time.Duration(value) * time.Second
		if value != 3 && value != 5 && value != 10 && value != 30 {
			errors["timeout_seconds"] = webText(locale, "website.error.timeout_choice")
		}
	} else {
		errors["timeout_seconds"] = webText(locale, "website.error.timeout_invalid")
	}
	if config.Kind == websitemonitor.KindHTTP && config.HTTPSuccessMode == websitemonitor.HTTPSuccessExact {
		statusInput := strings.TrimSpace(request.FormValue("expected_statuses"))
		if statusInput == "" {
			errors["expected_statuses"] = webText(locale, "website.error.status_required")
		} else if ranges, err := websitemonitor.ParseHTTPStatusRanges(statusInput); err != nil {
			errors["expected_statuses"] = webText(locale, "website.error.status_range")
		} else {
			config.ExpectedStatusRanges = ranges
		}
	}
	if config.Kind == websitemonitor.KindWebSocket &&
		config.SendType == websitemonitor.MessageBinary &&
		config.SendPayload != "" {
		if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.SendPayload)); err != nil {
			errors["send_payload"] = webText(locale, "website.error.binary_base64")
		}
	}
	if len(errors) == 0 {
		return config, nil
	}
	return config, errors
}

func validWebsiteStateFilter(value websitemonitor.State) bool {
	switch value {
	case "", websitemonitor.StatePending, websitemonitor.StateUp, websitemonitor.StateVerifying,
		websitemonitor.StateDown, websitemonitor.StatePaused:
		return true
	default:
		return false
	}
}

func (a *App) websiteMonitorsForPage(
	ctx context.Context,
	state websitemonitor.State,
	scope websitemonitor.Scope,
) ([]websitemonitor.Monitor, error) {
	if state != websitemonitor.StateVerifying {
		return a.websiteMonitor.List(ctx, websitemonitor.Filter{State: state, Scope: scope})
	}
	candidates, err := a.websiteMonitor.List(ctx, websitemonitor.Filter{Scope: scope})
	if err != nil {
		return nil, err
	}
	filtered := make([]websitemonitor.Monitor, 0, len(candidates))
	for _, monitor := range candidates {
		if monitor.State == websitemonitor.StatePending || monitor.State == websitemonitor.StateVerifying {
			filtered = append(filtered, monitor)
		}
	}
	return filtered, nil
}

func validWebsiteScopeFilter(value websitemonitor.Scope) bool {
	return value == "" || value == websitemonitor.ScopeLocal || value == websitemonitor.ScopeExternal
}

func (a *App) newWebsiteMonitorPageView(ctx context.Context, monitor websitemonitor.Monitor, locale webLocale) websiteMonitorPageView {
	view := websiteMonitorPageView{
		ID: monitor.ID, Name: monitor.Config.Name, URL: monitor.Config.URL,
		Scope: monitor.Config.Scope, ScopeLabel: websiteScopeLabel(locale, monitor.Config.Scope),
		Kind: monitor.Config.Kind, KindLabel: websiteKindLabel(locale, monitor.Config.Kind),
		MethodLabel: monitor.Config.HTTPMethod, State: monitor.State,
		StateLabel:      websiteStateLabel(locale, monitor.State),
		LatestSummary:   localizeWebsiteMonitorSummary(locale, monitor.Latest.Summary),
		LatestTechnical: monitor.Latest.TechnicalError, LastCheckedAt: monitor.Latest.CheckedAt,
		FrequencyLabel: websiteDurationLabel(locale, monitor.Config.Frequency),
		TimeoutLabel:   websiteDurationLabel(locale, monitor.Config.Timeout),
		Certificate:    monitor.Latest.Certificate, SortOrder: monitor.SortOrder,
		FailureCount: monitor.FailureCount, NextCheckAt: monitor.NextCheckAt,
	}
	if monitor.Config.Kind == websitemonitor.KindWebSocket {
		view.MethodLabel = "WebSocket"
	}
	if !monitor.Latest.CheckedAt.IsZero() {
		view.CheckedToken = monitor.Latest.CheckedAt.UnixNano()
	}
	if monitor.Latest.StatusCode > 0 {
		view.LatestLabel = fmt.Sprintf("HTTP %d", monitor.Latest.StatusCode)
	} else if monitor.Latest.Summary != "" {
		view.LatestLabel = view.LatestSummary
	} else {
		view.LatestLabel = webText(locale, "website.waiting_first_check")
	}
	if !monitor.Latest.CheckedAt.IsZero() {
		view.LatencyLabel = fmt.Sprintf("%d ms", monitor.Latest.Latency.Milliseconds())
	} else {
		view.LatencyLabel = "—"
	}
	view.SecurityTone, view.SecurityTitle, view.SecurityDescription = websiteSecuritySummary(locale, monitor)
	view.TLSAttention = !monitor.Latest.Certificate.NotAfter.IsZero() &&
		monitor.Latest.Certificate.DaysRemaining <= 14
	history, err := a.websiteMonitor.Availability24h(ctx, monitor.ID)
	if err != nil || len(history) != 48 {
		history = make([]websitemonitor.AvailabilityBucket, 48)
	}
	view.Availability = make([]websiteMonitorAvailabilityBucketView, len(history))
	for index := range history {
		if history[index].State == "" {
			history[index].State = websitemonitor.AvailabilityGap
		}
		view.Availability[index] = newWebsiteMonitorAvailabilityBucketView(
			locale,
			history[index],
			30*time.Minute,
			monitor.Latest.CheckedAt,
			monitor.State,
		)
	}
	return view
}

func newWebsiteMonitorAvailabilityBucketView(
	locale webLocale,
	bucket websitemonitor.AvailabilityBucket,
	bucketSize time.Duration,
	latestCheckedAt time.Time,
	monitorState websitemonitor.State,
) websiteMonitorAvailabilityBucketView {
	endedAt := bucket.StartedAt.Add(bucketSize)
	tone := string(bucket.State)
	title := websiteAvailabilityLabel(locale, bucket.State)
	if !bucket.StartedAt.IsZero() {
		title = fmt.Sprintf(
			"%s–%s · %s",
			bucket.StartedAt.Format("2006-01-02 15:04"),
			endedAt.Format("15:04"),
			title,
		)
	}
	if bucket.Provisional {
		provisionalLabel := websiteAvailabilityLabel(locale, bucket.State)
		switch monitorState {
		case websitemonitor.StateUp, websitemonitor.StateVerifying, websitemonitor.StateDown:
			tone = string(monitorState)
			provisionalLabel = websiteStateLabel(locale, monitorState)
		}
		title = fmt.Sprintf(
			webText(locale, "website.availability_provisional"),
			provisionalLabel,
			websiteAvailabilityCheckedAtLabel(locale, latestCheckedAt),
		)
	}
	return websiteMonitorAvailabilityBucketView{
		StartedAt: bucket.StartedAt, EndedAt: endedAt, State: bucket.State,
		Tone: tone, Provisional: bucket.Provisional, Title: title,
		TotalChecks: bucket.TotalChecks, SuccessfulChecks: bucket.SuccessfulChecks,
		FailedChecks: bucket.FailedChecks,
	}
}

func websiteAvailabilityCheckedAtLabel(locale webLocale, value time.Time) string {
	if value.IsZero() {
		return webText(locale, "website.never_checked")
	}
	if locale == localeSimplifiedChinese {
		return value.Local().Format("2006年01月02日 15:04:05")
	}
	return value.Local().Format("Jan 2, 2006 15:04:05")
}

func websiteStateLabel(locale webLocale, state websitemonitor.State) string {
	switch state {
	case websitemonitor.StateUp:
		return webText(locale, "website.state.up")
	case websitemonitor.StateVerifying:
		return webText(locale, "website.state.verifying")
	case websitemonitor.StateDown:
		return webText(locale, "website.state.down")
	case websitemonitor.StatePaused:
		return webText(locale, "website.state.paused")
	default:
		return webText(locale, "website.state.pending")
	}
}

func websiteScopeLabel(locale webLocale, scope websitemonitor.Scope) string {
	if scope == websitemonitor.ScopeLocal {
		return webText(locale, "website.scope.local")
	}
	return webText(locale, "website.scope.external")
}

func websiteKindLabel(locale webLocale, kind websitemonitor.Kind) string {
	if kind == websitemonitor.KindWebSocket {
		return "WebSocket"
	}
	return "HTTP / HTTPS"
}

func websiteSecuritySummary(locale webLocale, monitor websitemonitor.Monitor) (string, string, string) {
	if strings.HasPrefix(monitor.Config.URL, "http://") || strings.HasPrefix(monitor.Config.URL, "ws://") {
		return "neutral", webText(locale, "website.security.no_tls_title"), webText(locale, "website.security.no_tls_description")
	}
	if monitor.Latest.CheckedAt.IsZero() || monitor.Latest.Certificate.NotAfter.IsZero() {
		return "neutral", webText(locale, "website.security.unavailable_title"), webText(locale, "website.security.unavailable_description")
	}
	if !monitor.Latest.Certificate.NotAfter.After(monitor.Latest.CheckedAt) {
		if locale == localeSimplifiedChinese {
			return "danger", "证书已过期", "最近一次连接读取到的证书已经超过有效期。"
		}
		return "danger", "Certificate expired", "The certificate read during the latest connection is past its validity period."
	}
	if monitor.Config.SkipTLSVerification {
		return "warning", webText(locale, "website.security.verification_off_title"), webText(locale, "website.security.verification_off_description")
	}
	if monitor.Latest.Certificate.DaysRemaining <= 14 {
		if monitor.Latest.Certificate.DaysRemaining <= 0 {
			if locale == localeSimplifiedChinese {
				return "warning", "证书将在 24 小时内到期", "请尽快更新证书，避免检查因 TLS 验证失败转为故障。"
			}
			return "warning", "Certificate expires within 24 hours", "Renew the certificate soon to avoid a TLS verification failure."
		}
		expiryKey := "website.security.expires_in_days"
		if monitor.Latest.Certificate.DaysRemaining == 1 {
			expiryKey = "website.security.expires_in_day"
		}
		return "warning",
			fmt.Sprintf(webText(locale, expiryKey), monitor.Latest.Certificate.DaysRemaining),
			webText(locale, "website.security.expiring_description")
	}
	return "success", webText(locale, "website.security.valid_title"), webText(locale, "website.security.valid_description")
}

func websiteDurationLabel(locale webLocale, duration time.Duration) string {
	seconds := int64(duration / time.Second)
	if seconds%60 == 0 {
		minutes := seconds / 60
		if locale == localeSimplifiedChinese {
			return fmt.Sprintf("%d 分钟", minutes)
		}
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	if locale == localeSimplifiedChinese {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds == 1 {
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func websiteElapsedLabel(locale webLocale, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	totalSeconds := int64(duration / time.Second)
	days := totalSeconds / 86400
	hours := totalSeconds % 86400 / 3600
	minutes := totalSeconds % 3600 / 60
	seconds := totalSeconds % 60
	if locale == localeSimplifiedChinese {
		switch {
		case days > 0:
			return fmt.Sprintf("%d 天 %d 小时", days, hours)
		case hours > 0:
			return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
		case minutes > 0:
			return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
		default:
			return fmt.Sprintf("%d 秒", seconds)
		}
	}
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func websiteAvailabilityLabel(locale webLocale, availability websitemonitor.Availability) string {
	if locale == localeSimplifiedChinese {
		switch availability {
		case websitemonitor.AvailabilityUp:
			return "正常"
		case websitemonitor.AvailabilityDown:
			return "故障"
		default:
			return "无检查"
		}
	}
	switch availability {
	case websitemonitor.AvailabilityUp:
		return "Up"
	case websitemonitor.AvailabilityDown:
		return "Down"
	default:
		return "No check"
	}
}

func localizeWebsiteMonitorSummary(locale webLocale, summary string) string {
	if locale == localeSimplifiedChinese || summary == "" {
		return summary
	}
	if strings.HasPrefix(summary, "网站返回 HTTP ") {
		return "Website returned HTTP " + strings.TrimPrefix(summary, "网站返回 HTTP ")
	}
	if strings.HasPrefix(summary, "网站返回了不符合预期的 HTTP ") {
		return "Website returned unexpected HTTP " + strings.TrimPrefix(summary, "网站返回了不符合预期的 HTTP ")
	}
	translations := map[string]string{
		"暂不支持这种检查方式":                "This check type is not supported",
		"无法创建网站请求":                  "Unable to create the website request",
		"网站请求未完成":                   "The website request did not complete",
		"读取网站响应时连接中断":               "The connection closed while reading the website response",
		"在读取上限内没有找到要求的返回内容":         "The required content was not found within the response read limit",
		"网站响应中没有要求的内容":              "The website response did not contain the required content",
		"WebSocket 连接未建立":           "The WebSocket connection was not established",
		"WebSocket 连接已建立":           "The WebSocket connection was established",
		"WebSocket 应用消息未发送":         "The WebSocket application message was not sent",
		"等待 WebSocket 应用消息时未收到有效响应": "No valid response arrived while waiting for a WebSocket application message",
		"已收到 WebSocket 应用消息":        "A WebSocket application message was received",
		"已收到匹配的 WebSocket 应用消息":     "A matching WebSocket application message was received",
		"Ping 载荷无效":                 "The Ping payload is invalid",
		"Ping 控制帧未发送":               "The Ping control frame was not sent",
		"Pong 载荷与 Ping 完全一致":        "The Pong payload exactly matched the Ping payload",
		"等待匹配的 Pong 时连接已结束":         "The connection closed while waiting for a matching Pong",
		"等待匹配的 Pong 超时":             "Timed out waiting for a matching Pong",
	}
	if translated, ok := translations[summary]; ok {
		return translated
	}
	return summary
}
