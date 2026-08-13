package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/identity"
)

const (
	kubernetesTabMonitor     = "monitor"
	kubernetesTabConnections = "connections"
)

type kubernetesPageView struct {
	clusterstatus.View
	Locale             webLocale
	CSRFToken          string
	Query              clusterstatus.Query
	Connections        []clusterstatus.ConnectionStatus
	ActiveTab          string
	SelectedConnection string
	CanManage          bool
	NamespaceOptions   []string
}

type kubernetesConnectionView struct {
	Locale       webLocale
	CSRFToken    string
	Connection   clusterstatus.Connection
	Configured   bool
	Action       string
	Error        string
	Capabilities clusterstatus.Capabilities
}

func kubernetesQueryURL(query clusterstatus.Query, mutate func(url.Values)) string {
	values := url.Values{}
	if query.ConnectionID != "" {
		values.Set("cluster", query.ConnectionID)
	}
	if query.Search != "" {
		values.Set("query", query.Search)
	}
	for key, value := range map[string]string{"status": query.Status, "namespace": query.Namespace, "kind": query.Kind, "sort": query.Sort, "direction": query.Direction} {
		if value != "" && value != "all" {
			values.Set(key, value)
		}
	}
	mutate(values)
	return "/monitor/kubernetes?" + values.Encode()
}

func kubernetesSortURL(query clusterstatus.Query, field string) string {
	return kubernetesQueryURL(query, func(values url.Values) {
		values.Set("sort", field)
		direction := "asc"
		if query.Sort == field && query.Direction == "asc" {
			direction = "desc"
		}
		values.Set("direction", direction)
	})
}

func kubernetesStatusURL(query clusterstatus.Query, status string) string {
	return kubernetesQueryURL(query, func(values url.Values) {
		if status == "" || status == "all" {
			values.Del("status")
		} else {
			values.Set("status", status)
		}
	})
}

func parseKubernetesQuery(request *http.Request) clusterstatus.Query {
	query := clusterstatus.Query{
		ConnectionID: strings.TrimSpace(request.URL.Query().Get("cluster")),
		Search:       strings.TrimSpace(request.URL.Query().Get("query")), Status: request.URL.Query().Get("status"),
		Namespace: request.URL.Query().Get("namespace"), Kind: request.URL.Query().Get("kind"),
		Sort: request.URL.Query().Get("sort"), Direction: request.URL.Query().Get("direction"), Limit: 100,
	}
	if query.Status == "" {
		query.Status = "all"
	}
	if query.Namespace == "" {
		query.Namespace = "all"
	}
	if query.Kind == "" {
		query.Kind = "all"
	}
	if query.Sort == "" {
		query.Sort = "status"
	}
	if query.Direction == "" {
		query.Direction = "asc"
	}
	return query
}

func (a *App) loadKubernetes(request *http.Request, connectionID string, query clusterstatus.Query) (clusterstatus.View, error) {
	view, err := a.kubernetesStatus.View(request.Context(), connectionID, query)
	if err != nil {
		return clusterstatus.View{}, err
	}
	if view.Connection.Name != "" && view.CollectedAt.IsZero() {
		if err := a.kubernetesStatus.Refresh(request.Context(), connectionID); err != nil {
			view.Connection.Connected = false
			view.Connection.Error = err.Error()
			return view, nil
		}
		return a.kubernetesStatus.View(request.Context(), connectionID, query)
	}
	return view, nil
}

func sanitizeKubernetesConnections(connections []clusterstatus.ConnectionStatus) []clusterstatus.ConnectionStatus {
	result := append([]clusterstatus.ConnectionStatus(nil), connections...)
	for index := range result {
		result[index].KubeconfigPath = ""
		result[index].Fingerprint = ""
	}
	return result
}

func selectKubernetesConnection(connections []clusterstatus.ConnectionStatus, requested string) string {
	for _, connection := range connections {
		if connection.ID == requested {
			return requested
		}
	}
	if len(connections) > 0 {
		return connections[0].ID
	}
	return ""
}

func (a *App) kubernetesPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	current := request.Context().Value(sessionContextKey).(session)
	query := parseKubernetesQuery(request)
	connections, err := a.kubernetesStatus.Connections(request.Context())
	if err != nil {
		http.Error(response, "Unable to read Kubernetes connections", http.StatusInternalServerError)
		return
	}
	selected := selectKubernetesConnection(connections, query.ConnectionID)
	query.ConnectionID = selected
	activeTab := request.URL.Query().Get("tab")
	if len(connections) == 0 {
		activeTab = kubernetesTabConnections
	} else if activeTab != kubernetesTabConnections {
		activeTab = kubernetesTabMonitor
	}
	var view clusterstatus.View
	if selected != "" {
		view, err = a.loadKubernetes(request, selected, query)
		if err != nil {
			http.Error(response, "Unable to read Kubernetes status", http.StatusInternalServerError)
			return
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kubernetesTemplate.Execute(response, kubernetesPageView{
		View: view, Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: query,
		Connections: sanitizeKubernetesConnections(connections), ActiveTab: activeTab, SelectedConnection: selected,
		CanManage: identity.Allows(current.role, identity.PermissionManageOperations), NamespaceOptions: view.AvailableNamespaces,
	})
}

func (a *App) kubernetesData(response http.ResponseWriter, request *http.Request) {
	query := parseKubernetesQuery(request)
	connections, err := a.kubernetesStatus.Connections(request.Context())
	if err != nil {
		http.Error(response, "Unable to read Kubernetes connections", http.StatusInternalServerError)
		return
	}
	selected := selectKubernetesConnection(connections, query.ConnectionID)
	if selected == "" {
		http.Error(response, "Kubernetes connection is not configured", http.StatusNotFound)
		return
	}
	query.ConnectionID = selected
	view, err := a.loadKubernetes(request, selected, query)
	if err != nil {
		http.Error(response, "Unable to read Kubernetes status", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func kubernetesConnectionAction(id string) string {
	if id == "" {
		return "/monitor/kubernetes/connections"
	}
	return "/monitor/kubernetes/connections/" + url.PathEscape(id)
}

func (a *App) kubernetesConnectionTask(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	current := request.Context().Value(sessionContextKey).(session)
	id := request.PathValue("connection")
	var status clusterstatus.ConnectionStatus
	configured := false
	var err error
	if id != "" {
		status, configured, err = a.kubernetesStatus.ConnectionStatus(request.Context(), id)
		if err != nil {
			http.Error(response, "Unable to read Kubernetes connection", http.StatusInternalServerError)
			return
		}
		if !configured {
			http.NotFound(response, request)
			return
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kubernetesConnectionTemplate.Execute(response, kubernetesConnectionView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Connection: status.Connection, Configured: configured, Action: kubernetesConnectionAction(id), Error: status.Error, Capabilities: status.Capabilities})
}

func kubernetesConnectionFromRequest(request *http.Request) clusterstatus.Connection {
	return clusterstatus.Connection{ID: request.PathValue("connection"), Name: request.FormValue("name"), KubeconfigPath: request.FormValue("kubeconfig_path"), Context: request.FormValue("context"), Mode: clusterstatus.Mode(request.FormValue("mode"))}
}

func (a *App) saveKubernetesConnection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	connection := kubernetesConnectionFromRequest(request)
	result, err := a.kubernetesStatus.SaveConnection(request.Context(), connection)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusUnprocessableEntity)
		_ = kubernetesConnectionTemplate.Execute(response, kubernetesConnectionView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Connection: connection, Configured: connection.ID != "", Action: kubernetesConnectionAction(connection.ID), Error: err.Error()})
		return
	}
	if err := a.kubernetesStatus.Refresh(request.Context(), result.ID); err != nil {
		http.Error(response, "Connection saved but initial Kubernetes snapshot failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	a.recordAuditForRequest(request, "save_kubernetes_connection", result.ID+":"+result.Name, "succeeded")
	http.Redirect(response, request, "/monitor/kubernetes?cluster="+url.QueryEscape(result.ID), http.StatusSeeOther)
}

func (a *App) testKubernetesConnection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	result, err := a.kubernetesStatus.TestConnection(request.Context(), kubernetesConnectionFromRequest(request))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		response.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(response).Encode(map[string]string{"error": err.Error()})
		return
	}
	result.KubeconfigPath = ""
	_ = json.NewEncoder(response).Encode(result)
}

func kubernetesWorkloadKey(request *http.Request) string {
	return request.PathValue("namespace") + "/" + request.PathValue("kind") + "/" + request.PathValue("name")
}

func (a *App) kubernetesWorkloadDetails(response http.ResponseWriter, request *http.Request) {
	detail, err := a.kubernetesStatus.Detail(request.Context(), request.PathValue("connection"), kubernetesWorkloadKey(request))
	if err != nil {
		http.Error(response, "Unable to read Kubernetes workload details", http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(detail)
}

func (a *App) kubernetesWorkloadLogs(response http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	if limit <= 0 && strings.Contains(request.Header.Get("Accept"), "text/html") {
		limit = 500
	}
	lines, err := a.kubernetesStatus.Logs(request.Context(), request.PathValue("connection"), kubernetesWorkloadKey(request), limit)
	if err != nil {
		http.Error(response, "Unable to read Kubernetes Pod logs", http.StatusBadRequest)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	if strings.Contains(request.Header.Get("Accept"), "text/html") {
		locale := resolveWebLocale(request)
		views := make([]kubernetesLogLineView, 0, len(lines))
		for _, line := range lines {
			views = append(views, kubernetesLogLineView{At: line.At, Source: line.Pod + "/" + line.Container, Text: line.Text})
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = kubernetesLogsTemplate.Execute(response, kubernetesLogsPageView{
			Locale: locale, Connection: request.PathValue("connection"), Namespace: request.PathValue("namespace"), Kind: request.PathValue("kind"), Name: request.PathValue("name"), Lines: views,
		})
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(lines)
}

type kubernetesLogLineView struct {
	At           time.Time
	Source, Text string
}

type kubernetesLogsPageView struct {
	Locale                            webLocale
	Connection, Namespace, Kind, Name string
	Lines                             []kubernetesLogLineView
}

func (a *App) operateKubernetesWorkload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	connectionID := request.PathValue("connection")
	operation := clusterstatus.Operation{Kind: clusterstatus.OperationKind(request.FormValue("operation")), WorkloadKey: kubernetesWorkloadKey(request)}
	if operation.Kind == clusterstatus.OperationScale {
		operation.Replicas, _ = strconv.Atoi(request.FormValue("replicas"))
	}
	if err := a.kubernetesStatus.Operate(request.Context(), connectionID, operation); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "operate_kubernetes_workload", connectionID+":"+operation.WorkloadKey+":"+string(operation.Kind), "succeeded")
	returnTo := request.FormValue("return_to")
	if parsed, err := url.Parse(returnTo); err != nil || parsed.IsAbs() || !strings.HasPrefix(parsed.Path, "/monitor/kubernetes") {
		returnTo = "/monitor/kubernetes?cluster=" + url.QueryEscape(connectionID)
	}
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}
