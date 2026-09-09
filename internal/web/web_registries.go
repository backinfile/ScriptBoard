package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"scriptboard/internal/registrymonitor"
)

type registryPageData struct {
	Locale       webLocale
	CSRFToken    string
	Connections  []registrymonitor.ManagedConnection
	Connection   registrymonitor.ManagedConnection
	Repositories []string
	Namespaces   []string
	Query        string
	Namespace    string
	Tab          string
	Task         string
	BackURL      string
	Error        string
	Artifacts    []registrymonitor.Artifact
	Repository   string
	Plan         *registrymonitor.DeletePlan
	Events       []registrymonitor.ManagementEvent
	Results      []registrymonitor.DeleteResult
}

func (a *App) registryBackend() (registrymonitor.ManagementBackend, error) {
	backend, ok := a.registryConnections.(registrymonitor.ManagementBackend)
	if !ok {
		return nil, errors.New("Registry management is unavailable")
	}
	return backend, nil
}
func registryURL(id string) string { return "/resources/registries?connection=" + url.QueryEscape(id) }
func (a *App) registryView(request *http.Request) (registryPageData, error) {
	current := request.Context().Value(sessionContextKey).(session)
	view := registryPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: request.URL.Query().Get("query"), Namespace: request.URL.Query().Get("namespace"), Tab: request.URL.Query().Get("tab"), Task: request.URL.Query().Get("task"), Repository: request.URL.Query().Get("repository")}
	backend, err := a.registryBackend()
	if err != nil {
		return view, err
	}
	out, err := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "list"})
	if err != nil {
		return view, err
	}
	view.Connections = out.Connections
	id := request.URL.Query().Get("connection")
	if id == "" && len(view.Connections) > 0 {
		id = view.Connections[0].ID
	}
	for _, c := range view.Connections {
		if c.ID == id {
			view.Connection = c
		}
	}
	if id != "" && view.Connection.ID == "" {
		return view, errors.New("Registry connection not found")
	}
	view.BackURL = registryURL(view.Connection.ID)
	return view, nil
}
func renderRegistry(response http.ResponseWriter, view registryPageData, status int) {
	var body bytes.Buffer
	if err := registriesTemplate.Execute(&body, view); err != nil {
		http.Error(response, "Cannot render registry page", 500)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write(body.Bytes())
}
func (a *App) registriesPage(response http.ResponseWriter, request *http.Request) {
	view, err := a.registryView(request)
	if err != nil {
		view.Error = err.Error()
		renderRegistry(response, view, 502)
		return
	}
	backend, _ := a.registryBackend()
	id := view.Connection.ID
	if request.URL.Path == "/resources/registries/task" {
		switch view.Task {
		case "new":
			view.Connection = registrymonitor.ManagedConnection{Config: registrymonitor.Config{AuthMode: "anonymous"}}
		case "edit", "cleanup", "remove":
		case "detail":
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "detail", ID: id, Repository: view.Repository})
			view.Artifacts, err = out.Artifacts, e
		case "plan":
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "plan", ID: id, PlanID: request.URL.Query().Get("plan")})
			view.Plan, err = out.Plan, e
		default:
			err = errors.New("unknown registry task")
		}
	} else {
		view.Task = ""
		if id != "" {
			command := "catalog"
			if view.Tab == "history" {
				command = "history"
			}
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: command, ID: id})
			err = e
			view.Events = out.Events
			namespaces := map[string]bool{}
			for _, repo := range out.Repositories {
				if index := strings.Index(repo, "/"); index >= 0 {
					namespaces[repo[:index+1]] = true
				}
				if strings.HasPrefix(repo, view.Namespace) && strings.Contains(strings.ToLower(repo), strings.ToLower(view.Query)) {
					view.Repositories = append(view.Repositories, repo)
				}
			}
			for ns := range namespaces {
				view.Namespaces = append(view.Namespaces, ns)
			}
			sort.Strings(view.Namespaces)
			sort.Strings(view.Repositories)
		}
	}
	if err != nil {
		view.Error = err.Error()
	}
	renderRegistry(response, view, 200)
}

func (a *App) registryMutation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF validation failed", 403)
		return
	}
	backend, err := a.registryBackend()
	if err != nil {
		http.Error(response, err.Error(), 503)
		return
	}
	command := request.PathValue("command")
	req := registrymonitor.ManagementRequest{Command: command, ID: request.FormValue("connection"), Name: request.FormValue("name"), Repository: request.FormValue("repository"), Repositories: request.Form["repositories"], Tag: request.FormValue("tag"), PlanID: request.FormValue("plan"), Confirmation: request.FormValue("confirmation")}
	if command != "save" && command != "remove" && command != "preview" && command != "execute" {
		http.Error(response, "Invalid command", 400)
		return
	}
	if command == "save" {
		req.Config = registrymonitor.Config{Endpoint: request.FormValue("endpoint"), Images: []string{"*"}, Username: request.FormValue("username"), AuthMode: request.FormValue("auth_mode"), SkipTLSVerify: request.FormValue("skip_tls_verify") == "1"}
		req.Password = request.FormValue("password")
		req.Preserve = req.ID != "" && req.Password == ""
	}
	if command == "preview" {
		req.Rule.Prefix = strings.TrimSpace(request.FormValue("prefix"))
		req.Rule.Pattern = request.FormValue("pattern")
		req.Rule.Protect = request.FormValue("protect") == "1"
		for name, destination := range map[string]*int{"older_days": &req.Rule.OlderDays, "keep": &req.Rule.Keep} {
			value := request.FormValue(name)
			if value != "" {
				parsed, e := strconv.Atoi(value)
				if e != nil {
					http.Error(response, "Invalid retention value", 422)
					return
				}
				*destination = parsed
			}
		}
	}
	out, err := backend.Manage(request.Context(), req)
	if err != nil {
		current := request.Context().Value(sessionContextKey).(session)
		view := registryPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Task: "error", Error: err.Error(), BackURL: registryURL(req.ID)}
		if command == "save" {
			view.Task = "edit"
			view.Connection = registrymonitor.ManagedConnection{ID: req.ID, Name: req.Name, Config: req.Config}
		}
		renderRegistry(response, view, 422)
		return
	}
	a.recordAuditForRequest(request, "registry_"+command, req.ID, "completed")
	if command == "preview" {
		http.Redirect(response, request, "/resources/registries/task?task=plan&connection="+url.QueryEscape(req.ID)+"&plan="+url.QueryEscape(out.Plan.ID), 303)
		return
	}
	if command == "execute" {
		current := request.Context().Value(sessionContextKey).(session)
		renderRegistry(response, registryPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Task: "result", Results: out.Results, BackURL: registryURL(req.ID)}, 200)
		return
	}
	if command == "save" {
		req.ID = out.ID
	}
	if command == "remove" {
		req.ID = ""
	}
	http.Redirect(response, request, registryURL(req.ID), 303)
}
