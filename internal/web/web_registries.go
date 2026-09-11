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
	Namespaces   []*registryNamespace
	Images       map[string]registrymonitor.ImageResult
	Total        int
	PreviousURL  string
	NextURL      string
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
			view.Connection = registrymonitor.ManagedConnection{Config: registrymonitor.Config{AuthMode: "anonymous", ReadOnly: true}}
		case "edit", "remove":
		case "cleanup":
			if view.Connection.Config.ReadOnly {
				view.Task = "error"
				err = errors.New("Registry connection is read-only")
			}
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

			for _, repo := range out.Repositories {
				if strings.HasPrefix(repo, view.Namespace) && strings.Contains(strings.ToLower(repo), strings.ToLower(view.Query)) {
					view.Repositories = append(view.Repositories, repo)
				}
			}
			view.Namespaces = registryNamespaceTree(out.Repositories, view.Namespace, view.BackURL)
			sort.Strings(view.Repositories)
			view.Total = len(view.Repositories)
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			pages := (view.Total + 19) / 20
			if page < 1 || page > pages {
				page = 1
			}
			start := (page - 1) * 20
			view.Repositories = view.Repositories[start:min(start+20, view.Total)]
			pageURL := view.BackURL + "&namespace=" + url.QueryEscape(view.Namespace) + "&query=" + url.QueryEscape(view.Query)
			if page > 1 {
				view.PreviousURL = pageURL + "&page=" + strconv.Itoa(page-1)
			}
			if page < pages {
				view.NextURL = pageURL + "&page=" + strconv.Itoa(page+1)
			}
			if command == "catalog" && len(view.Repositories) > 0 {
				summary, summaryErr := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "summary", ID: id, Repositories: view.Repositories})
				view.Images = map[string]registrymonitor.ImageResult{}
				for _, name := range view.Repositories {
					item := registrymonitor.ImageResult{Image: name}
					if summaryErr != nil {
						item.Error = summaryErr.Error()
					}
					view.Images[name] = item
				}
				for _, item := range summary.Images {
					view.Images[item.Image] = item
				}
				if summaryErr != nil {
					err = summaryErr
				}
			}
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
		req.Config = registrymonitor.Config{ReadOnly: request.FormValue("access_mode") != "write", Endpoint: request.FormValue("endpoint"), Images: []string{"*"}, Username: request.FormValue("username"), AuthMode: request.FormValue("auth_mode"), SkipTLSVerify: request.FormValue("skip_tls_verify") == "1"}
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

// Preserve every namespace segment and its slash boundary when filtering descendants.
type registryNamespace struct {
	Name, Path, URL string
	Selected, Open  bool
	Children        []*registryNamespace
}

func registryNamespaceTree(repositories []string, selected, base string) []*registryNamespace {
	root := &registryNamespace{}
	for _, repo := range repositories {
		parts := strings.Split(repo, "/")
		parent := root
		prefix := ""
		for _, part := range parts[:len(parts)-1] {
			prefix += part + "/"
			var node *registryNamespace
			for _, child := range parent.Children {
				if child.Path == prefix {
					node = child
					break
				}
			}
			if node == nil {
				node = &registryNamespace{Name: part, Path: prefix, URL: base + "&namespace=" + url.QueryEscape(prefix), Selected: selected == prefix, Open: strings.HasPrefix(selected, prefix)}
				parent.Children = append(parent.Children, node)
			}
			parent = node
		}
	}
	var sortNodes func(*registryNamespace)
	sortNodes = func(node *registryNamespace) {
		sort.Slice(node.Children, func(i, j int) bool { return node.Children[i].Name < node.Children[j].Name })
		for _, child := range node.Children {
			sortNodes(child)
		}
	}
	sortNodes(root)
	return root.Children
}
