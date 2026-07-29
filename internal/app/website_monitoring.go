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
	CSRFToken  string
}

func (a *App) websiteMonitorList(response http.ResponseWriter, request *http.Request) {
	state := websitemonitor.State(request.URL.Query().Get("state"))
	if !validWebsiteStateFilter(state) {
		http.Error(response, "网站状态筛选无效", http.StatusBadRequest)
		return
	}
	scope := websitemonitor.Scope(request.URL.Query().Get("scope"))
	if !validWebsiteScopeFilter(scope) {
		http.Error(response, "网站范围筛选无效", http.StatusBadRequest)
		return
	}
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{State: state, Scope: scope})
	if err != nil {
		http.Error(response, "无法读取网站监控："+err.Error(), http.StatusInternalServerError)
		return
	}
	all, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	if err != nil {
		http.Error(response, "无法汇总网站监控："+err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	view := websiteMonitorListView{
		State: state, Scope: scope, CSRFToken: current.csrfToken,
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
		view.Monitors = append(view.Monitors, a.newWebsiteMonitorPageView(request.Context(), monitor))
	}
	for _, monitor := range all {
		item := a.newWebsiteMonitorPageView(request.Context(), monitor)
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
	state := websitemonitor.State(request.URL.Query().Get("state"))
	scope := websitemonitor.Scope(request.URL.Query().Get("scope"))
	if !validWebsiteStateFilter(state) || !validWebsiteScopeFilter(scope) {
		http.Error(response, "网站筛选无效", http.StatusBadRequest)
		return
	}
	monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{State: state, Scope: scope})
	if err != nil {
		http.Error(response, "无法读取网站监控："+err.Error(), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(struct {
		Monitors []websiteMonitorPageView `json:"monitors"`
	}{Monitors: a.websiteMonitorPageViews(request.Context(), monitors)})
}

func (a *App) websiteMonitorPageViews(ctx context.Context, monitors []websitemonitor.Monitor) []websiteMonitorPageView {
	result := make([]websiteMonitorPageView, 0, len(monitors))
	for _, monitor := range monitors {
		result = append(result, a.newWebsiteMonitorPageView(ctx, monitor))
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
	renderWebsiteMonitorForm(response, http.StatusOK, newWebsiteMonitorFormView(config, nil, current.csrfToken, "", false))
}

func (a *App) websiteMonitorEditTask(response http.ResponseWriter, request *http.Request) {
	monitor, err := a.websiteMonitor.Get(request.Context(), request.PathValue("id"))
	if err != nil || monitor.DeletedAt != nil {
		http.NotFound(response, request)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	renderWebsiteMonitorForm(response, http.StatusOK,
		newWebsiteMonitorFormView(monitor.Config, nil, current.csrfToken, monitor.ID, true))
}

func (a *App) websiteMonitorNginxTask(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	renderWebsiteMonitorNginx(response, http.StatusOK, websiteMonitorNginxView{CSRFToken: current.csrfToken})
}

func (a *App) scanWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	configPath := strings.TrimSpace(request.FormValue("config_path"))
	preview, err := a.websiteMonitor.ScanNginx(request.Context(), websitemonitor.NginxScanRequest{ConfigPath: configPath})
	current := request.Context().Value(sessionContextKey).(session)
	if err != nil {
		renderWebsiteMonitorNginx(response, http.StatusUnprocessableEntity, websiteMonitorNginxView{
			ConfigPath: configPath, Error: err.Error(), CSRFToken: current.csrfToken,
		})
		return
	}
	renderWebsiteMonitorNginx(response, http.StatusOK, websiteMonitorNginxView{
		ConfigPath: configPath, Preview: &preview, CSRFToken: current.csrfToken,
	})
}

func (a *App) importWebsiteMonitorNginx(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
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
			ConfigPath: configPath, Preview: &preview, Error: err.Error(), CSRFToken: current.csrfToken,
		})
		return
	}
	a.recordAudit("import_nginx_website_monitors", fmt.Sprintf("%d monitors", len(imported)), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites", http.StatusSeeOther)
}

func (a *App) createWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	config, fieldErrors := websiteMonitorConfigFromRequest(request)
	if len(fieldErrors) > 0 {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, current.csrfToken, "", false))
		return
	}
	monitor, err := a.websiteMonitor.Create(request.Context(), config)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, current.csrfToken, "", false))
		return
	}
	a.recordAudit("create_website_monitor", monitor.Config.Name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+monitor.ID, http.StatusSeeOther)
}

func (a *App) updateWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	config, fieldErrors := websiteMonitorConfigFromRequest(request)
	if len(fieldErrors) > 0 {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, fieldErrors, current.csrfToken, id, true))
		return
	}
	monitor, err := a.websiteMonitor.Update(request.Context(), id, config)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		renderWebsiteMonitorForm(response, http.StatusUnprocessableEntity,
			newWebsiteMonitorFormView(config, map[string]string{"form": err.Error()}, current.csrfToken, id, true))
		return
	}
	a.recordAudit("update_website_monitor", monitor.Config.Name, "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+monitor.ID, http.StatusSeeOther)
}

func (a *App) websiteMonitorDetail(response http.ResponseWriter, request *http.Request) {
	monitor, err := a.websiteMonitor.Get(request.Context(), request.PathValue("id"))
	if err != nil || monitor.DeletedAt != nil {
		if errors.Is(err, context.Canceled) {
			http.Error(response, "网站监控请求已取消", http.StatusRequestTimeout)
			return
		}
		http.NotFound(response, request)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	incidents, _ := a.websiteMonitor.Incidents(request.Context(), monitor.ID)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = websiteMonitorDetailTemplate.Execute(response, struct {
		Monitor   websiteMonitorPageView
		Raw       websitemonitor.Monitor
		Incidents []websitemonitor.Incident
		CSRFToken string
	}{
		Monitor: a.newWebsiteMonitorPageView(request.Context(), monitor), Raw: monitor,
		Incidents: incidents, CSRFToken: current.csrfToken,
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
	_ = json.NewEncoder(response).Encode(a.newWebsiteMonitorPageView(request.Context(), monitor))
}

func (a *App) checkWebsiteMonitorNow(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.CheckNow(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "无法立即检查网站："+err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) pauseWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Pause(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "无法暂停网站监控："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("pause_website_monitor", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) resumeWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Resume(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "无法恢复网站监控："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("resume_website_monitor", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites/"+request.PathValue("id"), http.StatusSeeOther)
}

func (a *App) moveWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
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
		http.Error(response, "无法调整网站顺序："+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAudit("reorder_website_monitors", request.PathValue("id"), "succeeded", request.RemoteAddr)
	http.Redirect(response, request, "/monitor/websites?reorder=1", http.StatusSeeOther)
}

func (a *App) reorderWebsiteMonitors(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "网站顺序无效", http.StatusBadRequest)
		return
	}
	if err := a.websiteMonitor.Reorder(request.Context(), request.Form["id"]); err != nil {
		http.Error(response, "无法保存网站顺序："+err.Error(), http.StatusConflict)
		return
	}
	a.recordAudit("reorder_website_monitors", fmt.Sprintf("%d monitors", len(request.Form["id"])), "succeeded", request.RemoteAddr)
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) deleteWebsiteMonitor(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除网站监控需要明确确认", http.StatusForbidden)
		return
	}
	if err := a.websiteMonitor.Delete(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, "无法删除网站监控："+err.Error(), http.StatusConflict)
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

func newWebsiteMonitorFormView(config websitemonitor.Config, fieldErrors map[string]string, csrfToken, id string, editing bool) websiteMonitorFormView {
	statuses := make([]string, 0, len(config.ExpectedStatuses))
	for _, status := range config.ExpectedStatuses {
		statuses = append(statuses, strconv.Itoa(status))
	}
	return websiteMonitorFormView{
		Config:               config,
		Errors:               fieldErrors,
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
			errors["frequency_seconds"] = "检查频率必须是 30 秒、1 分钟、5 分钟或 15 分钟"
		}
	} else {
		errors["frequency_seconds"] = "请选择有效的检查频率"
	}
	if value, err := strconv.Atoi(request.FormValue("timeout_seconds")); err == nil {
		config.Timeout = time.Duration(value) * time.Second
		if value != 3 && value != 5 && value != 10 && value != 30 {
			errors["timeout_seconds"] = "最长等待必须是 3、5、10 或 30 秒"
		}
	} else {
		errors["timeout_seconds"] = "请选择有效的最长等待时间"
	}
	if config.Kind == websitemonitor.KindHTTP && config.HTTPSuccessMode == websitemonitor.HTTPSuccessExact {
		for _, raw := range strings.FieldsFunc(request.FormValue("expected_statuses"), func(value rune) bool {
			return value == ',' || value == '，' || value == ' '
		}) {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 100 || value > 599 {
				errors["expected_statuses"] = "状态码必须是 100 到 599 的数字"
				break
			}
			config.ExpectedStatuses = append(config.ExpectedStatuses, value)
		}
		if len(config.ExpectedStatuses) == 0 && errors["expected_statuses"] == "" {
			errors["expected_statuses"] = "请至少填写一个状态码"
		}
	}
	if config.Kind == websitemonitor.KindWebSocket &&
		config.SendType == websitemonitor.MessageBinary &&
		config.SendPayload != "" {
		if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.SendPayload)); err != nil {
			errors["send_payload"] = "二进制发送内容必须是有效的 Base64"
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

func (a *App) newWebsiteMonitorPageView(ctx context.Context, monitor websitemonitor.Monitor) websiteMonitorPageView {
	view := websiteMonitorPageView{
		ID: monitor.ID, Name: monitor.Config.Name, URL: monitor.Config.URL,
		Scope: monitor.Config.Scope, ScopeLabel: websiteScopeLabel(monitor.Config.Scope),
		Kind: monitor.Config.Kind, KindLabel: websiteKindLabel(monitor.Config.Kind),
		MethodLabel: monitor.Config.HTTPMethod, State: monitor.State,
		StateLabel: websiteStateLabel(monitor.State), LatestSummary: monitor.Latest.Summary,
		LatestTechnical: monitor.Latest.TechnicalError, LastCheckedAt: monitor.Latest.CheckedAt,
		FrequencyLabel: monitor.Config.Frequency.String(), TimeoutLabel: monitor.Config.Timeout.String(),
		Certificate: monitor.Latest.Certificate, SortOrder: monitor.SortOrder,
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
		view.LatestLabel = monitor.Latest.Summary
	} else {
		view.LatestLabel = "等待第一次检查"
	}
	if monitor.Latest.Latency > 0 {
		view.LatencyLabel = fmt.Sprintf("%d ms", monitor.Latest.Latency.Milliseconds())
	} else {
		view.LatencyLabel = "—"
	}
	view.SecurityTone, view.SecurityTitle, view.SecurityDescription = websiteSecuritySummary(monitor)
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

func websiteStateLabel(state websitemonitor.State) string {
	switch state {
	case websitemonitor.StateUp:
		return "正常"
	case websitemonitor.StateVerifying:
		return "复核中"
	case websitemonitor.StateDown:
		return "故障"
	case websitemonitor.StatePaused:
		return "已暂停"
	default:
		return "等待检查"
	}
}

func websiteScopeLabel(scope websitemonitor.Scope) string {
	if scope == websitemonitor.ScopeLocal {
		return "本机"
	}
	return "外部"
}

func websiteKindLabel(kind websitemonitor.Kind) string {
	if kind == websitemonitor.KindWebSocket {
		return "WebSocket"
	}
	return "HTTP / HTTPS"
}

func websiteSecuritySummary(monitor websitemonitor.Monitor) (string, string, string) {
	if strings.HasPrefix(monitor.Config.URL, "http://") || strings.HasPrefix(monitor.Config.URL, "ws://") {
		return "neutral", "此地址未使用 TLS", "连接不会提供证书信息，传输内容也不会由 TLS 加密。"
	}
	if monitor.Latest.CheckedAt.IsZero() || monitor.Latest.Certificate.NotAfter.IsZero() {
		return "neutral", "暂时无法读取证书", "连接成功后，ScriptBoard 会显示证书颁发机构、有效期和 TLS 版本。"
	}
	if monitor.Config.SkipTLSVerification {
		return "warning", "证书验证已关闭", "证书过期或域名不匹配时，这项检查仍可能显示正常。"
	}
	if monitor.Latest.Certificate.DaysRemaining <= 14 {
		return "warning", fmt.Sprintf("证书将在 %d 天后到期", monitor.Latest.Certificate.DaysRemaining), "网站目前仍可安全访问，请在到期前更新证书。"
	}
	return "success", "证书有效", "证书、域名和有效期检查均已通过。"
}
