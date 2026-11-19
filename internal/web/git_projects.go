package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/gitproject"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/identity"
)

const gitProjectsURL = "/resources/git-projects"

var gitProjectsTemplate = mustWebTemplate("git-projects")

type gitProjectRow struct {
	gitproject.Project
	Status                             gitproject.Status
	State, Message, Last, DirectoryURL string
}

type gitTaskData struct {
	Locale                                   webLocale
	Kind, CSRFToken, Message, Search, Filter string
	History, CanWrite                        bool
	Action                                   gitproject.Action
	Project                                  gitProjectRow
	Projects                                 []gitProjectRow
	Runs                                     []gitproject.Run
	Run                                      gitproject.Run
}

func (data gitTaskData) Task(task, id string) string {
	params := url.Values{"task": {task}}
	if id != "" {
		params.Set("project_id", id)
	}
	return gitProjectsURL + "/task?" + params.Encode()
}

func (data gitTaskData) FilterStates() []string {
	return []string{"current", "ready", "attention", "idle", "untracked_branch", "failed"}
}

func (data gitTaskData) ActionLabel(action gitproject.Action) string {
	return webText(data.Locale, "git.action."+string(action))
}

func (data gitTaskData) StatusLabel(status string) string {
	return webText(data.Locale, "git.status."+status)
}

func (data gitTaskData) ProjectCount(count int) string {
	if count == 1 {
		return webText(data.Locale, "git.project_count_one")
	}
	return strconv.Itoa(count) + " " + webText(data.Locale, "git.projects_count")
}

func (data gitTaskData) Short(commit string) string {
	if commit == "" {
		return "—"
	}
	return commit[:min(len(commit), 7)]
}

func (data gitTaskData) RunURL(id string) string {
	return gitProjectsURL + "/runs/" + url.PathEscape(id)
}
func (data gitTaskData) RunState(run gitproject.Run) string {
	if run.FinishedAt == 0 {
		return "running"
	}
	failed, succeeded := 0, 0
	for _, entry := range run.Entries {
		switch entry.Status {
		case "succeeded":
			succeeded++
		default:
			failed++
		}
	}
	if failed == 0 {
		return "succeeded"
	}
	if succeeded > 0 {
		return "partial"
	}
	return "failed"
}

func (data gitTaskData) Time(timestamp int64) string {
	if timestamp == 0 {
		return "—"
	}
	value := time.Unix(timestamp, 0).Local()
	if data.Locale == localeSimplifiedChinese {
		return value.Format("2006年01月02日 15:04")
	}
	return value.Format("Jan 2, 2006 15:04")
}

func (a *App) renderGit(response http.ResponseWriter, request *http.Request, data gitTaskData) {
	current := request.Context().Value(sessionContextKey).(session)
	data.Locale, data.CSRFToken = resolveWebLocale(request), current.csrfToken
	response.Header().Set("Cache-Control", "no-store")
	data.CanWrite = identity.Allows(current.role, identity.PermissionWriteFiles)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = gitProjectsTemplate.Execute(response, data)
}

func (a *App) gitProjectsPage(response http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	data := gitTaskData{Kind: "list", History: request.URL.Query().Get("tab") == "history",
		Search: strings.TrimSpace(request.URL.Query().Get("q")), Filter: request.URL.Query().Get("status")}
	runs, err := gitproject.RecentRuns(ctx, a.db)
	if err != nil {
		http.Error(response, "Unable to read Git operation history", http.StatusInternalServerError)
		return
	}
	if data.History {
		data.Runs = runs
		a.renderGit(response, request, data)
		return
	}
	projects, err := gitproject.List(ctx, a.db)
	if err != nil {
		http.Error(response, "Unable to read Git projects", http.StatusInternalServerError)
		return
	}
	latest := make(map[string]int64)
	for _, run := range runs {
		for _, entry := range run.Entries {
			if _, exists := latest[entry.ProjectID]; !exists {
				latest[entry.ProjectID] = run.StartedAt
			}
		}
	}
	filter := strings.ToLower(data.Search)
	results := make([]gitProjectRow, len(projects))
	var workers sync.WaitGroup
	inspectCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for lane := 0; lane < min(len(projects), 3); lane++ {
		workers.Go(func() {
			for index := lane; index < len(projects); index += 3 {
				project := projects[index]
				row := a.gitOverview(inspectCtx, project, gitproject.Overview)
				row.Last = dataTime(resolveWebLocale(request), latest[project.ID])
				results[index] = row
			}
		})
	}
	workers.Wait()
	for _, row := range results {
		if filter != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.Directory+" "+row.Status.Branch), filter) {
			continue
		}
		if data.Filter == "" || data.Filter == row.State {
			data.Projects = append(data.Projects, row)
		}
	}
	a.renderGit(response, request, data)
}

func dataTime(locale webLocale, stamp int64) string {
	return gitTaskData{Locale: locale}.Time(stamp)
}

func (a *App) gitOverview(ctx context.Context, project gitproject.Project, action gitproject.Action) gitProjectRow {
	result, err := a.gitApply(ctx, project, gitproject.Request{ProjectID: project.ID, Action: action})
	row := gitProjectRow{Project: project, Status: result.After, State: "idle"}
	if row.Status.Remote != "" {
		row.Remote = row.Status.Remote
	}
	if err != nil {
		row.State, row.Message = "failed", err.Error()
	} else if row.Status.Cloned {
		row.DirectoryURL = routeFileURL("/resources/files", project.Directory)
		row.State = "current"
		if row.Status.Tracked > 0 || row.Status.Ahead > 0 {
			row.State = "attention"
		} else if row.Status.Behind > 0 {
			row.State = "ready"
		} else if row.Status.Upstream == "" {
			row.State = "untracked_branch"
		}
	}
	return row
}

func (a *App) gitProjectsForIDs(ctx context.Context, ids []string) ([]gitproject.Project, error) {
	if len(ids) == 0 || len(ids) > gitproject.MaxBatch {
		return nil, errors.New("select one to thirty Git projects")
	}
	seen := make(map[string]bool)
	var projects []gitproject.Project
	for _, id := range ids {
		if !gitproject.ValidID(id) {
			return nil, errors.New("invalid Git project ID")
		}
		if seen[id] {
			continue
		}
		project, err := gitproject.Get(ctx, a.db, id)
		if err != nil {
			return nil, err
		}
		seen[id] = true
		projects = append(projects, project)
	}
	return projects, nil
}

func (a *App) gitProjectTask(response http.ResponseWriter, request *http.Request) {
	task := request.URL.Query().Get("task")
	data := gitTaskData{Kind: task}
	current := request.Context().Value(sessionContextKey).(session)
	if task == "add" && identity.Allows(current.role, identity.PermissionWriteFiles) {
		a.renderGit(response, request, data)
		return
	}
	if task != "detail" && !identity.Allows(current.role, identity.PermissionWriteFiles) {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains([]string{"detail", "switch", "force", "discard"}, task) {
		http.NotFound(response, request)
		return
	}
	projects, err := a.gitProjectsForIDs(request.Context(), request.URL.Query()["project_id"])
	if err != nil || task != "force" && len(projects) != 1 {
		http.Error(response, "Git project selection is invalid", http.StatusBadRequest)
		return
	}
	for _, project := range projects {
		row := a.gitOverview(request.Context(), project, gitproject.Inspect)
		if task == "force" && row.State == "idle" {
			row.Status.Revision = gitproject.UnclonedRevision
		}
		data.Projects = append(data.Projects, row)
	}
	data.Project = data.Projects[0]
	data.Action = gitproject.Action(task)
	a.renderGit(response, request, data)
}

func (a *App) addGitProject(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	if !validSessionCSRF(request) {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	ctx := request.Context()
	directory := strings.TrimSpace(request.FormValue("directory"))
	existing := request.FormValue("mode") == "existing"
	id, err := randomToken(18)
	project := gitproject.Project{ID: id, Name: strings.TrimSpace(request.FormValue("name")), Remote: strings.TrimSpace(request.FormValue("remote")),
		Directory: directory, Branch: strings.TrimSpace(request.FormValue("branch")), SkipTLS: request.FormValue("skip_tls") == "1", Existing: existing}
	if err == nil {
		if existing {
			project.Directory, err = a.hostCanonicalDirectory(ctx, directory)
			if err == nil {
				metadata, _, infoErr := a.hostInfo(ctx, filepath.Join(project.Directory, ".git"))
				if infoErr != nil || !metadata.IsDir() {
					err = errors.New("local directory must contain a standard Git repository")
				}
			}
			project.Branch = ""
		} else {
			project.Directory, err = a.hostCanonicalDestination(ctx, directory)
			if err == nil {
				_, _, infoErr := a.hostInfo(ctx, project.Directory)
				if !errors.Is(infoErr, os.ErrNotExist) {
					err = errors.New("select a new checkout directory")
				}
			}
		}
	}
	if err == nil {
		project.PathKey = hostfiles.ComparisonKey(project.Directory)
		err = gitproject.Add(ctx, a.db, project)
	}
	if err != nil {
		a.renderGit(response, request, gitTaskData{Kind: "add", Message: err.Error(), Project: gitProjectRow{Project: project}})
		return
	}
	a.recordAuditForRequest(request, "create_git_project", project.ID, "succeeded")
	http.Redirect(response, request, gitProjectsURL, http.StatusSeeOther)
}

func (a *App) startGitProjectRun(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	if !validSessionCSRF(request) {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	action := gitproject.Action(request.PathValue("action"))
	if action == "" {
		action = gitproject.Action(filepath.Base(request.URL.Path))
	}
	destructive := action == gitproject.Force || action == gitproject.Discard
	ctx := request.Context()
	projects, err := a.gitProjectsForIDs(ctx, request.Form["project_id"])
	if err != nil || (action == gitproject.Switch || action == gitproject.Discard) && len(projects) != 1 ||
		destructive && request.FormValue("confirm") != "yes" {
		http.Error(response, "Git project selection or confirmation is invalid", http.StatusBadRequest)
		return
	}
	requests := make([]gitproject.Request, 0, len(projects))
	for _, project := range projects {
		operation := gitproject.Request{ProjectID: project.ID, Action: action}
		if action == gitproject.Switch {
			operation.Branch = request.FormValue("branch")
		}
		if destructive {
			operation.Revision = request.FormValue("revision_" + project.ID)
		}
		if !gitproject.ValidateRequest(operation) {
			http.Error(response, "Git operation is invalid", http.StatusBadRequest)
			return
		}
		requests = append(requests, operation)
	}
	run, err := a.createGitRun(ctx, action, "manual", projects)
	if err != nil {
		http.Error(response, "Unable to record Git operation", http.StatusInternalServerError)
		return
	}
	if !a.gitJobs.start(ctx, func(jobCtx context.Context) {
		err := a.gitRunPhase(jobCtx, &run, projects, requests, 0)
		_ = a.finishGitRun(jobCtx, &run)
		outcome := "succeeded"
		if err != nil {
			outcome = "failed"
		}
		a.recordAuditForRequest(request, "git_project_"+string(action), run.ID, outcome)
	}) {
		_ = a.finishGitRun(ctx, &run)
		http.Error(response, "Git operation service is shutting down", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(response, request, gitProjectsURL+"/runs/"+url.PathEscape(run.ID), http.StatusSeeOther)
}

func (a *App) gitProjectRunPage(response http.ResponseWriter, request *http.Request) {
	run, err := gitproject.ReadRun(request.Context(), a.db, request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(response, request)
		return
	}
	if err != nil {
		http.Error(response, "Unable to read Git operation", http.StatusInternalServerError)
		return
	}
	a.renderGit(response, request, gitTaskData{Kind: "run", Run: run})
}
