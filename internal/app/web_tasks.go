package app

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/scheduler"
)

type taskPageData struct {
	Locale             webLocale
	Kind               string
	Title              string
	Description        string
	BackURL            string
	Action             string
	CSRFToken          string
	Path               string
	Name               string
	Value              string
	Script             string
	Arguments          string
	Expression         string
	TimeoutSeconds     int
	IsPassword         bool
	DisallowOverlap    bool
	GroupID            string
	Groups             []quickRunGroup
	ScheduleGroupID    string
	ScheduleGroups     []scheduleGroup
	ReturnTo           string
	PreviewAction      string
	TimeoutInput       string
	CronPreview        scheduleCronPreviewPayload
	CronError          string
	Error              string
	Source             string
	WorkingDirectory   string
	FileName           string
	Languages          []scriptLanguageOption
	Language           string
	Conflict           bool
	ConflictPath       string
	SuggestedName      string
	CanOverwrite       bool
	QuickReferences    int
	ScheduleReferences int
	User               userView
}

func (a *App) renderTaskPage(response http.ResponseWriter, request *http.Request, data taskPageData) {
	a.renderTaskPageStatus(response, request, http.StatusOK, data)
}

func (a *App) renderTaskPageStatus(response http.ResponseWriter, request *http.Request, status int, data taskPageData) {
	current := request.Context().Value(sessionContextKey).(session)
	data.Locale = resolveWebLocale(request)
	data.CSRFToken = current.csrfToken
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = taskPageTemplate.Execute(response, data)
}

func (a *App) newDirectoryTask(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	if relative == "" {
		http.Error(response, "请先进入一个主机目录", http.StatusBadRequest)
		return
	}
	if _, err := a.files.List(relative); err != nil {
		writeHostFileError(response, "无法打开新建目录任务", err)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "new-directory", Title: webText(resolveWebLocale(request), "task.new_directory.title"),
		Description: webText(resolveWebLocale(request), "task.new_directory.description"),
		BackURL:     filesURL(relative), Action: "/resources/files/mkdir", Path: relative,
	})
}

func (a *App) uploadTask(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	if relative == "" {
		http.Error(response, "请先进入一个主机目录", http.StatusBadRequest)
		return
	}
	if _, err := a.files.List(relative); err != nil {
		writeHostFileError(response, "无法打开上传任务", err)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "upload", Title: webText(resolveWebLocale(request), "task.upload.title"),
		Description: webText(resolveWebLocale(request), "task.upload.description"),
		BackURL:     filesURL(relative), Action: "/resources/files/upload", Path: relative,
	})
}

func (a *App) moveFileTask(response http.ResponseWriter, request *http.Request) {
	path, err := a.files.CanonicalExisting(request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法打开移动任务", err)
		return
	}
	if !a.files.CanMutate(path) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	parent, ok := hostPathParent(path)
	if !ok {
		http.Error(response, "filesystem roots cannot be moved", http.StatusBadRequest)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "move-file", Title: webText(resolveWebLocale(request), "task.move_file.title"),
		Description: webText(resolveWebLocale(request), "task.move_file.description"),
		BackURL:     filesURL(parent), Action: "/resources/files/move", Path: path,
		Name: hostfiles.Base(path), WorkingDirectory: parent,
	})
}

func (a *App) runFileTask(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	info, err := a.files.Info(relative)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(response, "Script not found", http.StatusNotFound)
		return
	}
	parent, _ := hostPathParent(relative)
	a.renderTaskPage(response, request, taskPageData{
		Kind: "run", Title: webText(resolveWebLocale(request), "task.run.title"),
		Description: webText(resolveWebLocale(request), "task.run.description"),
		BackURL:     filesURL(parent), Action: "/history/runs/start", Path: relative,
	})
}

func (a *App) quickRunFromFileTask(response http.ResponseWriter, request *http.Request) {
	relative := request.URL.Query().Get("path")
	info, err := a.files.Info(relative)
	if err != nil || !info.Mode().IsRegular() || !isScriptExtension(relative) {
		http.Error(response, "Script not found", http.StatusNotFound)
		return
	}
	parent, _ := hostPathParent(relative)
	backURL := safeFilesReturnTo(request.URL.Query().Get("return_to"))
	if backURL == "" {
		backURL = filesURL(parent)
	}
	name := strings.TrimSuffix(hostfiles.Base(relative), hostfiles.Extension(relative))
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "Unable to read Quick Run groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "quick-new", Title: webText(resolveWebLocale(request), "task.quick_new.title"),
		Description: webText(resolveWebLocale(request), "task.quick_new.description"),
		BackURL:     backURL, Action: "/config/quick-runs", Path: relative, Name: name, ReturnTo: backURL, Groups: groups,
	})
}

func safeFilesReturnTo(value string) string {
	candidate, err := url.Parse(value)
	if err != nil || candidate.IsAbs() || candidate.Host != "" || candidate.Path == "" {
		return ""
	}
	if candidate.Path != "/resources/files" {
		return ""
	}
	return candidate.RequestURI()
}

func (a *App) newVariableTask(response http.ResponseWriter, request *http.Request) {
	a.renderTaskPage(response, request, taskPageData{
		Kind: "variable-new", Title: webText(resolveWebLocale(request), "task.variable_new.title"),
		Description: webText(resolveWebLocale(request), "task.variable_description"),
		BackURL:     "/resources/variables", Action: "/resources/variables",
	})
}

func (a *App) saveQuickRunTask(response http.ResponseWriter, request *http.Request) {
	run, err := a.runs.Get(request.PathValue("id"))
	if err != nil {
		http.Error(response, "Run not found", http.StatusNotFound)
		return
	}
	if run.ScriptKind == "one_time" {
		http.Error(response, "One-time Runs cannot be saved directly as Quick Runs", http.StatusConflict)
		return
	}
	backURL := "/history/runs/" + url.PathEscape(run.ID)
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "Unable to read Quick Run groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "quick-save", Title: webText(resolveWebLocale(request), "run_detail.save_quick"),
		Description: webText(resolveWebLocale(request), "task.quick_save.description"),
		BackURL:     backURL, Action: backURL + "/quick-run", Path: run.ScriptPath, Groups: groups,
	})
}

func (a *App) editVariableTask(response http.ResponseWriter, request *http.Request) {
	var name, value string
	var isPassword bool
	if err := a.db.QueryRow("SELECT name, value, is_password FROM variables WHERE name = ?", request.PathValue("name")).Scan(&name, &value, &isPassword); err != nil {
		if err == sql.ErrNoRows {
			http.Error(response, "Variable not found", http.StatusNotFound)
			return
		}
		http.Error(response, "Unable to read variable", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "variable-edit", Title: webText(resolveWebLocale(request), "task.variable_edit.title"),
		Description: webText(resolveWebLocale(request), "task.variable_description"),
		BackURL:     "/resources/variables", Action: "/resources/variables/" + url.PathEscape(name) + "/update",
		Name: name, Value: value, IsPassword: isPassword,
	})
}

func (a *App) newScheduleTask(response http.ResponseWriter, request *http.Request) {
	groups, err := a.loadScheduleGroups()
	if err != nil {
		http.Error(response, "Unable to read Schedule groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "schedule-new", Title: webText(resolveWebLocale(request), "task.schedule_new.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_description"),
		BackURL:     "/config/schedules", Action: "/config/schedules", PreviewAction: "/config/schedules/preview",
		Expression: "0 2 * * *", ScheduleGroups: groups,
	})
}

func (a *App) editScheduleTask(response http.ResponseWriter, request *http.Request) {
	var selected scheduler.Schedule
	schedules, err := a.scheduler.List()
	if err == nil {
		for _, candidate := range schedules {
			if candidate.ID == request.PathValue("id") {
				selected = candidate
				break
			}
		}
	}
	if err != nil || selected.ID == "" {
		http.Error(response, "Schedule not found", http.StatusNotFound)
		return
	}
	groups, err := a.loadScheduleGroups()
	if err != nil {
		http.Error(response, "Unable to read Schedule groups", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "schedule-edit", Title: webText(resolveWebLocale(request), "task.schedule_edit.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_description"),
		BackURL:     "/config/schedules", Action: "/config/schedules/" + url.PathEscape(selected.ID) + "/update",
		PreviewAction: "/config/schedules/" + url.PathEscape(selected.ID) + "/preview",
		Name:          selected.Name, Script: selected.ScriptPath, Arguments: selected.ArgumentsTemplate,
		Expression: selected.Expression, TimeoutSeconds: selected.TimeoutSeconds, DisallowOverlap: !selected.AllowOverlap,
		ScheduleGroupID: selected.GroupID, ScheduleGroups: groups,
	})
}
