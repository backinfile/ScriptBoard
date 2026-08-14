package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/clusterstatus"
	"scriptboard/internal/identity"
	"scriptboard/internal/kubeconfigmanager"
)

const (
	kubernetesTabMonitor     = "monitor"
	kubernetesTabConnections = "connections"
	kubernetesTabLocal       = "local"
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
	LocalConfig        kubeconfigmanager.Snapshot
	LocalConfigPaths   []string
	LocalConnectionIDs map[string]string
	LocalError         string
	LocalNotice        string
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

func localKubeconfigPaths(connections []clusterstatus.ConnectionStatus) ([]string, error) {
	defaultPath, err := kubeconfigmanager.DefaultPath()
	if err != nil {
		return nil, err
	}
	paths := []string{defaultPath}
	seen := map[string]bool{filepath.Clean(defaultPath): true}
	for _, connection := range connections {
		path := filepath.Clean(strings.TrimSpace(connection.KubeconfigPath))
		if !filepath.IsAbs(path) || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths, nil
}

func selectLocalKubeconfigPath(paths []string, requested string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return paths[0], true
	}
	requested = filepath.Clean(requested)
	for _, path := range paths {
		if requested == filepath.Clean(path) {
			return path, true
		}
	}
	return paths[0], false
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
	canManage := identity.Allows(current.role, identity.PermissionManageOperations)
	activeTab := request.URL.Query().Get("tab")
	if activeTab == kubernetesTabLocal && !canManage {
		activeTab = ""
	}
	if len(connections) == 0 && activeTab != kubernetesTabLocal {
		activeTab = kubernetesTabConnections
	} else if activeTab != kubernetesTabConnections && activeTab != kubernetesTabLocal {
		activeTab = kubernetesTabMonitor
	}
	var view clusterstatus.View
	if selected != "" && activeTab == kubernetesTabMonitor {
		view, err = a.loadKubernetes(request, selected, query)
		if err != nil {
			http.Error(response, "Unable to read Kubernetes status", http.StatusInternalServerError)
			return
		}
	}
	var localConfig kubeconfigmanager.Snapshot
	var localPaths []string
	localConnectionIDs := make(map[string]string)
	var localError string
	if activeTab == kubernetesTabLocal {
		localPaths, err = localKubeconfigPaths(connections)
		if err == nil {
			localPath, valid := selectLocalKubeconfigPath(localPaths, request.URL.Query().Get("path"))
			if !valid {
				localError = "The selected kubeconfig path is not managed by ScriptBoard"
			}
			localConfig, err = kubeconfigmanager.Inspect(localPath)
		}
		if err != nil {
			localError = err.Error()
			if len(localPaths) > 0 {
				localConfig.Path = localPaths[0]
			}
		} else {
			for _, connection := range connections {
				if sameKubeconfigPath(connection.KubeconfigPath, localConfig.Path) {
					contextName := strings.TrimSpace(connection.Context)
					if contextName == "" {
						contextName = localConfig.Current
					}
					if contextName != "" {
						localConnectionIDs[contextName] = connection.ID
					}
				}
			}
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kubernetesTemplate.Execute(response, kubernetesPageView{
		View: view, Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: query,
		Connections: sanitizeKubernetesConnections(connections), ActiveTab: activeTab, SelectedConnection: selected,
		CanManage: canManage, NamespaceOptions: view.AvailableNamespaces, LocalConfig: localConfig, LocalConfigPaths: localPaths, LocalConnectionIDs: localConnectionIDs,
		LocalError: localError, LocalNotice: request.URL.Query().Get("notice"),
	})
}

func sameKubeconfigPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "." || right == "." {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (a *App) managedKubeconfigPath(request *http.Request) (string, error) {
	connections, err := a.kubernetesStatus.Connections(request.Context())
	if err != nil {
		return "", err
	}
	paths, err := localKubeconfigPaths(connections)
	if err != nil {
		return "", err
	}
	requested := request.FormValue("path")
	if requested == "" {
		requested = request.URL.Query().Get("path")
	}
	path, valid := selectLocalKubeconfigPath(paths, requested)
	if !valid {
		return "", errors.New("selected kubeconfig path is not managed by ScriptBoard")
	}
	return path, nil
}

func kubernetesLocalURL(path, notice string) string {
	values := url.Values{"tab": {kubernetesTabLocal}}
	if path != "" {
		values.Set("path", path)
	}
	if notice != "" {
		values.Set("notice", notice)
	}
	return "/monitor/kubernetes?" + values.Encode()
}

func requireKubernetesMutation(response http.ResponseWriter, request *http.Request) bool {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", http.StatusForbidden)
		return false
	}
	return true
}

func (a *App) downloadLocalKubeconfig(response http.ResponseWriter, request *http.Request) {
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := kubeconfigmanager.Download(path)
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="kubeconfig.yaml"`)
	_, _ = response.Write(raw)
}

func (a *App) downloadLocalKubeconfigContext(response http.ResponseWriter, request *http.Request) {
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := kubeconfigmanager.DownloadContext(path, request.PathValue("context"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="kubeconfig-context.yaml"`)
	_, _ = response.Write(raw)
}

func readKubeconfigUpload(response http.ResponseWriter, request *http.Request) ([]byte, error) {
	if request.MultipartForm == nil {
		request.Body = http.MaxBytesReader(response, request.Body, kubeconfigmanager.MaxFileSize+(64<<10))
		if err := request.ParseMultipartForm(kubeconfigmanager.MaxFileSize); err != nil {
			return nil, errors.New("invalid or oversized multipart upload")
		}
	}
	file, _, err := request.FormFile("kubeconfig")
	if err != nil {
		return nil, errors.New("kubeconfig file is required")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, kubeconfigmanager.MaxFileSize+1))
	if err != nil || len(raw) > kubeconfigmanager.MaxFileSize {
		return nil, errors.New("kubeconfig file exceeds 2 MiB")
	}
	return raw, nil
}

func (a *App) previewLocalKubeconfigImport(response http.ResponseWriter, request *http.Request) {
	raw, err := readKubeconfigUpload(response, request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if !requireKubernetesMutation(response, request) {
		return
	}
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	preview, err := kubeconfigmanager.PreviewImport(path, raw)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(preview)
}

func (a *App) importLocalKubeconfig(response http.ResponseWriter, request *http.Request) {
	raw, err := readKubeconfigUpload(response, request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if !requireKubernetesMutation(response, request) {
		return
	}
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = kubeconfigmanager.Import(path, raw, request.FormValue("current_context") == "import")
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "import_local_kubeconfig", filepath.Base(path), "succeeded")
	http.Redirect(response, request, kubernetesLocalURL(path, "imported"), http.StatusSeeOther)
}

func (a *App) mutateLocalKubeconfigContext(response http.ResponseWriter, request *http.Request) {
	if !requireKubernetesMutation(response, request) {
		return
	}
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	name := request.PathValue("context")
	action := request.FormValue("action")
	switch action {
	case "use":
		err = kubeconfigmanager.UseContext(path, name)
	case "update":
		err = kubeconfigmanager.UpdateContext(path, name, request.FormValue("cluster"), request.FormValue("user"), request.FormValue("namespace"))
	case "rename":
		err = kubeconfigmanager.RenameContext(path, name, request.FormValue("name"))
	case "delete":
		err = kubeconfigmanager.DeleteContext(path, name)
	default:
		err = fmt.Errorf("unsupported context action %q", action)
	}
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "manage_local_kubeconfig_context", action+":"+name, "succeeded")
	http.Redirect(response, request, kubernetesLocalURL(path, action), http.StatusSeeOther)
}

func uniqueKubernetesConnectionName(contextName string, connections []clusterstatus.ConnectionStatus) string {
	used := make(map[string]bool, len(connections))
	for _, connection := range connections {
		used[strings.ToLower(strings.TrimSpace(connection.Name))] = true
	}
	base := strings.TrimSpace(contextName)
	if !used[strings.ToLower(base)] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)", base, suffix)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func (a *App) addLocalKubeconfigConnection(response http.ResponseWriter, request *http.Request) {
	if !requireKubernetesMutation(response, request) {
		return
	}
	path, err := a.managedKubeconfigPath(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	contextName := strings.TrimSpace(request.PathValue("context"))
	snapshot, err := kubeconfigmanager.Inspect(path)
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	found := false
	for _, context := range snapshot.Contexts {
		if context.Name == contextName {
			found = true
			break
		}
	}
	if !found {
		http.Error(response, "Kubernetes context was not found", http.StatusNotFound)
		return
	}
	connections, err := a.kubernetesStatus.Connections(request.Context())
	if err != nil {
		http.Error(response, "Unable to read Kubernetes connections", http.StatusInternalServerError)
		return
	}
	for _, connection := range connections {
		connectionContext := strings.TrimSpace(connection.Context)
		if connectionContext == "" {
			connectionContext = snapshot.Current
		}
		if sameKubeconfigPath(connection.KubeconfigPath, path) && connectionContext == contextName {
			http.Redirect(response, request, kubernetesLocalURL(path, "connection_exists"), http.StatusSeeOther)
			return
		}
	}
	result, err := a.kubernetesStatus.SaveConnection(request.Context(), clusterstatus.Connection{
		Name: uniqueKubernetesConnectionName(contextName, connections), KubeconfigPath: path, Context: contextName, Mode: clusterstatus.ModeObserve,
	})
	if err != nil {
		http.Error(response, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	a.recordAuditForRequest(request, "add_local_kubeconfig_connection", result.ID+":"+result.Name, "succeeded")
	if err := a.kubernetesStatus.Refresh(request.Context(), result.ID); err != nil {
		http.Error(response, "Connection saved but initial Kubernetes snapshot failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(response, request, kubernetesLocalURL(path, "connection_added"), http.StatusSeeOther)
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
