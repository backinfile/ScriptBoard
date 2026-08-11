package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/customdashboard"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/websitemonitor"
)

const (
	customDashboardConfigFormat   = "scriptboard.custom-dashboard"
	customDashboardConfigVersion  = 1
	customDashboardImportMaxSize  = 2 << 20
	customDashboardImportMaxCards = 100
)

var customDashboardTransferSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type customDashboardConfigFile struct {
	Format     string                      `json:"format"`
	Version    int                         `json:"version"`
	ExportedAt time.Time                   `json:"exported_at"`
	Dashboard  customDashboardConfigRecord `json:"dashboard"`
}

type customDashboardConfigRecord struct {
	Name  string                            `json:"name"`
	Slug  string                            `json:"slug"`
	Cards []customDashboardCardConfigRecord `json:"cards"`
}

type customDashboardCardConfigRecord struct {
	Name            string                   `json:"name"`
	Type            customdashboard.CardType `json:"type"`
	SourceURL       string                   `json:"source_url,omitempty"`
	Headers         map[string]string        `json:"headers,omitempty"`
	ValuePath       string                   `json:"value_path,omitempty"`
	SecondaryPath   string                   `json:"secondary_path,omitempty"`
	Formula         string                   `json:"formula,omitempty"`
	Config          json.RawMessage          `json:"config,omitempty"`
	RefreshSeconds  int                      `json:"refresh_seconds"`
	WebsiteMonitors []string                 `json:"website_monitors,omitempty"`
}

func (a *App) exportCustomDashboard(response http.ResponseWriter, request *http.Request) {
	dashboard, err := a.customDashboards.GetDashboard(request.Context(), request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, "无法读取自定义面板", http.StatusInternalServerError)
		return
	}
	monitorNames := map[string]string{}
	if monitors, monitorErr := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{}); monitorErr == nil {
		for _, monitor := range monitors {
			monitorNames[monitor.ID] = monitor.Config.Name
		}
	}
	bundle := customDashboardConfigFile{
		Format: customDashboardConfigFormat, Version: customDashboardConfigVersion, ExportedAt: time.Now().UTC(),
		Dashboard: customDashboardConfigRecord{Name: dashboard.Name, Slug: dashboard.Slug},
	}
	selectedValues := request.URL.Query()["selection"]
	selected := make(map[string]struct{}, len(selectedValues))
	for _, id := range selectedValues {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		http.Error(response, "请至少选择一张卡片", http.StatusUnprocessableEntity)
		return
	}
	for _, card := range dashboard.Cards {
		if _, included := selected[card.ID]; !included {
			continue
		}
		delete(selected, card.ID)
		record := customDashboardCardConfigRecord{
			Name: card.Name, Type: card.Type, SourceURL: card.SourceURL, Headers: card.Headers,
			ValuePath: card.ValuePath, SecondaryPath: card.SecondaryPath, Formula: card.Formula,
			Config: append(json.RawMessage(nil), card.Config...), RefreshSeconds: card.RefreshSeconds,
		}
		if card.Type == customdashboard.CardWebsite {
			for _, id := range customDashboardMonitorIDs(card.Config) {
				if name := strings.TrimSpace(monitorNames[id]); name != "" {
					record.WebsiteMonitors = append(record.WebsiteMonitors, name)
				}
			}
		}
		bundle.Dashboard.Cards = append(bundle.Dashboard.Cards, record)
	}
	if len(selected) != 0 || len(bundle.Dashboard.Cards) == 0 {
		http.Error(response, "卡片选择无效，请刷新页面后重试", http.StatusUnprocessableEntity)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scriptboard-dashboard-%s.json"`, time.Now().Format("20060102-150405")))
	encoded, err := secretredaction.MarshalJSON(bundle)
	if err == nil {
		_, err = response.Write(append(encoded, '\n'))
	}
	if err == nil {
		a.recordAuditForRequest(request, "export_custom_dashboard", dashboard.ID, "succeeded")
	}
}

func (a *App) importCustomDashboard(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, customDashboardImportMaxSize+(1<<20))
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		code := "invalid"
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			code = "too_large"
		}
		a.redirectCustomDashboardImportError(response, request, code)
		return
	}
	defer request.MultipartForm.RemoveAll()
	if !validSessionCSRF(request) {
		http.Error(response, "页面已过期，请重试", http.StatusForbidden)
		return
	}
	file, header, err := request.FormFile("dashboard_file")
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "file_required")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, customDashboardImportMaxSize+1))
	if err != nil || len(raw) > customDashboardImportMaxSize {
		a.redirectCustomDashboardImportError(response, request, "too_large")
		return
	}
	if err := validateJSONConfigurationImport(header.Filename, header.Header.Get("Content-Type"), raw, customDashboardImportMaxSize); err != nil {
		a.redirectCustomDashboardImportError(response, request, "invalid")
		return
	}
	bundle, err := decodeCustomDashboardConfigFile(raw)
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "invalid")
		return
	}
	selectedCards, err := selectedCustomDashboardCards(bundle.Dashboard.Cards, request.Form)
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "selection_required")
		return
	}
	bundle.Dashboard.Cards = selectedCards
	dashboards, err := a.customDashboards.ListDashboards(request.Context())
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "failed")
		return
	}
	slug := availableCustomDashboardSlug(dashboards, bundle.Dashboard.Slug)
	created, err := a.customDashboards.CreateDashboard(request.Context(), customdashboard.DashboardInput{
		Name: bundle.Dashboard.Name, Slug: slug, Public: false,
	})
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "failed")
		return
	}
	monitors, _ := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	for _, record := range bundle.Dashboard.Cards {
		config := append(json.RawMessage(nil), record.Config...)
		if record.Type == customdashboard.CardWebsite {
			config = remapCustomDashboardMonitorConfig(config, record.WebsiteMonitors, monitors)
		}
		_, err = a.customDashboards.CreateCard(request.Context(), created.ID, customdashboard.CardInput{
			Name: record.Name, Type: record.Type, SourceURL: record.SourceURL, Headers: record.Headers,
			ValuePath: record.ValuePath, SecondaryPath: record.SecondaryPath, Formula: record.Formula,
			Config: config, RefreshSeconds: record.RefreshSeconds,
		})
		if err != nil {
			_ = a.customDashboards.DeleteDashboard(request.Context(), created.ID)
			a.redirectCustomDashboardImportError(response, request, "failed")
			return
		}
	}
	a.recordAuditForRequest(request, "import_custom_dashboard", created.ID, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+url.QueryEscape(created.ID)+"&imported=1", http.StatusSeeOther)
}

func decodeCustomDashboardConfigFile(raw []byte) (customDashboardConfigFile, error) {
	var bundle customDashboardConfigFile
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return bundle, errors.New("配置文件包含多余内容")
	}
	if bundle.Format != customDashboardConfigFormat || bundle.Version != customDashboardConfigVersion {
		return bundle, errors.New("不支持的配置文件版本")
	}
	if name := strings.TrimSpace(bundle.Dashboard.Name); name == "" || utf8.RuneCountInString(name) > 80 {
		return bundle, errors.New("面板名称无效")
	}
	if slug := bundle.Dashboard.Slug; len(slug) > 80 || !customDashboardTransferSlugPattern.MatchString(slug) {
		return bundle, errors.New("面板地址标识无效")
	}
	if len(bundle.Dashboard.Cards) > customDashboardImportMaxCards {
		return bundle, errors.New("卡片数量超过限制")
	}
	for _, card := range bundle.Dashboard.Cards {
		if name := strings.TrimSpace(card.Name); name == "" || utf8.RuneCountInString(name) > 80 {
			return bundle, errors.New("卡片名称无效")
		}
		if len(card.Config) > 0 && !json.Valid(card.Config) {
			return bundle, errors.New("卡片配置无效")
		}
	}
	return bundle, nil
}

func selectedCustomDashboardCards(cards []customDashboardCardConfigRecord, form url.Values) ([]customDashboardCardConfigRecord, error) {
	if form.Get("selection_present") != "1" {
		return cards, nil
	}
	requested := make(map[int]struct{}, len(form["selection"]))
	for _, rawIndex := range form["selection"] {
		index, err := strconv.Atoi(rawIndex)
		if err != nil || index < 0 || index >= len(cards) {
			return nil, errors.New("卡片选择无效")
		}
		requested[index] = struct{}{}
	}
	if len(requested) == 0 {
		return nil, errors.New("至少选择一张卡片")
	}
	selected := make([]customDashboardCardConfigRecord, 0, len(requested))
	for index, card := range cards {
		if _, included := requested[index]; included {
			selected = append(selected, card)
		}
	}
	return selected, nil
}

func availableCustomDashboardSlug(dashboards []customdashboard.Dashboard, desired string) string {
	used := make(map[string]struct{}, len(dashboards))
	for _, dashboard := range dashboards {
		used[dashboard.Slug] = struct{}{}
	}
	if _, exists := used[desired]; !exists {
		return desired
	}
	for suffixNumber := 2; ; suffixNumber++ {
		suffix := fmt.Sprintf("-%d", suffixNumber)
		prefix := desired
		if len(prefix)+len(suffix) > 80 {
			prefix = strings.TrimRight(prefix[:80-len(suffix)], "-")
		}
		candidate := prefix + suffix
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func customDashboardMonitorIDs(config json.RawMessage) []string {
	var value struct {
		MonitorIDs []string `json:"monitorIds"`
	}
	_ = json.Unmarshal(config, &value)
	return value.MonitorIDs
}

func remapCustomDashboardMonitorConfig(config json.RawMessage, names []string, monitors []websitemonitor.Monitor) json.RawMessage {
	byName := make(map[string]string, len(monitors))
	byID := make(map[string]struct{}, len(monitors))
	for _, monitor := range monitors {
		byName[strings.ToLower(strings.TrimSpace(monitor.Config.Name))] = monitor.ID
		byID[monitor.ID] = struct{}{}
	}
	seen := map[string]struct{}{}
	var ids []string
	for _, name := range names {
		if id := byName[strings.ToLower(strings.TrimSpace(name))]; id != "" {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	for _, id := range customDashboardMonitorIDs(config) {
		if _, exists := byID[id]; exists {
			if _, duplicate := seen[id]; !duplicate {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(config, &fields)
	encodedIDs, _ := json.Marshal(ids)
	fields["monitorIds"] = encodedIDs
	encoded, _ := json.Marshal(fields)
	return encoded
}

func (a *App) redirectCustomDashboardImportError(response http.ResponseWriter, request *http.Request, code string) {
	target := "/config/dashboards?import_error=" + url.QueryEscape(code)
	if id := strings.TrimSpace(request.FormValue("dashboard_id")); id != "" {
		target += "&dashboard=" + url.QueryEscape(id)
	}
	a.recordAuditForRequest(request, "import_custom_dashboard", code, "failed")
	http.Redirect(response, request, target, http.StatusSeeOther)
}

func customDashboardImportError(code string) string {
	switch code {
	case "file_required":
		return "请选择要导入的 JSON 文件。"
	case "too_large":
		return "文件超过 2 MB，请检查内容后重试。"
	case "invalid":
		return "无法识别这个面板文件，请选择由 ScriptBoard 导出的 JSON。"
	case "failed":
		return "导入失败，文件中的卡片配置可能无效。"
	case "selection_required":
		return "请在文件中至少选择一张要导入的卡片。"
	default:
		return ""
	}
}
