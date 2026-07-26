package app

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"scriptboard/internal/scheduler"
)

type taskPageData struct {
	Locale          webLocale
	Kind            string
	Title           string
	Description     string
	BackURL         string
	Action          string
	CSRFToken       string
	Path            string
	Name            string
	Value           string
	Script          string
	Arguments       string
	Expression      string
	TimeoutSeconds  int
	IsPassword      bool
	DisallowOverlap bool
	PreviewAction   string
	TimeoutInput    string
	CronPreview     scheduleCronPreviewPayload
	CronError       string
}

var taskPageTemplate = template.Must(template.New("task-page").Funcs(webTemplateFunctions()).Parse(`<!doctype html>
<html lang="{{.Locale}}">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v={{assetVersion}}"><script defer src="/assets/app-v2.js?v={{assetVersion}}"></script><title>{{.Title}} · ScriptBoard</title></head>
<body>
<main class="workspace task-page" data-task-page data-task-kind="{{.Kind}}">
  <a class="task-back" href="{{.BackURL}}"><span data-lucide="arrow-left" aria-hidden="true"></span>{{t .Locale "common.back"}}</a>
  <section class="task-sheet">
    <header><div><p class="page-eyebrow">{{t .Locale "common.create"}}</p><h1>{{.Title}}</h1><p>{{.Description}}</p></div><a class="icon-button" href="{{.BackURL}}" aria-label="{{t .Locale "common.close"}}"><span data-lucide="x" aria-hidden="true"></span></a></header>
    {{if eq .Kind "new-directory"}}
    <form method="post" action="{{.Action}}" data-async>
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.Path}}">
      <label>{{t .Locale "task.directory_name"}} <input name="name" autocomplete="off" spellcheck="false" required autofocus></label>
      <footer><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><button class="button--primary" type="submit">{{t .Locale "common.create"}}</button></footer>
    </form>
    {{else if eq .Kind "upload"}}
    <form method="post" action="{{.Action}}" enctype="multipart/form-data" data-native>
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="path" value="{{.Path}}">
      <label class="file-picker">{{t .Locale "task.select_files"}} <input name="files" type="file" multiple required autofocus></label>
      <label class="check-field"><input name="replace" type="checkbox" value="yes"> {{t .Locale "task.replace_existing"}}</label>
      <footer><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><button class="button--primary" type="submit">{{t .Locale "task.start_upload"}}</button></footer>
    </form>
    {{else if eq .Kind "run"}}
    <form method="post" action="{{.Action}}">
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="script" value="{{.Path}}">
      <div class="field-readonly"><span>{{t .Locale "common.script"}}</span><code>{{.Path}}</code></div>
      <label>{{t .Locale "task.arguments"}} <input name="arguments" autocomplete="off" spellcheck="false" value="{{.Arguments}}" autofocus></label>
      <label>{{t .Locale "task.timeout_seconds"}} <input name="timeout_seconds" type="number" inputmode="numeric" min="0" max="86400" value="{{.TimeoutSeconds}}"></label>
      <footer><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><button class="button--primary" type="submit"><span data-lucide="play" aria-hidden="true"></span>{{t .Locale "task.start_run"}}</button></footer>
    </form>
    {{else if eq .Kind "quick-save"}}
    <form method="post" action="{{.Action}}" data-async>
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <div class="field-readonly"><span>{{t .Locale "common.script"}}</span><code>{{.Path}}</code></div>
      <label>{{t .Locale "run_detail.quick_name"}} <input name="name" autocomplete="off" required autofocus></label>
      <footer><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><button class="button--primary" type="submit">{{t .Locale "run_detail.save_quick"}}</button></footer>
    </form>
    {{else if or (eq .Kind "variable-new") (eq .Kind "variable-edit")}}
    <form method="post" action="{{.Action}}" data-async>
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <label>{{t .Locale "common.name"}} <input name="name" autocomplete="off" spellcheck="false" pattern="[A-Z][A-Z0-9_]{0,63}" value="{{.Name}}" required autofocus></label>
      <label>{{t .Locale "common.value"}} <textarea name="value" autocomplete="off" maxlength="4096">{{.Value}}</textarea></label>
      <label class="check-field"><input type="checkbox" name="is_password" value="1" {{if .IsPassword}}checked{{end}}> {{t .Locale "task.password_type"}}</label>
      <footer><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><button class="button--primary" type="submit">{{if eq .Kind "variable-edit"}}{{t .Locale "task.save_changes"}}{{else}}{{t .Locale "common.create"}}{{end}}</button></footer>
    </form>
    {{else if or (eq .Kind "schedule-new") (eq .Kind "schedule-edit")}}
    <form method="post" action="{{.Action}}" data-async data-schedule-form data-preview-action="{{.PreviewAction}}" data-cron-next-label="{{t .Locale "cron.next_five"}}" data-cron-idle="{{t .Locale "cron.preview_idle"}}" data-cron-pending="{{t .Locale "cron.preview_pending"}}" data-cron-unavailable="{{t .Locale "cron.preview_unavailable"}}">
      <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
      <label>{{t .Locale "common.name"}} <input name="name" autocomplete="off" value="{{.Name}}" required autofocus></label>
      <label>{{t .Locale "common.script"}} <input name="script" autocomplete="off" spellcheck="false" value="{{.Script}}" required></label>
      <label>{{t .Locale "common.arguments"}} <input name="arguments" autocomplete="off" spellcheck="false" value="{{.Arguments}}"></label>
      <section class="cron-composer" aria-labelledby="cron-composer-title">
        <header><div><span class="section-index">01</span><h2 id="cron-composer-title">{{t .Locale "cron.composer_title"}}</h2></div><p>{{t .Locale "cron.composer_description"}}</p></header>
        <div class="cron-presets" role="group" aria-label="{{t .Locale "cron.presets"}}">
          <button type="button" data-cron-preset="*/15 * * * *">{{t .Locale "cron.preset.every_15_minutes"}}</button>
          <button type="button" data-cron-preset="0 * * * *">{{t .Locale "cron.preset.hourly"}}</button>
          <button type="button" data-cron-preset="0 0 * * *">{{t .Locale "cron.preset.daily"}}</button>
          <button type="button" data-cron-preset="0 9 * * 1">{{t .Locale "cron.preset.monday"}}</button>
        </div>
        <label class="cron-expression-field"><span>{{t .Locale "task.cron_expression"}}</span><input name="expression" autocomplete="off" spellcheck="false" maxlength="256" value="{{.Expression}}" placeholder="0 0 * * *" aria-describedby="cron-field-order cron-feedback" data-cron-expression {{if .CronError}}aria-invalid="true"{{end}} required></label>
        <dl class="cron-field-ruler" id="cron-field-order">
          <div><dt>{{t .Locale "cron.field.minute"}}</dt><dd>0–59</dd></div>
          <div><dt>{{t .Locale "cron.field.hour"}}</dt><dd>0–23</dd></div>
          <div><dt>{{t .Locale "cron.field.day"}}</dt><dd>1–31</dd></div>
          <div><dt>{{t .Locale "cron.field.month"}}</dt><dd>1–12</dd></div>
          <div><dt>{{t .Locale "cron.field.weekday"}}</dt><dd>0–6</dd></div>
        </dl>
        <div class="cron-operators" aria-label="{{t .Locale "cron.operators"}}"><span><code>*</code>{{t .Locale "cron.operator.any"}}</span><span><code>,</code>{{t .Locale "cron.operator.list"}}</span><span><code>-</code>{{t .Locale "cron.operator.range"}}</span><span><code>/</code>{{t .Locale "cron.operator.step"}}</span></div>
        <details class="cron-disclosure"><summary><span data-lucide="braces" aria-hidden="true"></span>{{t .Locale "cron.advanced_guide"}}</summary><div>
          <p>{{t .Locale "cron.advanced_description"}}</p>
          <dl><div><dt><code>0 9 * * 1-5</code></dt><dd>{{t .Locale "cron.example.weekdays"}}</dd></div><div><dt><code>0 0 1 * *</code></dt><dd>{{t .Locale "cron.example.monthly"}}</dd></div></dl>
          <p class="cron-contract">{{t .Locale "cron.contract"}}</p>
        </div></details>
        <div class="cron-feedback" id="cron-feedback" data-cron-feedback aria-live="polite" {{if .CronError}}data-state="invalid"{{else if .CronPreview.Valid}}data-state="valid"{{else}}data-state="idle"{{end}}>
          {{if .CronError}}<div class="cron-feedback__message" role="alert"><span data-lucide="circle-x" aria-hidden="true"></span><p>{{.CronError}}</p></div>
          {{else if .CronPreview.Valid}}<div class="cron-feedback__result"><header><div><span data-lucide="calendar-clock" aria-hidden="true"></span><div><strong>{{.CronPreview.Summary}}</strong><small>{{.CronPreview.Timezone}}</small></div></div><span>{{t .Locale "cron.next_five"}}</span></header>{{if .CronPreview.DayOrWarning}}<p class="cron-day-warning"><span data-lucide="triangle-alert" aria-hidden="true"></span>{{.CronPreview.DayOrWarning}}</p>{{end}}<ol>{{range .CronPreview.Next}}<li><time datetime="{{.Datetime}}" data-cron-time>{{.Label}}</time></li>{{end}}</ol></div>
          {{else}}<div class="cron-feedback__message"><span data-lucide="info" aria-hidden="true"></span><p>{{t .Locale "cron.preview_idle"}}</p></div>{{end}}
        </div>
      </section>
      <label>{{t .Locale "task.timeout_seconds"}} <input name="timeout_seconds" type="number" inputmode="numeric" min="0" max="86400" value="{{if .TimeoutInput}}{{.TimeoutInput}}{{else}}{{.TimeoutSeconds}}{{end}}"></label>
      <label class="check-field"><input name="disallow_overlap" type="checkbox" value="1" {{if .DisallowOverlap}}checked{{end}}> {{t .Locale "task.disallow_overlap"}}</label>
      <footer class="schedule-form-actions"><a class="button" href="{{.BackURL}}">{{t .Locale "common.cancel"}}</a><div><button type="submit" formaction="{{.PreviewAction}}" formnovalidate data-cron-preview-submit><span data-lucide="calendar-clock" aria-hidden="true"></span>{{t .Locale "cron.preview_action"}}</button><button class="button--primary" type="submit">{{if eq .Kind "schedule-edit"}}{{t .Locale "task.save_changes"}}{{else}}{{t .Locale "common.create"}}{{end}}</button></div></footer>
    </form>
    {{end}}
  </section>
</main>
</body>
</html>`))

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
	relative := strings.Trim(request.URL.Query().Get("path"), "/")
	a.renderTaskPage(response, request, taskPageData{
		Kind: "new-directory", Title: webText(resolveWebLocale(request), "task.new_directory.title"),
		Description: webText(resolveWebLocale(request), "task.new_directory.description"),
		BackURL:     filesURL(relative), Action: "/resources/files/mkdir", Path: relative,
	})
}

func (a *App) uploadTask(response http.ResponseWriter, request *http.Request) {
	relative := strings.Trim(request.URL.Query().Get("path"), "/")
	a.renderTaskPage(response, request, taskPageData{
		Kind: "upload", Title: webText(resolveWebLocale(request), "task.upload.title"),
		Description: webText(resolveWebLocale(request), "task.upload.description"),
		BackURL:     filesURL(relative), Action: "/resources/files/upload", Path: relative,
	})
}

func (a *App) runFileTask(response http.ResponseWriter, request *http.Request) {
	relative := filepath.ToSlash(strings.Trim(request.PathValue("path"), "/"))
	info, err := a.managed.Info(relative)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(response, "Script not found", http.StatusNotFound)
		return
	}
	parent := filepath.ToSlash(filepath.Dir(relative))
	if parent == "." {
		parent = ""
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: "run", Title: webText(resolveWebLocale(request), "task.run.title"),
		Description: webText(resolveWebLocale(request), "task.run.description"),
		BackURL:     filesURL(parent), Action: "/monitor/runs/start", Path: relative,
	})
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
	backURL := "/monitor/runs/" + url.PathEscape(run.ID)
	a.renderTaskPage(response, request, taskPageData{
		Kind: "quick-save", Title: webText(resolveWebLocale(request), "run_detail.save_quick"),
		Description: webText(resolveWebLocale(request), "task.quick_save.description"),
		BackURL:     backURL, Action: backURL + "/quick-run", Path: run.ScriptPath,
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
	a.renderTaskPage(response, request, taskPageData{
		Kind: "schedule-new", Title: webText(resolveWebLocale(request), "task.schedule_new.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_description"),
		BackURL:     "/config/schedules", Action: "/config/schedules", PreviewAction: "/config/schedules/preview",
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
	a.renderTaskPage(response, request, taskPageData{
		Kind: "schedule-edit", Title: webText(resolveWebLocale(request), "task.schedule_edit.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_description"),
		BackURL:     "/config/schedules", Action: "/config/schedules/" + url.PathEscape(selected.ID) + "/update",
		PreviewAction: "/config/schedules/" + url.PathEscape(selected.ID) + "/preview",
		Name:          selected.Name, Script: selected.ScriptPath, Arguments: selected.ArgumentsTemplate,
		Expression: selected.Expression, TimeoutSeconds: selected.TimeoutSeconds, DisallowOverlap: !selected.AllowOverlap,
	})
}
