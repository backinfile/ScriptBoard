package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	customDashboardConfigFormat   = "scriptboard.custom-dashboard-nodes"
	customDashboardConfigVersion  = 1
	customDashboardImportMaxSize  = 2 << 20
	customDashboardImportMaxCards = 100
)

type customDashboardConfigFile struct {
	Format     string                            `json:"format"`
	Version    int                               `json:"version"`
	ExportedAt time.Time                         `json:"exported_at"`
	Nodes      []customDashboardCardConfigRecord `json:"nodes"`
}

type customDashboardCardConfigRecord struct {
	Name            string                   `json:"name"`
	Note            string                   `json:"note,omitempty"`
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

// 配置格式说明文档随二进制内嵌，面板导入页可直接下载（供人工或 AI 编写导入文件参照）。
var dashboardConfigFormatDoc = mustWebAsset("ui/assets/dashboard-config-format.md")

func (a *App) downloadDashboardConfigFormat(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="scriptboard-dashboard-config-format.md"`)
	_, _ = io.WriteString(response, dashboardConfigFormatDoc)
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
	quickRunNamesByID, _ := a.customDashboardQuickRunNameMaps(request.Context())
	bundle := customDashboardConfigFile{
		Format: customDashboardConfigFormat, Version: customDashboardConfigVersion, ExportedAt: time.Now().UTC(),
	}
	selectedValues := request.URL.Query()["selection"]
	selected := make(map[string]struct{}, len(selectedValues))
	for _, id := range selectedValues {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		http.Error(response, "请至少选择一个节点", http.StatusUnprocessableEntity)
		return
	}
	for _, card := range dashboard.Cards {
		if _, included := selected[card.ID]; !included {
			continue
		}
		delete(selected, card.ID)
		config := append(json.RawMessage(nil), card.Config...)
		// actions 的 quickRunId 与 flow 的 runId 是本机 ID；导出时附带/刷新
		// 快捷执行项名称，导入端按名称重映射（与网站监控 ID 同一先例）。
		config = annotateCustomDashboardQuickRunRefs(config, quickRunNamesByID)
		record := customDashboardCardConfigRecord{
			Name: card.Name, Note: card.Note, Type: card.Type, SourceURL: card.SourceURL, Headers: card.Headers,
			ValuePath: card.ValuePath, SecondaryPath: card.SecondaryPath, Formula: card.Formula,
			Config: config, RefreshSeconds: card.RefreshSeconds,
		}
		if card.Type == customdashboard.CardWebsite {
			for _, id := range customDashboardMonitorIDs(card.Config) {
				if name := strings.TrimSpace(monitorNames[id]); name != "" {
					record.WebsiteMonitors = append(record.WebsiteMonitors, name)
				}
			}
		}
		bundle.Nodes = append(bundle.Nodes, record)
	}
	if len(selected) != 0 || len(bundle.Nodes) == 0 {
		http.Error(response, "节点选择无效，请刷新页面后重试", http.StatusUnprocessableEntity)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scriptboard-dashboard-nodes-%s.json"`, time.Now().Format("20060102-150405")))
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
	selectedCards, err := selectedCustomDashboardCards(bundle.Nodes, request.Form)
	if err != nil {
		a.redirectCustomDashboardImportError(response, request, "selection_required")
		return
	}
	bundle.Nodes = selectedCards
	dashboardID := strings.TrimSpace(request.FormValue("dashboard_id"))
	if dashboardID == "" {
		a.redirectCustomDashboardImportError(response, request, "failed")
		return
	}
	if _, err := a.customDashboards.GetDashboard(request.Context(), dashboardID); err != nil {
		a.redirectCustomDashboardImportError(response, request, "failed")
		return
	}
	monitors, _ := a.websiteMonitor.List(request.Context(), websitemonitor.Filter{})
	quickRunIDs, quickRunNamesByName := a.customDashboardQuickRunNameMaps(request.Context())
	inputs := make([]customdashboard.CardInput, 0, len(bundle.Nodes))
	for _, record := range bundle.Nodes {
		config := append(json.RawMessage(nil), record.Config...)
		if record.Type == customdashboard.CardWebsite {
			config = remapCustomDashboardMonitorConfig(config, record.WebsiteMonitors, monitors)
		}
		// actions/flow 的快捷执行项引用按名称重映射到本机 ID；flow 节点
		// 无法解析时拒绝导入（DAG 不允许缺节点），actions 未匹配则降级丢弃。
		config, err := remapCustomDashboardQuickRunRefs(config, quickRunIDs, quickRunNamesByName)
		if err != nil {
			a.redirectCustomDashboardImportError(response, request, "failed")
			return
		}
		inputs = append(inputs, customdashboard.CardInput{
			Name: record.Name, Note: record.Note, Type: record.Type, SourceURL: record.SourceURL, Headers: record.Headers,
			ValuePath: record.ValuePath, SecondaryPath: record.SecondaryPath, Formula: record.Formula,
			Config: config, RefreshSeconds: record.RefreshSeconds,
		})
	}
	// 含 script/uses 节点的流程卡片需要执行管理权限才能导入。
	for _, input := range inputs {
		if err := a.enforceFlowExecutionPermission(request, input); err != nil {
			http.Error(response, err.Error(), http.StatusForbidden)
			return
		}
	}
	if err := a.customDashboards.ImportCards(request.Context(), dashboardID, inputs); err != nil {
		a.redirectCustomDashboardImportError(response, request, "failed")
		return
	}
	a.recordAuditForRequest(request, "import_custom_dashboard", dashboardID, "succeeded")
	http.Redirect(response, request, "/config/dashboards?dashboard="+url.QueryEscape(dashboardID)+"&imported=1", http.StatusSeeOther)
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
	if len(bundle.Nodes) == 0 || len(bundle.Nodes) > customDashboardImportMaxCards {
		return bundle, errors.New("节点数量无效或超过限制")
	}
	for _, card := range bundle.Nodes {
		if name := strings.TrimSpace(card.Name); name == "" || utf8.RuneCountInString(name) > 80 {
			return bundle, errors.New("节点名称无效")
		}
		if len(card.Config) > 0 && !json.Valid(card.Config) {
			return bundle, errors.New("节点配置无效")
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
			return nil, errors.New("节点选择无效")
		}
		requested[index] = struct{}{}
	}
	if len(requested) == 0 {
		return nil, errors.New("至少选择一个节点")
	}
	selected := make([]customDashboardCardConfigRecord, 0, len(requested))
	for index, card := range cards {
		if _, included := requested[index]; included {
			selected = append(selected, card)
		}
	}
	return selected, nil
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

// customDashboardQuickRunNameMaps 返回快捷执行项的 id→name 与 name→id 映射，
// 供导出注解与导入重映射使用；读取失败时返回空映射（导入端会走降级/拒绝）。
func (a *App) customDashboardQuickRunNameMaps(ctx context.Context) (byID, byName map[string]string) {
	byID = map[string]string{}
	byName = map[string]string{}
	rows, err := a.db.QueryContext(ctx, `SELECT id, name FROM quick_runs`)
	if err != nil {
		return byID, byName
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			byID[id] = name
			if _, present := byName[name]; !present {
				byName[name] = id
			}
		}
	}
	return byID, byName
}

// annotateCustomDashboardQuickRunRefs 在导出配置中附带快捷执行项名称：
// actions[].quickRunName 新增；flow 节点/post 的 run 名称按当前库刷新。
func annotateCustomDashboardQuickRunRefs(config json.RawMessage, quickRunNamesByID map[string]string) json.RawMessage {
	if len(config) == 0 {
		return config
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(config, &fields); err != nil {
		return config
	}
	if raw, present := fields["actions"]; present {
		var actions []map[string]any
		if err := json.Unmarshal(raw, &actions); err == nil {
			for _, action := range actions {
				if id, _ := action["quickRunId"].(string); id != "" {
					if name := quickRunNamesByID[id]; name != "" {
						action["quickRunName"] = name
					}
				}
			}
			if encoded, err := json.Marshal(actions); err == nil {
				fields["actions"] = encoded
			}
		}
	}
	if raw, present := fields["flow"]; present {
		var flow struct {
			Nodes []map[string]any `json:"nodes"`
			Post  []map[string]any `json:"post"`
		}
		if err := json.Unmarshal(raw, &flow); err == nil {
			var refresh func([]map[string]any)
			refresh = func(entries []map[string]any) {
				for _, entry := range entries {
					refresh(nestedFlowPosts(entry))
					if id, _ := entry["runId"].(string); id != "" {
						if name := quickRunNamesByID[id]; name != "" {
							entry["run"] = name
						}
					}
					// http-request 的 header_* 值是凭据，导出时脱敏（与既有请求头脱敏一致）。
					if uses, _ := entry["uses"].(string); uses != "http-request" {
						continue
					}
					if with, ok := entry["with"].(map[string]any); ok {
						for key := range with {
							if strings.HasPrefix(key, "header_") {
								with[key] = secretredaction.Marker
							}
						}
					}
				}
			}
			refresh(flow.Nodes)
			refresh(flow.Post)
			if encoded, err := json.Marshal(flow); err == nil {
				fields["flow"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return config
	}
	return encoded
}

// remapCustomDashboardQuickRunRefs 在导入时把快捷执行项引用重映射到本机 ID。
// actions：本机 ID 仍然有效则保留，否则按 quickRunName 解析，未匹配则丢弃该
// 操作（与网站监控未匹配丢弃的先例一致）；flow：节点/post 按 run 名称解析，
// 任一无法解析即返回错误拒绝导入（DAG 不允许缺节点）。
func remapCustomDashboardQuickRunRefs(config json.RawMessage, quickRunIDs, quickRunIDsByName map[string]string) (json.RawMessage, error) {
	if len(config) == 0 {
		return config, nil
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(config, &fields); err != nil {
		return config, nil
	}
	if raw, present := fields["actions"]; present {
		var actions []map[string]any
		if err := json.Unmarshal(raw, &actions); err == nil {
			kept := make([]map[string]any, 0, len(actions))
			for _, action := range actions {
				name, _ := action["quickRunName"].(string)
				delete(action, "quickRunName")
				id, _ := action["quickRunId"].(string)
				if id != "" {
					if _, ok := quickRunIDs[id]; ok {
						kept = append(kept, action)
						continue
					}
				}
				if name != "" {
					if resolved, ok := quickRunIDsByName[name]; ok {
						action["quickRunId"] = resolved
						kept = append(kept, action)
					}
					// 未匹配的 quick_run 操作丢弃；browser_http 无 quickRunId 不受影响。
				} else if id == "" {
					kept = append(kept, action)
				}
			}
			if len(kept) > 0 {
				if encoded, err := json.Marshal(kept); err == nil {
					fields["actions"] = encoded
				}
			} else {
				delete(fields, "actions")
			}
		}
	}
	if raw, present := fields["flow"]; present {
		var flow struct {
			Nodes []map[string]any `json:"nodes"`
			Post  []map[string]any `json:"post"`
		}
		if err := json.Unmarshal(raw, &flow); err == nil {
			var remap func([]map[string]any) error
			remap = func(entries []map[string]any) error {
				for _, entry := range entries {
					if err := remap(nestedFlowPosts(entry)); err != nil {
						return err
					}
					label, _ := entry["id"].(string)
					if label == "" {
						label, _ = entry["name"].(string)
					}
					id, _ := entry["runId"].(string)
					if id != "" {
						if _, ok := quickRunIDs[id]; ok {
							continue
						}
					}
					name, _ := entry["run"].(string)
					if name == "" {
						// script/uses 节点无快捷执行项引用，无需重映射。
						continue
					}
					resolved, ok := quickRunIDsByName[name]
					if !ok {
						return fmt.Errorf("流程节点 %s 的快捷执行项 %q 未匹配", label, name)
					}
					entry["runId"] = resolved
				}
				return nil
			}
			if err := remap(flow.Nodes); err != nil {
				return nil, err
			}
			if err := remap(flow.Post); err != nil {
				return nil, err
			}
			if encoded, err := json.Marshal(flow); err == nil {
				fields["flow"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return config, nil
	}
	return encoded, nil
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
		return "无法识别这个节点配置文件，请选择由 ScriptBoard 导出的 JSON。"
	case "failed":
		return "导入失败，文件中的节点配置可能无效。"
	case "selection_required":
		return "请在文件中至少选择一个要导入的节点。"
	default:
		return ""
	}
}

func nestedFlowPosts(entry map[string]any) []map[string]any {
	var posts []map[string]any
	list, _ := entry["post"].([]any)
	for _, value := range list {
		if post, ok := value.(map[string]any); ok {
			posts = append(posts, post)
		}
	}
	return posts
}
