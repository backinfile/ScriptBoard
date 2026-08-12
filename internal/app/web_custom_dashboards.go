package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"scriptboard/internal/customdashboard"
	"scriptboard/internal/registrymonitor"
	"scriptboard/internal/websitemonitor"
)

type customDashboardPageView struct {
	Locale                                      webLocale
	CSRFToken                                   string
	DashboardUpdatedLabel                       string
	ImportError                                 string
	Dashboards                                  []customdashboard.Dashboard
	Dashboard                                   customdashboard.Dashboard
	Cards                                       []customDashboardCardView
	WebsiteMonitors                             []websitemonitor.Monitor
	CanManage, PublicView, MonitorView, Reorder bool
}

type customDashboardCardView struct {
	customdashboard.Card
	ValueLabel, SecondaryLabel, HeadersText, Unit string
	QuotaProgressLabel                            string
	QuotaProgress                                 float64
	DisplayIndex                                  int
	CanMoveUp, CanMoveDown                        bool
	Websites                                      []customDashboardWebsiteView
	SelectedMonitorIDs                            map[string]bool
	InsecureSource                                bool
	RegistryEndpoint, RegistryImagesText          string
	RegistryAuthMode, RegistryUsername            string
	RegistryImageCount                            int
	RegistryImages                                []customDashboardRegistryImageView
}

type customDashboardRegistryImageView struct {
	Image, Tag, PushedLabel, Error string
	Stale                          bool
}

type customDashboardWebsiteView struct {
	Name, State, StateLabel, AvailabilityLabel, LatencyLabel, SSLLabel, SSLTone, CheckedLabel string
	Availability                                                                              []websiteMonitorAvailabilityBucketView
}

func (a *App) legacyCustomDashboardPage(response http.ResponseWriter, request *http.Request) {
	target := "/config/dashboards"
	if request.URL.RawQuery != "" {
		target += "?" + request.URL.RawQuery
	}
	http.Redirect(response, request, target, http.StatusSeeOther)
}

func (a *App) customDashboardPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	dashboards, err := a.customDashboards.ListDashboards(request.Context())
	if err != nil {
		http.Error(response, "无法读取自定义面板", http.StatusInternalServerError)
		return
	}
	for index := range dashboards {
		if complete, completeErr := a.customDashboards.GetDashboard(request.Context(), dashboards[index].ID); completeErr == nil {
			dashboards[index] = complete
		}
	}
	var dashboard customdashboard.Dashboard
	if id := strings.TrimSpace(request.URL.Query().Get("dashboard")); id != "" {
		dashboard, err = a.customDashboards.GetDashboard(request.Context(), id)
	} else if len(dashboards) > 0 {
		dashboard, err = a.customDashboards.GetDashboard(request.Context(), dashboards[0].ID)
	}
	if err != nil && err != sql.ErrNoRows {
		http.Error(response, "无法读取自定义面板", http.StatusInternalServerError)
		return
	}
	view := a.newCustomDashboardPageView(request, dashboard, false)
	view.Dashboards = dashboards
	view.CSRFToken = current.csrfToken
	view.CanManage = roleAllows(current.role, permissionManageOperations)
	view.Reorder = view.CanManage && request.URL.Query().Get("reorder") == "1"
	view.ImportError = customDashboardImportError(request.URL.Query().Get("import_error"))
	for index := range view.Cards {
		view.Cards[index].DisplayIndex = index + 1
		view.Cards[index].CanMoveUp = index > 0
		view.Cards[index].CanMoveDown = index < len(view.Cards)-1
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = customDashboardTemplate.Execute(response, view)
}

func (a *App) publicCustomDashboard(response http.ResponseWriter, request *http.Request) {
	dashboard, err := a.customDashboards.GetPublicDashboard(request.Context(), request.PathValue("slug"))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	view := a.newCustomDashboardPageView(request, dashboard, true)
	view.PublicView = true
	response.Header().Set("Cache-Control", "public, max-age=15")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = customDashboardTemplate.Execute(response, view)
}

func (a *App) customDashboardMonitorPage(response http.ResponseWriter, request *http.Request) {
	dashboard, err := a.customDashboards.GetDashboard(request.Context(), request.PathValue("id"))
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(response, request)
			return
		}
		http.Error(response, "无法读取自定义面板", http.StatusInternalServerError)
		return
	}
	view := a.newCustomDashboardPageView(request, dashboard, false)
	view.MonitorView = true
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = customDashboardTemplate.Execute(response, view)
}

func (a *App) newCustomDashboardPageView(request *http.Request, dashboard customdashboard.Dashboard, public bool) customDashboardPageView {
	view := customDashboardPageView{Locale: resolveWebLocale(request), Dashboard: dashboard, PublicView: public}
	if !dashboard.UpdatedAt.IsZero() {
		view.DashboardUpdatedLabel = dashboard.UpdatedAt.Local().Format("01-02 15:04")
	}
	if monitors, err := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{}); err == nil {
		view.WebsiteMonitors = monitors
	}
	for _, card := range dashboard.Cards {
		item := customDashboardCardView{Card: card, SelectedMonitorIDs: map[string]bool{}}
		var cardConfig struct {
			MonitorIDs []string `json:"monitorIds"`
			Unit       string   `json:"unit"`
		}
		_ = json.Unmarshal(card.Config, &cardConfig)
		var registryConfig registrymonitor.Config
		if card.Type == customdashboard.CardRegistry {
			_ = json.Unmarshal(card.Config, &registryConfig)
			item.RegistryEndpoint = registryConfig.Endpoint
			item.RegistryImagesText = strings.Join(registryConfig.Images, "\n")
			item.RegistryImageCount = len(registryConfig.Images)
			item.RegistryAuthMode = registryConfig.AuthMode
			item.RegistryUsername = registryConfig.Username
			item.InsecureSource = strings.HasPrefix(strings.ToLower(registryConfig.Endpoint), "http://")
			for _, image := range card.Snapshot.Images {
				imageView := customDashboardRegistryImageView{Image: image.Image, Tag: image.Tag, Error: image.Error, Stale: image.Stale}
				if public && imageView.Error != "" {
					imageView.Error = "刷新失败"
				}
				if imageView.Tag == "" {
					imageView.Tag = "—"
				}
				if image.PushTimeAvailable && !image.PushedAt.IsZero() {
					imageView.PushedLabel = image.PushedAt.Local().Format("2006-01-02 15:04")
				} else {
					imageView.PushedLabel = "仓库未提供"
				}
				item.RegistryImages = append(item.RegistryImages, imageView)
			}
		}
		item.Unit = strings.TrimSpace(cardConfig.Unit)
		headerNames := make([]string, 0, len(card.Headers))
		for name := range card.Headers {
			headerNames = append(headerNames, name)
		}
		sort.Strings(headerNames)
		var headerLines []string
		for _, name := range headerNames {
			headerLines = append(headerLines, name+": "+card.Headers[name])
		}
		item.HeadersText = strings.Join(headerLines, "\n")
		if card.Type != customdashboard.CardRegistry {
			item.InsecureSource = strings.HasPrefix(strings.ToLower(card.SourceURL), "http://")
		}
		item.ValueLabel = formatDashboardValue(card.Snapshot.Value)
		item.SecondaryLabel = formatDashboardValue(card.Snapshot.Secondary)
		if card.Type == customdashboard.CardPercentage {
			item.SecondaryLabel = ""
		}
		item.QuotaProgress = card.Snapshot.Number
		if card.Type == customdashboard.CardQuota {
			used, usedOK := dashboardNumericValue(card.Snapshot.Value)
			remaining, remainingOK := dashboardNumericValue(card.Snapshot.Secondary)
			if usedOK && remainingOK && used+remaining > 0 {
				item.QuotaProgress = used / (used + remaining) * 100
			} else {
				item.QuotaProgress = 0
			}
		}
		if item.QuotaProgress < 0 {
			item.QuotaProgress = 0
		}
		if item.QuotaProgress > 100 {
			item.QuotaProgress = 100
		}
		item.QuotaProgressLabel = formatDashboardValue(item.QuotaProgress)
		if card.LastError != "" {
			item.ValueLabel = "0"
			if card.Type == customdashboard.CardQuota {
				item.SecondaryLabel = "0"
			} else {
				item.SecondaryLabel = ""
			}
			item.QuotaProgress = 0
			item.QuotaProgressLabel = "0"
		}
		if card.Type == customdashboard.CardWebsite {
			for _, id := range cardConfig.MonitorIDs {
				item.SelectedMonitorIDs[id] = true
			}
			item.Websites = a.customDashboardWebsiteCards(request, card)
		}
		view.Cards = append(view.Cards, item)
	}
	return view
}

func (a *App) customDashboardWebsiteCards(request *http.Request, card customdashboard.Card) []customDashboardWebsiteView {
	var config struct {
		MonitorIDs []string `json:"monitorIds"`
	}
	_ = json.Unmarshal(card.Config, &config)
	result := make([]customDashboardWebsiteView, 0, len(config.MonitorIDs))
	locale := resolveWebLocale(request)
	for _, id := range config.MonitorIDs {
		monitor, err := a.websiteMonitor.Get(request.Context(), id)
		if err != nil || monitor.DeletedAt != nil {
			continue
		}
		monitorView := a.newWebsiteMonitorPageView(request.Context(), monitor, locale)
		item := customDashboardWebsiteView{Name: monitor.Config.Name, State: string(monitor.State), StateLabel: monitorView.StateLabel, LatencyLabel: monitorView.LatencyLabel, AvailabilityLabel: "—", SSLLabel: "无 TLS 证书", SSLTone: "neutral", CheckedLabel: "尚未检查", Availability: monitorView.Availability}
		if !monitor.Latest.CheckedAt.IsZero() {
			item.LatencyLabel = fmt.Sprintf("%d ms", monitor.Latest.Latency.Milliseconds())
			item.CheckedLabel = monitor.Latest.CheckedAt.Local().Format("01-02 15:04")
		}
		if snapshot, detailErr := a.websiteMonitor.DetailSnapshot(request.Context(), id); detailErr == nil {
			item.AvailabilityLabel = fmt.Sprintf("%.2f%%", snapshot.AvailabilityPercent)
		}
		if !monitor.Latest.Certificate.NotAfter.IsZero() {
			days := monitor.Latest.Certificate.DaysRemaining
			item.SSLLabel = fmt.Sprintf("SSL %d 天", days)
			item.SSLTone = "success"
			if days <= 14 {
				item.SSLTone = "warning"
			}
			if days <= 0 {
				item.SSLTone = "danger"
			}
		}
		result = append(result, item)
	}
	return result
}

func (a *App) createCustomDashboard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	dashboard, err := a.customDashboards.CreateDashboard(request.Context(), customdashboard.DashboardInput{Name: request.FormValue("name"), Slug: request.FormValue("slug"), Public: false})
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "create_custom_dashboard", dashboard.ID, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+dashboard.ID, http.StatusSeeOther)
}
func (a *App) updateCustomDashboard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	current, err := a.customDashboards.GetDashboard(request.Context(), id)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	_ = request.ParseForm()
	public := current.Public
	if _, provided := request.Form["public"]; provided {
		public = request.FormValue("public") == "1"
	}
	_, err = a.customDashboards.UpdateDashboard(request.Context(), id, customdashboard.DashboardInput{Name: request.FormValue("name"), Slug: request.FormValue("slug"), Public: public})
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "update_custom_dashboard", id, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+id, http.StatusSeeOther)
}
func (a *App) deleteCustomDashboard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "请确认删除面板", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.customDashboards.DeleteDashboard(request.Context(), id); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_custom_dashboard", id, "succeeded")
	http.Redirect(response, request, "/config/dashboards", http.StatusSeeOther)
}

func (a *App) refreshCustomDashboard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	result := "succeeded"
	if err := a.customDashboards.RefreshDashboard(request.Context(), id); err != nil {
		result = "failed"
	}
	a.recordAuditForRequest(request, "refresh_custom_dashboard", id, result)
	http.Redirect(response, request, "/config/dashboards?dashboard="+url.QueryEscape(id)+"&refreshed=1", http.StatusSeeOther)
}

func (a *App) createCustomDashboardCard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	input, err := customDashboardCardInput(request, false)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if input.Type == customdashboard.CardKeyValue {
		http.Error(response, "不支持的卡片类型", http.StatusUnprocessableEntity)
		return
	}
	card, err := a.customDashboards.CreateCard(request.Context(), id, input)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if card.Type != customdashboard.CardWebsite {
		_, _ = a.customDashboards.RefreshCard(request.Context(), card.ID)
	}
	a.recordAuditForRequest(request, "create_custom_dashboard_card", card.ID, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+id, http.StatusSeeOther)
}
func (a *App) deleteCustomDashboardCard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "请确认删除卡片", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	dashboardID := request.FormValue("dashboard_id")
	if err := a.customDashboards.DeleteCard(request.Context(), id); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_custom_dashboard_card", id, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+dashboardID, http.StatusSeeOther)
}
func (a *App) refreshCustomDashboardCard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	dashboardID := request.FormValue("dashboard_id")
	if _, err := a.customDashboards.RefreshCard(request.Context(), id); err != nil {
		a.recordAuditForRequest(request, "refresh_custom_dashboard_card", id, "failed")
	}
	http.Redirect(response, request, "/config/dashboards?dashboard="+dashboardID, http.StatusSeeOther)
}

func (a *App) moveCustomDashboardCard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	direction := 0
	switch request.FormValue("direction") {
	case "up":
		direction = -1
	case "down":
		direction = 1
	}
	dashboardID, err := a.customDashboards.MoveCard(request.Context(), request.PathValue("id"), direction)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "move_custom_dashboard_card", request.PathValue("id"), "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+dashboardID+"&reorder=1", http.StatusSeeOther)
}

func (a *App) updateCustomDashboardCard(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	input, err := customDashboardCardInput(request, true)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	card, err := a.customDashboards.UpdateCard(request.Context(), id, input)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if card.Type != customdashboard.CardWebsite {
		_, _ = a.customDashboards.RefreshCard(request.Context(), card.ID)
	}
	a.recordAuditForRequest(request, "update_custom_dashboard_card", id, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+card.DashboardID, http.StatusSeeOther)
}

func customDashboardCardInput(request *http.Request, preserveRegistryPassword bool) (customdashboard.CardInput, error) {
	_ = request.ParseForm()
	refresh, _ := strconv.Atoi(request.FormValue("refresh_seconds"))
	headers := map[string]string{}
	for _, line := range strings.Split(request.FormValue("headers"), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) != "" {
			headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	cardType := customdashboard.CardType(request.FormValue("type"))
	config := json.RawMessage(`{}`)
	if cardType == customdashboard.CardWebsite {
		encoded, err := json.Marshal(map[string]any{"monitorIds": request.Form["monitor_id"]})
		if err != nil {
			return customdashboard.CardInput{}, err
		}
		config = encoded
	} else if cardType == customdashboard.CardRegistry {
		if request.FormValue("registry_auth_mode") == "basic" && strings.TrimSpace(request.FormValue("registry_username")) == "" {
			return customdashboard.CardInput{}, errors.New("用户名不能为空")
		}
		if request.FormValue("registry_auth_mode") == "basic" && !preserveRegistryPassword && request.FormValue("registry_password") == "" {
			return customdashboard.CardInput{}, errors.New("密码或访问令牌不能为空")
		}
		images := strings.FieldsFunc(request.FormValue("registry_images"), func(character rune) bool { return character == '\n' || character == '\r' || character == ',' })
		encoded, err := json.Marshal(registrymonitor.Config{
			Endpoint: request.FormValue("registry_endpoint"), Images: images,
			AuthMode: request.FormValue("registry_auth_mode"), Username: request.FormValue("registry_username"),
		})
		if err != nil {
			return customdashboard.CardInput{}, err
		}
		config = encoded
	} else if cardType == customdashboard.CardNumber || cardType == customdashboard.CardQuota {
		unitRunes := []rune(strings.TrimSpace(request.FormValue("unit")))
		if len(unitRunes) > 16 {
			unitRunes = unitRunes[:16]
		}
		encoded, err := json.Marshal(map[string]string{"unit": string(unitRunes)})
		if err != nil {
			return customdashboard.CardInput{}, err
		}
		config = encoded
	}
	return customdashboard.CardInput{Name: request.FormValue("name"), Type: cardType, SourceURL: request.FormValue("source_url"), Headers: headers, ValuePath: request.FormValue("value_path"), SecondaryPath: request.FormValue("secondary_path"), Config: config, RefreshSeconds: refresh, RegistryPassword: request.FormValue("registry_password"), PreserveRegistryPassword: preserveRegistryPassword && request.FormValue("registry_password") == ""}, nil
}

func formatDashboardValue(value any) string {
	if value == nil {
		return "—"
	}
	switch v := value.(type) {
	case float64:
		rounded := math.Round(v*100) / 100
		if rounded == 0 {
			return "0"
		}
		return strconv.FormatFloat(rounded, 'f', -1, 64)
	case string:
		return v
	default:
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
}

func dashboardNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}
