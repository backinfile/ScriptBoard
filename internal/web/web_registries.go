package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/registrymonitor"
)

type registryPageData struct {
	Diagnostic, ExistingURL, ExistingName               string
	NamespaceCounts                                     map[string]int
	Locale                                              webLocale
	CSRFToken                                           string
	Connections                                         []registrymonitor.ManagedConnection
	Connection                                          registrymonitor.ManagedConnection
	Repositories                                        []string
	Namespaces                                          []*registryNamespace
	Images                                              map[string]registrymonitor.ImageResult
	Total                                               int
	PreviousURL, NextURL                                string
	Query, Namespace, Tab, Task, BackURL, Error, Notice string
	Artifacts                                           []registrymonitor.Artifact
	Repository                                          string
	Plan                                                *registrymonitor.DeletePlan
	Events                                              []registrymonitor.ManagementEvent
	Results                                             []registrymonitor.DeleteResult
	Selection                                           registrymonitor.CleanupSelection
	OlderDays, Keep, Confirmation                       string
	EditURL                                             string
	ReadFailed, RefreshOnClose, Previewed               bool
	ReadAt                                              time.Time
}

func registryBytes(n int64) string {
	if n <= 0 {
		return "—"
	}
	value := float64(n)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}
func registryURL(id string) string { return "/resources/registries?connection=" + url.QueryEscape(id) }

// Preserve only local registry filter parameters in return links.
func registryReturn(raw, id string) string {
	u, err := url.Parse(raw)
	q := url.Values{"connection": {id}}
	if err == nil && u.Scheme == "" && u.Host == "" && u.Path == "/resources/registries" {
		for _, key := range []string{"namespace", "query", "tab", "page"} {
			if value := u.Query().Get(key); value != "" {
				q.Set(key, value)
			}
		}
	}
	return "/resources/registries?" + q.Encode()
}

// Recovery links carry only editable cleanup conditions, never credentials or authority.
func registryRecovery(raw, id, back string) string {
	u, err := url.Parse(raw)
	v := registryPageData{Connection: registrymonitor.ManagedConnection{ID: id}, BackURL: back}
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path != "/resources/registries/task" {
		return v.TaskLink("cleanup", "")
	}
	source := u.Query()
	q := url.Values{"task": {"cleanup"}, "connection": {id}, "return": {back}, "configured": {"1"}}
	for _, key := range []string{"repository", "repositories", "tag", "prefix", "pattern", "older_days", "keep", "protect"} {
		for _, value := range source[key] {
			q.Add(key, value)
		}
	}
	return "/resources/registries/task?" + q.Encode()
}
func (v registryPageData) TaskLink(task, repository string) string {
	q := url.Values{"task": {task}, "connection": {v.Connection.ID}, "return": {v.BackURL}, "namespace": {v.Namespace}}
	if repository != "" {
		q.Set("repository", repository)
	}
	return "/resources/registries/task?" + q.Encode()
}
func (v registryPageData) FilterLink(namespace string) string {
	q := url.Values{"connection": {v.Connection.ID}, "namespace": {namespace}, "query": {v.Query}}
	return "/resources/registries?" + q.Encode()
}
func (v registryPageData) HistoryLink() string {
	u, _ := url.Parse(v.BackURL)
	q := u.Query()
	q.Set("tab", "history")
	u.RawQuery = q.Encode()
	return u.String()
}
func (v registryPageData) ResetLink() string {
	q := url.Values{"connection": {v.Connection.ID}, "namespace": {v.Namespace}}
	return "/resources/registries?" + q.Encode()
}
func (v registryPageData) PullReference(repository, tag string) string {
	u, err := url.Parse(v.Connection.Config.Endpoint)
	if err != nil {
		return repository + ":" + tag
	}
	return u.Host + strings.TrimRight(u.Path, "/") + "/" + repository + ":" + tag
}
func (v registryPageData) PlanTags() int {
	n := 0
	if v.Plan != nil {
		for _, target := range v.Plan.Targets {
			n += len(target.Tags)
		}
	}
	return n
}
func (v registryPageData) PlanRepositories() int {
	seen := map[string]bool{}
	if v.Plan != nil {
		for _, target := range v.Plan.Targets {
			seen[target.Repository] = true
		}
	}
	return len(seen)
}
func (v registryPageData) PlanExpires() time.Time {
	if v.Plan == nil {
		return time.Time{}
	}
	return v.Plan.Created.Add(10 * time.Minute)
}
func (v registryPageData) SuccessCount(results []registrymonitor.DeleteResult) int {
	n := 0
	for _, r := range results {
		if r.Error == "" {
			n++
		}
	}
	return n
}
func (v registryPageData) FailureCount(results []registrymonitor.DeleteResult) int {
	return len(results) - v.SuccessCount(results)
}
func (v registryPageData) EventStatus(event registrymonitor.ManagementEvent) string {
	if strings.HasPrefix(event.Summary, "Deletion completed") {
		return webText(v.Locale, "registry.completed")
	}
	return webText(v.Locale, "registry.incomplete")
}
func (v registryPageData) EventName(kind string) string {
	switch kind {
	case "tag", "repository", "selection", "rule":
		return webText(v.Locale, "registry.kind_"+kind)
	}
	return webText(v.Locale, "registry.history")
}
func (v registryPageData) SkipName(reason string) string {
	return webText(v.Locale, "registry.skip_"+reason)
}
func (v *registryPageData) usePlan(plan *registrymonitor.DeletePlan) {
	v.Plan = plan
	if plan == nil {
		return
	}
	v.Selection = plan.Selection
	v.OlderDays = strconv.Itoa(plan.Selection.Rule.OlderDays)
	v.Keep = strconv.Itoa(plan.Selection.Rule.Keep)
	v.EditURL = v.selectionURL()
}
func (v registryPageData) selectionURL() string {
	u, _ := url.Parse(v.TaskLink("cleanup", ""))
	q := u.Query()
	s := v.Selection
	q.Set("prefix", s.Rule.Prefix)
	q.Set("pattern", s.Rule.Pattern)
	q.Set("older_days", strconv.Itoa(s.Rule.OlderDays))
	q.Set("keep", strconv.Itoa(s.Rule.Keep))
	q.Set("protect", strconv.FormatBool(s.Rule.Protect))
	q.Set("configured", "1")
	q.Set("repository", s.Repository)
	q.Set("tag", s.Tag)
	for _, r := range s.Repositories {
		q.Add("repositories", r)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func registryError(locale webLocale, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	key := "request_failed"
	switch {
	case msg == "registry connection is read-only":
		key = "readonly_hint"
	case msg == "registry URL already exists":
		key = "duplicate"
	case msg == "confirmation mismatch":
		key = "confirm_error"
	case strings.Contains(msg, "preview expired"), strings.Contains(msg, "repository changed"):
		key = "preview_stale"
	case strings.Contains(msg, "select repositories or enter"):
		key = "select_first"
	case strings.Contains(msg, "syntax error in pattern"):
		key = "pattern_error"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		key = "auth_error"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "x509"):
		key = "certificate_error"
	case strings.Contains(msg, "invalid"):
		key = "invalid_input"
	}
	return webText(locale, "registry."+key)
}
func (a *App) registryBackend() (registrymonitor.ManagementBackend, error) {
	backend, ok := a.registryConnections.(registrymonitor.ManagementBackend)
	if !ok {
		return nil, errors.New("Registry management is unavailable")
	}
	return backend, nil
}
func (a *App) registryView(request *http.Request) (registryPageData, error) {
	current := request.Context().Value(sessionContextKey).(session)
	q := request.URL.Query()
	v := registryPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, Query: q.Get("query"), Namespace: q.Get("namespace"), Tab: q.Get("tab"), Task: q.Get("task"), Repository: q.Get("repository"), OlderDays: "0", Keep: "0"}
	backend, err := a.registryBackend()
	if err != nil {
		return v, err
	}
	out, err := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "list"})
	if err != nil {
		return v, err
	}
	v.Connections = out.Connections
	id := q.Get("connection")
	if id == "" && len(v.Connections) > 0 {
		id = v.Connections[0].ID
	}
	for _, c := range v.Connections {
		if c.ID == id {
			v.Connection = c
		}
	}
	v.BackURL = registryReturn(request.URL.RequestURI(), id)
	if q.Get("return") != "" {
		v.BackURL = registryReturn(q.Get("return"), id)
		u, _ := url.Parse(v.BackURL)
		v.Query = u.Query().Get("query")
		v.Namespace = u.Query().Get("namespace")
	}
	if id != "" && v.Connection.ID == "" {
		return v, errors.New("Registry connection not found")
	}
	return v, nil
}
func renderRegistry(response http.ResponseWriter, v registryPageData, status int) {
	var body bytes.Buffer
	if err := registriesTemplate.Execute(&body, v); err != nil {
		http.Error(response, "Cannot render registry page", 500)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write(body.Bytes())
}
func (a *App) registriesPage(response http.ResponseWriter, request *http.Request) {
	v, err := a.registryView(request)
	if err != nil {
		v.Error = registryError(v.Locale, err)
		v.ReadFailed = true
		renderRegistry(response, v, 502)
		return
	}
	backend, _ := a.registryBackend()
	id := v.Connection.ID
	q := request.URL.Query()
	if request.URL.Path == "/resources/registries/task" {
		if v.Connection.Config.ReadOnly && (v.Task == "cleanup" || v.Task == "plan") {
			v.Task = "readonly"
			v.Notice = webText(v.Locale, "registry.readonly_hint")
			renderRegistry(response, v, 200)
			return
		}
		switch v.Task {
		case "new":
			v.Connection = registrymonitor.ManagedConnection{Config: registrymonitor.Config{AuthMode: "anonymous", ReadOnly: true}}
		case "edit", "remove":
		case "cleanup":
			v.Selection = registrymonitor.CleanupSelection{Repository: q.Get("repository"), Repositories: q["repositories"], Tag: q.Get("tag"), Rule: registrymonitor.CleanupRule{Prefix: v.Namespace, Protect: true}}
			if q.Get("configured") == "1" {
				v.Selection.Rule.Prefix = q.Get("prefix")
				v.Selection.Rule.Pattern = q.Get("pattern")
				v.Selection.Rule.Protect = q.Get("protect") == "true"
				v.OlderDays = q.Get("older_days")
				v.Keep = q.Get("keep")
				v.Selection.Rule.OlderDays, _ = strconv.Atoi(v.OlderDays)
				v.Selection.Rule.Keep, _ = strconv.Atoi(v.Keep)
			}
		case "summary", "detail":
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "detail", ID: id, Repository: v.Repository})
			err = e
			if v.Task == "summary" {
				response.Header().Set("Cache-Control", "no-store")
				response.Header().Set("Content-Type", "application/json")
				if e != nil {
					response.WriteHeader(502)
					_ = json.NewEncoder(response).Encode(map[string]string{"error": registryError(v.Locale, e)})
				} else {
					_ = json.NewEncoder(response).Encode(out.Artifacts)
				}
				return
			}
			v.Artifacts = out.Artifacts
			v.ReadFailed = e != nil
		case "plan":
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "plan", ID: id, PlanID: q.Get("plan")})
			err = e
			v.usePlan(out.Plan)
			if e != nil {
				v.EditURL = registryRecovery(q.Get("edit"), id, v.BackURL)
			}
		default:
			err = errors.New("unknown registry task")
		}
	} else {
		v.Task = ""
		if id != "" {
			command := "catalog"
			if v.Tab == "history" {
				command = "history"
			}
			out, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: command, ID: id})
			err = e
			v.ReadFailed = e != nil
			v.ReadAt = time.Now()
			v.Events = out.Events
			for _, repo := range out.Repositories {
				if strings.HasPrefix(repo, v.Namespace) && strings.Contains(strings.ToLower(repo), strings.ToLower(v.Query)) {
					v.Repositories = append(v.Repositories, repo)
				}
			}
			v.Namespaces = registryNamespaceTree(out.Repositories, v.Namespace, v.ResetLink())
			sort.Strings(v.Repositories)
			v.Total = len(v.Repositories)
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			pages := (v.Total + 19) / 20
			if page < 1 || page > pages {
				page = 1
			}
			start := (page - 1) * 20
			v.Repositories = v.Repositories[start:min(start+20, v.Total)]
			pageURL := v.BackURL + "&namespace=" + url.QueryEscape(v.Namespace) + "&query=" + url.QueryEscape(v.Query)
			if page > 1 {
				v.PreviousURL = pageURL + "&page=" + strconv.Itoa(page-1)
			}
			if page < pages {
				v.NextURL = pageURL + "&page=" + strconv.Itoa(page+1)
			}
			if command == "catalog" && len(v.Repositories) > 0 {
				summary, summaryErr := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "summary", ID: id, Repositories: v.Repositories})
				v.Images = map[string]registrymonitor.ImageResult{}
				for _, name := range v.Repositories {
					item := registrymonitor.ImageResult{Image: name}
					if summaryErr != nil {
						item.Error = summaryErr.Error()
					}
					v.Images[name] = item
				}
				for _, item := range summary.Images {
					v.Images[item.Image] = item
				}
				if summaryErr != nil {
					err = summaryErr
				}
			}
		}
		if q.Get("saved") == "1" {
			v.Notice = webText(v.Locale, "registry.saved")
		}
	}
	v.Error = registryError(v.Locale, err)
	if err != nil {
		v.Diagnostic = err.Error()
	}
	renderRegistry(response, v, 200)
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
	if command != "save" && command != "remove" && command != "preview" && command != "execute" && command != "test" {
		http.Error(response, "Invalid command", 400)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	req := registrymonitor.ManagementRequest{Command: command, ID: request.FormValue("connection"), Name: request.FormValue("name"), Repository: request.FormValue("repository"), Repositories: request.Form["repositories"], Tag: request.FormValue("tag"), PlanID: request.FormValue("plan"), Confirmation: request.FormValue("confirmation"), Actor: current.username}
	v := registryPageData{Locale: resolveWebLocale(request), CSRFToken: current.csrfToken, BackURL: registryReturn(request.FormValue("return"), req.ID), Repository: req.Repository, Confirmation: req.Confirmation, OlderDays: request.FormValue("older_days"), Keep: request.FormValue("keep")}
	u, _ := url.Parse(v.BackURL)
	v.Namespace = u.Query().Get("namespace")
	v.Query = u.Query().Get("query")
	listed, _ := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "list"})
	v.Connections = listed.Connections
	for _, c := range listed.Connections {
		if c.ID == req.ID {
			v.Connection = c
		}
	}
	if command == "save" || command == "test" {
		mode := request.FormValue("access_mode")
		if mode != "" && mode != "read" && mode != "write" && mode != "readonly" && mode != "writable" {
			http.Error(response, "Invalid access mode", 422)
			return
		}
		// Missing mode is read-only for new connections; edits retain the persisted choice.
		readOnly := true
		if mode == "" && req.ID != "" {
			readOnly = v.Connection.Config.ReadOnly
		}
		if mode != "" {
			readOnly = mode == "read" || mode == "readonly"
		}
		req.Config = registrymonitor.Config{ReadOnly: readOnly, Endpoint: request.FormValue("endpoint"), Images: []string{"*"}, Username: request.FormValue("username"), AuthMode: request.FormValue("auth_mode"), SkipTLSVerify: request.FormValue("skip_tls_verify") == "1"}
		req.Password = request.FormValue("password")
		req.Preserve = req.ID != "" && req.Password == ""
		v.Connection = registrymonitor.ManagedConnection{ID: req.ID, Name: req.Name, Config: req.Config}
	}
	req.Rule = registrymonitor.CleanupRule{Prefix: strings.TrimSpace(request.FormValue("prefix")), Pattern: request.FormValue("pattern"), Protect: request.FormValue("protect") == "1"}
	for name, dest := range map[string]*int{"older_days": &req.Rule.OlderDays, "keep": &req.Rule.Keep} {
		raw := request.FormValue(name)
		if raw != "" {
			*dest, err = strconv.Atoi(raw)
			if err != nil {
				err = errors.New("invalid retention value")
				break
			}
		}
	}
	v.Selection = registrymonitor.CleanupSelection{Repository: req.Repository, Repositories: req.Repositories, Tag: req.Tag, Rule: req.Rule}
	var out registrymonitor.ManagementResponse
	if command == "execute" {
		prior, e := backend.Manage(request.Context(), registrymonitor.ManagementRequest{Command: "plan", ID: req.ID, PlanID: req.PlanID})
		if e == nil {
			v.usePlan(prior.Plan)
		} else {
			v.EditURL = v.selectionURL()
		}
	}
	if err == nil {
		out, err = backend.Manage(request.Context(), req)
	}
	if err != nil {
		v.Error = registryError(v.Locale, err)
		if err.Error() == "registry connection is read-only" {
			v.Task = "readonly"
			renderRegistry(response, v, 403)
			return
		}
		v.Diagnostic = err.Error()
		if err.Error() == "registry URL already exists" && out.ID != "" {
			v.ExistingURL = registryURL(out.ID)
			for _, c := range listed.Connections {
				if c.ID == out.ID {
					v.ExistingName = c.Name
				}
			}
		}
		switch command {
		case "save", "test":
			v.Task = "edit"
		case "execute":
			if len(out.Results) > 0 {
				v.Task = "result"
				v.Results = out.Results
				v.RefreshOnClose = true
				renderRegistry(response, v, 422)
				return
			}
			v.Task = "plan"
			if err.Error() != "confirmation mismatch" {
				v.Plan = nil
			}
		case "preview":
			v.Task = "cleanup"
		default:
			v.Task = "remove"
		}
		if v.Task == "cleanup" && req.Repository == "" && len(req.Repositories) == 0 && req.Rule.Prefix == "" {
			v.Selection.Rule.Prefix = v.Namespace
		}
		renderRegistry(response, v, 422)
		return
	}
	if command == "test" {
		v.Task = "edit"
		v.Notice = webText(v.Locale, "registry.test_ok")
		renderRegistry(response, v, 200)
		return
	}
	a.recordAuditForRequest(request, "registry_"+command, req.ID, "completed")
	if command == "preview" {
		v.Task = "plan"
		v.Previewed = true
		v.usePlan(out.Plan)
		if out.Plan != nil && len(out.Plan.Targets) > 0 {
			http.Redirect(response, request, "/resources/registries/task?task=plan&connection="+url.QueryEscape(req.ID)+"&plan="+url.QueryEscape(out.Plan.ID)+"&return="+url.QueryEscape(v.BackURL)+"&edit="+url.QueryEscape(v.EditURL), 303)
			return
		}
		renderRegistry(response, v, 200)
		return
	}
	if command == "execute" {
		v.Task = "result"
		v.Results = out.Results
		v.RefreshOnClose = true
		renderRegistry(response, v, 200)
		return
	}
	if command == "save" {
		req.ID = out.ID
	}
	if command == "remove" {
		req.ID = ""
	}
	destination := registryReturn(v.BackURL, req.ID)
	if command == "save" {
		destination += "&saved=1"
	}
	http.Redirect(response, request, destination, 303)
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
