package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/websitemonitor"
)

type websiteMonitorPageView struct {
	ID                  string
	Name                string
	URL                 string
	Scope               websitemonitor.Scope
	ScopeLabel          string
	Kind                websitemonitor.Kind
	KindLabel           string
	MethodLabel         string
	State               websitemonitor.State
	StateLabel          string
	LatestLabel         string
	LatestSummary       string
	LatestTechnical     string
	LatencyLabel        string
	LastCheckedAt       time.Time
	CheckedToken        int64
	FrequencyLabel      string
	TimeoutLabel        string
	TLSAttention        bool
	SecurityTone        string
	SecurityTitle       string
	SecurityDescription string
	Certificate         websitemonitor.Certificate
	SortOrder           int
	Availability        []string
}

type websiteMonitorCounts struct {
	Up, Verifying, Down, Paused int
}

type websiteMonitorListView struct {
	Monitors   []websiteMonitorPageView
	Alerts     []websiteMonitorPageView
	Counts     websiteMonitorCounts
	Locale     webLocale
	State      websitemonitor.State
	Scope      websitemonitor.Scope
	CSRFToken  string
	Total      int
	NeedsCare  int
	HasFilters bool
	HasAny     bool
	Reorder    bool
	DataURL    string
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
}

type websiteMonitorNginxView struct {
	ConfigPath string
	Preview    *websitemonitor.NginxPreview
	Error      string
	Locale     webLocale
	CSRFToken  string
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
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{State: state, Scope: scope})
	if err != nil {
		http.Error(response, webText(locale, "website.error.read_monitors")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	all, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, webText(locale, "website.error.summarize_monitors")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	view := websiteMonitorListView{
		Locale: locale, State: state, Scope: scope, CSRFToken: current.csrfToken,
		Total: len(monitors), HasFilters: state != "" || scope != "",
		HasAny: len(all) > 0, Reorder: request.URL.Query().Get("reorder") == "1",
		DataURL: "/monitor/websites/data",
	}
	query := request.URL.Query()
	query.Del("reorder")
	if encoded := query.Encode(); encoded != "" {
		view.DataURL += "?" + encoded
	}
	for _, monitor := range monitors {
		view.Monitors = append(view.Monitors, a.newWebsiteMonitorPageView(request.Context(), monitor, locale))
	}
	for _, monitor := range all {
		item := a.newWebsiteMonitorPageView(request.Context(), monitor, locale)
		switch monitor.State {
		case websitemonitor.StateUp:
			view.Counts.Up++
		case websitemonitor.StateVerifying:
			view.Counts.Verifying++
			view.Alerts = append(view.Alerts, item)
		case websitemonitor.StateDown:
			view.Counts.Down++
			view.Alerts = append(view.Alerts, item)
		case websitemonitor.StatePaused:
			view.Counts.Paused++
		}
	}
	view.NeedsCare = view.Counts.Verifying + view.Counts.Down
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = websiteMonitorListTemplate.Execute(response, view)
}

func (a *App) websiteMonitorData(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	state := websitemonitor.State(request.URL.Query().Get("state"))
	scope := websitemonitor.Scope(request.URL.Query().Get("scope"))
	if !validWebsiteStateFilter(state) || !validWebsiteScopeFilter(scope) {
		http.Error(response, webText(locale, "website.error.invalid_filter"), http.StatusBadRequest)
		return
	}
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{State: state, Scope: scope})
	if err != nil {
		http.Error(response, webText(locale, "website.error.read_monitors")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(struct {
		Monitors []websiteMonitorPageView `json:"monitors"`
	}{Monitors: a.websiteMonitorPageViews(request.Context(), monitors, locale)})
}

func (a *App) websiteMonitorPageViews(ctx context.Context, monitors []websitemonitor.Monitor, locale webLocale) []websiteMonitorPageView {
	result := make([]websiteMonitorPageView, 0, len(monitors))
	for _, monitor := range monitors {
		result = append(result, a.newWebsiteMonitorPageView(ctx, monitor, locale))
	}
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
	renderWebsiteMonitorNginx(response, http.StatusOK, websiteMonitorNginxView{
		Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
	})
}

func (a *App) scanWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	configPath := strings.TrimSpace(request.FormValue("config_path"))
	preview, err := a.websiteMonitor.ScanNginx(request.Context(), websitemonitor.NginxScanRequest{ConfigPath: configPath})
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	if err != nil {
		renderWebsiteMonitorNginx(response, http.StatusUnprocessableEntity, websiteMonitorNginxView{
			ConfigPath: configPath, Error: err.Error(), Locale: locale, CSRFToken: current.csrfToken,
		})
		return
	}
	renderWebsiteMonitorNginx(response, http.StatusOK, websiteMonitorNginxView{
		ConfigPath: configPath, Preview: &preview, Locale: locale, CSRFToken: current.csrfToken,
	})
}

func (a *App) importWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
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
		current := request.Context().Value(sessionContextKey).(session)
		preview, _ := a.websiteMonitor.ScanNginx(request.Context(), websitemonitor.NginxScanRequest{ConfigPath: configPath})
		renderWebsiteMonitorNginx(response, http.StatusUnprocessableEntity, websiteMonitorNginxView{
			ConfigPath: configPath, Preview: &preview, Error: err.Error(),
			Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
		})
		return
	}
	a.recordAudit("import_nginx_website_monitors", fmt.Sprintf("%d monitors", len(imported)), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func (a *App) createWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "website.error.csrf"), http.StatusForbidden)
		return
	}
	config, fieldErrors := websiteMonitorConfigFromRequest(request)
	if len(fieldErrors) > 0 {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, resolveWebLocale(request), current.csrfToken, "", false))
		return
	}
	monitor, err := a.websiteMonitor.Create(request.Context(), config)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, resolveWebLocale(request), current.csrfToken, "", false))
		return
	}
	a.recordAudit("create_website_monitor", monitor.Config.Name, "succeeded", request.RemoteAddr)
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
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, resolveWebLocale(request), current.csrfToken, id, true))
		return
	}
	monitor, err := a.websiteMonitor.Update(request.Context(), id, config)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, resolveWebLocale(request), current.csrfToken, id, true))
		return
	}
	a.recordAudit("update_website_monitor", monitor.Config.Name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+monitor.ID, http.StatusSeeOther)
}

func (a *App) websiteMonitorDetail(response http.ResponseWriter, request *http.Request) {
	monitor, err := a.websiteMonitor.Get(request.Context(), request.PathValue("id"))
	if err != nil || monitor.DeletedAt != nil {
		if errors.Is(err, context.Canceled) {
			http.Error(response, webText(resolveWebLocale(request), "website.error.request_canceled"), http.StatusRequestTimeout)
			return
		}
		http.NotFound(response, request)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	incidents, _ := a.websiteMonitor.Incidents(request.Context(), monitor.ID)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = websiteMonitorDetailTemplate.Execute(response, struct {
		Monitor   websiteMonitorPageView
		Raw       websitemonitor.Monitor
		Incidents []websitemonitor.Incident
		Locale    webLocale
		CSRFToken string
	}{
		Monitor: a.newWebsiteMonitorPageView(request.Context(), monitor, locale), Raw: monitor,
		Incidents: incidents, Locale: locale, CSRFToken: current.csrfToken,
	})
}

func (a *App) websiteMonitorDetailData(response http.ResponseWriter, request *http.Request) {
	monitor, err := a.websiteMonitor.Get(request.Context(), request.PathValue("id"))
	if err != nil || monitor.DeletedAt != nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(a.newWebsiteMonitorPageView(
		request.Context(), monitor, resolveWebLocale(request),
	))
}

func (a *App) checkWebsiteMonitorNow(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.CheckNow(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, webText(locale, "website.error.check_now")+": "+err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) pauseWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Pause(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, webText(locale, "website.error.pause")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("pause_website_monitor", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) resumeWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	if !validSessionCSRF(request) {
		http.Error(response, webText(locale, "website.error.csrf"), http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Resume(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, webText(locale, "website.error.resume")+": "+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("resume_website_monitor", request.PathValue("id"), "succeeded", request.RemoteAddr)
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
	a.recordAudit("reorder_website_monitors", request.PathValue("id"), "succeeded", request.RemoteAddr)
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
	a.recordAudit("reorder_website_monitors", fmt.Sprintf("%d monitors", len(request.Form["id"])), "succeeded", request.RemoteAddr)
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
	a.recordAudit("delete_website_monitor", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func renderWebsiteMonitorForm(response http.ResponseWriter, status int, view websiteMonitorFormView) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = websiteMonitorFormTemplate.Execute(response, view)
}

func newWebsiteMonitorFormView(config websitemonitor.Config, fieldErrors map[string]string, locale webLocale, csrfToken, id string, editing bool) websiteMonitorFormView {
	statuses := make([]string, 0, len(config.ExpectedStatuses))
	for _, status := range config.ExpectedStatuses {
		statuses = append(statuses, strconv.Itoa(status))
	}
	return websiteMonitorFormView{
		Config:               config,
		Errors:               fieldErrors,
		Locale:               locale,
		CSRFToken:            csrfToken,
		Editing:              editing,
		ID:                   id,
		FrequencySeconds:     int64(config.Frequency / time.Second),
		TimeoutSeconds:       int64(config.Timeout / time.Second),
		ExpectedStatusesText: strings.Join(statuses, ", "),
	}
}

func renderWebsiteMonitorNginx(response http.ResponseWriter, status int, view websiteMonitorNginxView) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = websiteMonitorNginxTemplate.Execute(response, view)
}

func websiteMonitorConfigFromRequest(request *http.Request) (websitemonitor.Config, map[string]string) {
	_ = request.ParseForm()
	locale := resolveWebLocale(request)
	config := websitemonitor.Config{
		Name:                strings.TrimSpace(request.FormValue("name")),
		Scope:               websitemonitor.Scope(request.FormValue("scope")),
		Kind:                websitemonitor.Kind(request.FormValue("kind")),
		URL:                 strings.TrimSpace(request.FormValue("url")),
		DialHost:            strings.TrimSpace(request.FormValue("dial_host")),
		SkipTLSVerification: request.FormValue("verify_tls") != "1",
		Source:              "manual",
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
	errors := map[string]string{}
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
		for _, raw := range strings.FieldsFunc(request.FormValue("expected_statuses"), func(value rune) bool {
			return value == ',' || value == '，' || value == ' '
		}) {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 100 || value > 599 {
				errors["expected_statuses"] = webText(locale, "website.error.status_range")
				break
			}
			config.ExpectedStatuses = append(config.ExpectedStatuses, value)
		}
		if len(config.ExpectedStatuses) == 0 && errors["expected_statuses"] == "" {
			errors["expected_statuses"] = webText(locale, "website.error.status_required")
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
	view.TLSAttention = monitor.Latest.Certificate.DaysRemaining > 0 &&
		monitor.Latest.Certificate.DaysRemaining <= 14
	history, err := a.websiteMonitor.Availability24h(ctx, monitor.ID)
	if err != nil || len(history) != 48 {
		history = make([]websitemonitor.Availability, 48)
	}
	view.Availability = make([]string, len(history))
	for index, bucket := range history {
		if bucket == "" {
			bucket = websitemonitor.AvailabilityGap
		}
		view.Availability[index] = string(bucket)
	}
	return view
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
	if monitor.Config.SkipTLSVerification {
		return "warning", webText(locale, "website.security.verification_off_title"), webText(locale, "website.security.verification_off_description")
	}
	if monitor.Latest.Certificate.DaysRemaining <= 14 {
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
