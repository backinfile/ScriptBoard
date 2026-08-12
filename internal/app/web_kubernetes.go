package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"scriptboard/internal/clusterstatus"
)

type kubernetesPageView struct {
	clusterstatus.View
	Locale           webLocale
	CSRFToken        string
	Query            clusterstatus.Query
	CanManage        bool
	NamespaceOptions []string
}

type kubernetesConnectionView struct {
	Locale       webLocale
	CSRFToken    string
	Connection   clusterstatus.Connection
	Configured   bool
	Error        string
	Capabilities clusterstatus.Capabilities
}

func kubernetesSortURL(query clusterstatus.Query, field string) string {
	values := url.Values{}
	if query.Search != "" {
		values.Set("query", query.Search)
	}
	for key, value := range map[string]string{"status": query.Status, "namespace": query.Namespace, "kind": query.Kind} {
		if value != "" && value != "all" {
			values.Set(key, value)
		}
	}
	values.Set("sort", field)
	direction := "asc"
	if query.Sort == field && query.Direction == "asc" {
		direction = "desc"
	}
	values.Set("direction", direction)
	return "/monitor/kubernetes?" + values.Encode()
}

func parseKubernetesQuery(request *http.Request) clusterstatus.Query {
	query := clusterstatus.Query{
		Search: strings.TrimSpace(request.URL.Query().Get("query")), Status: request.URL.Query().Get("status"),
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

func (a *App) loadKubernetes(request *http.Request, query clusterstatus.Query) (clusterstatus.View, error) {
	view, err := a.kubernetesStatus.View(request.Context(), query)
	if err != nil {
		return clusterstatus.View{}, err
	}
	if view.Connection.Name != "" && view.CollectedAt.IsZero() {
		if err := a.kubernetesStatus.Refresh(request.Context()); err != nil {
			view.Connection.Connected = false
			view.Connection.Error = err.Error()
			return view, nil
		}
		return a.kubernetesStatus.View(request.Context(), query)
	}
	return view, nil
}

func (a *App) kubernetesPage(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	current := request.Context().Value(sessionContextKey).(session)
	query := parseKubernetesQuery(request)
	view, err := a.loadKubernetes(request, query)
	if err != nil {
		http.Error(response, "Unable to read Kubernetes status", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kubernetesTemplate.Execute(response, kubernetesPageView{View: view, Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: query, CanManage: roleAllows(current.role, permissionManageOperations), NamespaceOptions: view.AvailableNamespaces})
}

func (a *App) kubernetesData(response http.ResponseWriter, request *http.Request) {
	view, err := a.loadKubernetes(request, parseKubernetesQuery(request))
	if err != nil {
		http.Error(response, "Unable to read Kubernetes status", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(view)
}

func (a *App) kubernetesConnectionTask(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	current := request.Context().Value(sessionContextKey).(session)
	status, configured, err := a.kubernetesStatus.ConnectionStatus(request.Context())
	if err != nil {
		http.Error(response, "Unable to read Kubernetes connection", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kubernetesConnectionTemplate.Execute(response, kubernetesConnectionView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Connection: status.Connection, Configured: configured, Error: status.Error, Capabilities: status.Capabilities})
}

func kubernetesConnectionFromRequest(request *http.Request) clusterstatus.Connection {
	return clusterstatus.Connection{Name: request.FormValue("name"), KubeconfigPath: request.FormValue("kubeconfig_path"), Context: request.FormValue("context"), Mode: clusterstatus.Mode(request.FormValue("mode"))}
}

func (a *App) saveKubernetesConnection(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	connection := kubernetesConnectionFromRequest(request)
	if _, err := a.kubernetesStatus.SaveConnection(request.Context(), connection); err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusUnprocessableEntity)
		_ = kubernetesConnectionTemplate.Execute(response, kubernetesConnectionView{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Connection: connection, Error: err.Error()})
		return
	}
	if err := a.kubernetesStatus.Refresh(request.Context()); err != nil {
		http.Error(response, "Connection saved but initial Kubernetes snapshot failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	a.recordAuditForRequest(request, "save_kubernetes_connection", strings.TrimSpace(connection.Name), "succeeded")
	http.Redirect(response, request, "/monitor/kubernetes", http.StatusSeeOther)
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
	detail, err := a.kubernetesStatus.Detail(request.Context(), kubernetesWorkloadKey(request))
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
	lines, err := a.kubernetesStatus.Logs(request.Context(), kubernetesWorkloadKey(request), limit)
	if err != nil {
		http.Error(response, "Unable to read Kubernetes Pod logs", http.StatusBadRequest)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(lines)
}

func (a *App) pinKubernetesWorkload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	key := kubernetesWorkloadKey(request)
	if err := a.kubernetesStatus.Pin(request.Context(), key); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "pin_kubernetes_workload", key, "succeeded")
	http.Redirect(response, request, "/monitor/kubernetes", http.StatusSeeOther)
}

func (a *App) unpinKubernetesWorkload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	key := kubernetesWorkloadKey(request)
	if err := a.kubernetesStatus.Unpin(request.Context(), key); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "unpin_kubernetes_workload", key, "succeeded")
	http.Redirect(response, request, "/monitor/kubernetes", http.StatusSeeOther)
}

func (a *App) movePinnedKubernetesWorkload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	if err := a.kubernetesStatus.MovePin(request.Context(), kubernetesWorkloadKey(request), request.FormValue("direction")); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/monitor/kubernetes", http.StatusSeeOther)
}

func (a *App) operateKubernetesWorkload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return
	}
	operation := clusterstatus.Operation{Kind: clusterstatus.OperationKind(request.FormValue("operation")), WorkloadKey: kubernetesWorkloadKey(request)}
	if operation.Kind == clusterstatus.OperationScale {
		operation.Replicas, _ = strconv.Atoi(request.FormValue("replicas"))
	}
	if err := a.kubernetesStatus.Operate(request.Context(), operation); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "operate_kubernetes_workload", operation.WorkloadKey+":"+string(operation.Kind), "succeeded")
	returnTo := request.FormValue("return_to")
	if parsed, err := url.Parse(returnTo); err != nil || parsed.IsAbs() || !strings.HasPrefix(parsed.Path, "/monitor/kubernetes") {
		returnTo = "/monitor/kubernetes"
	}
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}
