package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/redismanager"
	"scriptboard/internal/secretredaction"
)

type redisDatabasesPageData struct {
	databaseWorkspaceData
	Locale      webLocale
	CSRFToken   string
	Instances   []redismanager.Instance
	Selected    *redismanager.Instance
	Overview    *redismanager.Overview
	Scan        *redismanager.ScanPage
	KeyGroups   []redisKeyGroup
	KeyValue    *redismanager.KeyValue
	Pattern     string
	SelectedKey string
	ActiveTab   string
	LoadError   string
}

type redisKeyGroup struct {
	Namespace string
	Root      bool
	Keys      []redismanager.KeySummary
}

func groupRedisKeys(keys []redismanager.KeySummary) []redisKeyGroup {
	groups := make(map[string][]redismanager.KeySummary)
	order := make([]string, 0)
	for _, key := range keys {
		namespace := ""
		// Redis 键空间仅将显式 "::" 视为层级分隔符，避免拆散包含普通冒号的键名。
		if prefix, _, found := strings.Cut(key.Name, "::"); found {
			namespace = strings.TrimSpace(prefix)
		}
		if _, exists := groups[namespace]; !exists {
			order = append(order, namespace)
		}
		groups[namespace] = append(groups[namespace], key)
	}
	sort.SliceStable(order, func(left, right int) bool {
		if order[left] == "" {
			return false
		}
		if order[right] == "" {
			return true
		}
		return strings.ToLower(order[left]) < strings.ToLower(order[right])
	})
	result := make([]redisKeyGroup, 0, len(order))
	for _, namespace := range order {
		result = append(result, redisKeyGroup{Namespace: namespace, Root: namespace == "", Keys: groups[namespace]})
	}
	return result
}

// databaseWorkspaceData keeps the shared connection inventory and add flow
// identical while each engine retains its own detail operations.
type databaseWorkspaceData struct {
	Locale               webLocale
	CSRFToken            string
	BackupRoot           string
	Tools                mysqlmanager.ToolSettings
	ConnectionRows       []databaseConnectionRow
	ConnectionPagination mysqlPagination
	SelectedMySQL        *mysqlmanager.Instance
	SelectedRedis        *redismanager.Instance
	ActiveEngine         string
	AddEngine            string
	AddDrawerOpen        bool
}

type databaseConnectionRow struct {
	Engine, ID, Name, Username, Host, ConnectionState string
	Port, Database                                    int
}

func newDatabaseWorkspaceData(request *http.Request, selectedEngine string, locale webLocale, csrfToken, backupRoot string, tools mysqlmanager.ToolSettings, mysqlInstances []mysqlmanager.Instance, redisInstances []redismanager.Instance) databaseWorkspaceData {
	connections := make([]databaseConnectionRow, 0, len(mysqlInstances)+len(redisInstances))
	for _, instance := range mysqlInstances {
		connections = append(connections, databaseConnectionRow{Engine: "mysql", ID: instance.ID, Name: instance.Name, Username: instance.Username, Host: instance.Host, Port: instance.Port, ConnectionState: string(instance.ConnectionState)})
	}
	for _, instance := range redisInstances {
		connections = append(connections, databaseConnectionRow{Engine: "redis", ID: instance.ID, Name: instance.Name, Username: instance.Username, Host: instance.Host, Port: instance.Port, Database: instance.Database, ConnectionState: string(instance.ConnectionState)})
	}
	// A single name-ordered inventory lets related MySQL and Redis connections sit together.
	sort.SliceStable(connections, func(left, right int) bool {
		leftName, rightName := strings.ToLower(connections[left].Name), strings.ToLower(connections[right].Name)
		if leftName == rightName {
			return connections[left].Engine < connections[right].Engine
		}
		return leftName < rightName
	})
	connectionRows, pagination := mysqlSlicePageWithSize(connections, mysqlRequestedNamedPage(request, "connection_page"), mysqlInstancePageSize)
	addEngine := strings.TrimSpace(request.URL.Query().Get("add"))
	addDrawerOpen := addEngine == "mysql" || addEngine == "redis"
	if !addDrawerOpen {
		addEngine = selectedEngine
	}
	return databaseWorkspaceData{
		Locale: locale, CSRFToken: csrfToken, BackupRoot: backupRoot, Tools: tools,
		ConnectionRows: connectionRows, ConnectionPagination: pagination, ActiveEngine: selectedEngine,
		AddEngine: addEngine, AddDrawerOpen: addDrawerOpen,
	}
}

func (a *App) databasesPage(response http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("engine") == "redis" {
		a.redisDatabasesPage(response, request)
		return
	}
	a.mysqlDatabasesPage(response, request)
}

func (a *App) redisDatabasesPage(response http.ResponseWriter, request *http.Request) {
	instances, err := a.redis.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	mysqlInstances, err := a.mysql.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	data := redisDatabasesPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Instances: instances, ActiveTab: "overview", Pattern: strings.TrimSpace(request.URL.Query().Get("pattern")), SelectedKey: strings.TrimSpace(request.URL.Query().Get("key"))}
	data.databaseWorkspaceData = newDatabaseWorkspaceData(request, "redis", data.Locale, data.CSRFToken, a.mysql.BackupRoot(), a.mysql.Tools(), mysqlInstances, instances)
	if request.URL.Query().Get("tab") == "keys" || request.URL.Query().Get("tab") == "diagnostics" {
		data.ActiveTab = request.URL.Query().Get("tab")
	}
	id := strings.TrimSpace(request.URL.Query().Get("instance"))
	if id == "" && len(instances) > 0 {
		id = instances[0].ID
	}
	for index := range instances {
		if instances[index].ID == id {
			selected := instances[index]
			data.Selected = &selected
			break
		}
	}
	if id != "" && data.Selected == nil {
		http.Error(response, "Redis instance not found", http.StatusNotFound)
		return
	}
	if data.Selected != nil {
		data.SelectedRedis = data.Selected
		ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
		defer cancel()
		switch data.ActiveTab {
		case "keys":
			scan, scanErr := a.redis.Scan(ctx, id, redismanager.ScanRequest{Pattern: data.Pattern, Count: 200})
			if scanErr != nil {
				data.LoadError = secretredaction.String(scanErr.Error())
			} else {
				data.Scan = &scan
				data.KeyGroups = groupRedisKeys(scan.Keys)
				if data.SelectedKey != "" {
					value, valueErr := a.redis.ReadKey(ctx, id, data.SelectedKey)
					if valueErr != nil {
						data.LoadError = secretredaction.String(valueErr.Error())
					} else {
						data.KeyValue = &value
					}
				}
			}
		case "overview":
			overview, overviewErr := a.redis.Overview(ctx, id)
			if overviewErr != nil {
				data.LoadError = secretredaction.String(overviewErr.Error())
			} else {
				data.Overview = &overview
			}
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = redisDatabasesTemplate.Execute(response, data)
}

func (a *App) saveRedisInstance(response http.ResponseWriter, request *http.Request) {
	// Reject cross-site connection mutations before parsing or persisting attacker-controlled fields.
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(request.FormValue("port"))
	if err != nil {
		http.Error(response, "invalid Redis port", http.StatusBadRequest)
		return
	}
	database, err := strconv.Atoi(request.FormValue("database"))
	if err != nil {
		http.Error(response, "invalid Redis database", http.StatusBadRequest)
		return
	}
	instance, err := a.redis.SaveInstance(request.Context(), redismanager.InstanceInput{ID: request.FormValue("id"), Name: request.FormValue("name"), Environment: redismanager.Environment(request.FormValue("environment")), Host: request.FormValue("host"), Port: port, Username: request.FormValue("username"), Password: request.FormValue("password"), Database: database, TLSMode: redismanager.TLSMode(request.FormValue("tls_mode")), CAPath: request.FormValue("ca_path")})
	if err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/resources/databases?engine=redis&instance="+url.QueryEscape(instance.ID), http.StatusSeeOther)
}

func (a *App) testRedisInstance(response http.ResponseWriter, request *http.Request) {
	// Connection tests update persisted health state, so they require the same CSRF boundary as MySQL tests.
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	result, err := a.redis.TestInstance(request.Context(), request.PathValue("id"))
	response.Header().Set("Content-Type", "application/json")
	if err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(response).Encode(struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}{OK: result.OK, Version: result.Version})
}
func (a *App) deleteRedisInstance(response http.ResponseWriter, request *http.Request) {
	// Protect destructive connection removal even when the administrator session is already stepped up.
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := a.redis.DeleteInstance(request.Context(), request.PathValue("id")); err != nil {
		http.Error(response, secretredaction.String(err.Error()), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/resources/databases?engine=redis", http.StatusSeeOther)
}
