package app

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"scriptboard/internal/hoststatus"
)

type shellStatusResponse struct {
	State       string    `json:"state"`
	CollectedAt time.Time `json:"collectedAt"`
	IssueCount  int       `json:"issueCount"`
	ActiveRuns  int       `json:"activeRuns"`
}

func (a *App) shellStatus(response http.ResponseWriter, request *http.Request) {
	overview, err := a.hostStatus.Overview(request.Context(), hoststatus.Range15Minutes)
	if err != nil {
		http.Error(response, "Unable to read host status", http.StatusInternalServerError)
		return
	}
	activeRuns := 0
	if err := a.db.QueryRow("SELECT COUNT(*) FROM runs WHERE status IN ('starting', 'running', 'stopping', 'timing_out')").Scan(&activeRuns); err != nil {
		http.Error(response, "Unable to read active runs", http.StatusInternalServerError)
		return
	}
	state := "current"
	if overview.Stale {
		state = "stale"
	} else if len(overview.Errors) > 0 {
		state = "attention"
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(shellStatusResponse{
		State:       state,
		CollectedAt: overview.CollectedAt,
		IssueCount:  len(overview.Errors),
		ActiveRuns:  activeRuns,
	})
}

type shellNavigationItem struct {
	Href, Label, Icon string
	Current           bool
}

type shellNavigationGroup struct {
	Label string
	Items []shellNavigationItem
}

type applicationShellData struct {
	Locale                                webLocale
	Username, CSRFToken, ReturnTo         string
	Environment, Status, StatusState      string
	ActiveRuns                            int
	Navigation                            []shellNavigationGroup
	SettingsCurrent, ChineseLocaleCurrent bool
}

var applicationShellTemplate = template.Must(template.New("application-shell").Funcs(template.FuncMap{
	"shellText": func(key string, locale webLocale) string {
		return webText(locale, key)
	},
}).Parse(`
<a class="skip-link" href="#main-content">{{.Locale | shellText "shell.skip"}}</a>
<button class="sidebar-toggle" type="button" data-sidebar-toggle aria-controls="app-sidebar" aria-expanded="false">
<span data-lucide="panel-left-open" aria-hidden="true"></span><span class="sr-only">{{.Locale | shellText "shell.open_navigation"}}</span>
</button>
<div class="sidebar-scrim" data-sidebar-close hidden></div>
<aside class="app-sidebar" id="app-sidebar" data-pjax-nav aria-label="{{.Locale | shellText "shell.navigation"}}">
  <div class="sidebar-head">
    <a class="brand-wordmark" href="/monitor" aria-label="ScriptBoard">{{"ScriptBoard"}}</a>
    <button class="sidebar-close" type="button" data-sidebar-close><span data-lucide="x" aria-hidden="true"></span><span class="sr-only">{{.Locale | shellText "shell.close_navigation"}}</span></button>
  </div>
  <nav class="sidebar-nav">
    {{range .Navigation}}
    <section class="nav-group">
      <h2>{{.Label}}</h2>
      {{range .Items}}<a href="{{.Href}}" {{if .Current}}aria-current="page"{{end}}><span data-lucide="{{.Icon}}" aria-hidden="true"></span><span>{{.Label}}</span></a>{{end}}
    </section>
    {{end}}
  </nav>
  <footer class="sidebar-footer">
    <a class="sidebar-status" href="/monitor" data-shell-status data-state="{{.StatusState}}">
      <span class="status-dot" aria-hidden="true"></span>
      <span><strong>{{.Status}}</strong><small>{{.Environment}} · {{.ActiveRuns}} {{.Locale | shellText "shell.active_runs"}}</small></span>
    </a>
    <div class="sidebar-utilities">
      <form method="post" action="/settings/locale" data-native>
        <input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
        <input type="hidden" name="return_to" value="{{.ReturnTo}}">
        <button type="submit" name="locale" value="{{if .ChineseLocaleCurrent}}en-US{{else}}zh-CN{{end}}" aria-label="{{.Locale | shellText "shell.change_language"}}">
          <span data-lucide="languages" aria-hidden="true"></span>{{if .ChineseLocaleCurrent}}EN{{else}}中文{{end}}
        </button>
      </form>
      <a href="/settings/account" {{if .SettingsCurrent}}aria-current="page"{{end}}><span data-lucide="settings" aria-hidden="true"></span><span>{{.Locale | shellText "shell.settings"}}</span></a>
    </div>
    <div class="sidebar-account">
      <a href="/settings/account"><span data-lucide="circle-user-round" aria-hidden="true"></span><span>{{.Username}}</span></a>
      <form method="post" action="/logout"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit"><span data-lucide="log-out" aria-hidden="true"></span><span>{{.Locale | shellText "shell.sign_out"}}</span></button></form>
    </div>
  </footer>
</aside>`))

func (a *App) addApplicationShell(request *http.Request, body []byte) []byte {
	current, username, ok := a.loadSession(request)
	if !ok {
		return body
	}
	locale := resolveWebLocale(request)
	activeRuns := 0
	_ = a.db.QueryRow("SELECT COUNT(*) FROM runs WHERE status IN ('starting', 'running', 'stopping', 'timing_out')").Scan(&activeRuns)
	environment := webText(locale, "shell.local")
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		remoteHost = request.RemoteAddr
	}
	if ip := net.ParseIP(remoteHost); ip != nil && !ip.IsLoopback() {
		environment = webText(locale, "shell.remote")
	}
	statusState := "stale"
	status := webText(locale, "status.stale")
	if overview, overviewErr := a.hostStatus.Overview(request.Context(), hoststatus.Range15Minutes); overviewErr == nil {
		switch {
		case overview.Stale:
			statusState, status = "stale", webText(locale, "status.stale")
		case len(overview.Errors) > 0:
			statusState, status = "attention", webText(locale, "status.attention")
		default:
			statusState, status = "current", webText(locale, "status.current")
		}
	}
	navigation := shellNavigation(locale, request.URL.Path)
	var shell bytes.Buffer
	_ = applicationShellTemplate.Execute(&shell, applicationShellData{
		Locale: locale, Username: username, CSRFToken: current.csrfToken, ReturnTo: request.URL.RequestURI(),
		Environment: environment, Status: status, StatusState: statusState, ActiveRuns: activeRuns,
		Navigation: navigation, SettingsCurrent: strings.HasPrefix(request.URL.Path, "/settings/"),
		ChineseLocaleCurrent: locale == localeSimplifiedChinese,
	})

	bodyText := setHTMLLocale(string(body), locale)
	bodyStart := strings.Index(bodyText, "<body")
	if bodyStart < 0 {
		return body
	}
	bodyEnd := strings.Index(bodyText[bodyStart:], ">")
	if bodyEnd < 0 {
		return body
	}
	mainStart := strings.Index(bodyText, "<main")
	if mainStart >= 0 {
		mainEnd := strings.Index(bodyText[mainStart:], ">")
		if mainEnd >= 0 {
			mainTag := bodyText[mainStart : mainStart+mainEnd]
			if !strings.Contains(mainTag, ` id=`) {
				bodyText = bodyText[:mainStart+len("<main")] + ` id="main-content"` + bodyText[mainStart+len("<main"):]
			}
		}
	}
	bodyStart = strings.Index(bodyText, "<body")
	bodyText = bodyText[:bodyStart+len("<body")] + ` data-app-shell data-locale="` + string(locale) + `"` + bodyText[bodyStart+len("<body"):]
	bodyStart = strings.Index(bodyText, "<body")
	bodyEnd = strings.Index(bodyText[bodyStart:], ">")
	insertAt := bodyStart + bodyEnd + 1
	return []byte(bodyText[:insertAt] + shell.String() + bodyText[insertAt:])
}

func shellNavigation(locale webLocale, path string) []shellNavigationGroup {
	type itemSpec struct {
		href, key, icon string
	}
	specs := []struct {
		key   string
		items []itemSpec
	}{
		{key: "nav.monitor", items: []itemSpec{{"/monitor", "nav.overview", "activity"}, {"/monitor/runs", "nav.runs", "square-terminal"}}},
		{key: "nav.resources", items: []itemSpec{{"/resources/files/", "nav.files", "folder-code"}, {"/resources/variables", "nav.variables", "braces"}}},
		{key: "nav.configuration", items: []itemSpec{{"/config/quick-runs", "nav.quick_runs", "zap"}, {"/config/schedules", "nav.schedules", "calendar-clock"}}},
		{key: "nav.history", items: []itemSpec{{"/history/audit", "nav.audit", "scroll-text"}}},
	}
	groups := make([]shellNavigationGroup, 0, len(specs))
	for _, group := range specs {
		items := make([]shellNavigationItem, 0, len(group.items))
		for _, item := range group.items {
			current := path == item.href || item.href == "/monitor" && path == "/monitor" ||
				item.href == "/resources/files/" && (strings.HasPrefix(path, "/resources/files/") || path == "/resources/trash") ||
				item.href != "/monitor" && item.href != "/resources/files/" && strings.HasPrefix(path, item.href)
			items = append(items, shellNavigationItem{Href: item.href, Label: webText(locale, item.key), Icon: item.icon, Current: current})
		}
		groups = append(groups, shellNavigationGroup{Label: webText(locale, group.key), Items: items})
	}
	return groups
}

func setHTMLLocale(body string, locale webLocale) string {
	htmlStart := strings.Index(body, "<html")
	if htmlStart < 0 {
		return body
	}
	htmlEnd := strings.Index(body[htmlStart:], ">")
	if htmlEnd < 0 {
		return body
	}
	tagEnd := htmlStart + htmlEnd
	tag := body[htmlStart:tagEnd]
	if langStart := strings.Index(tag, ` lang="`); langStart >= 0 {
		valueStart := langStart + len(` lang="`)
		if valueEnd := strings.Index(tag[valueStart:], `"`); valueEnd >= 0 {
			tag = tag[:valueStart] + string(locale) + tag[valueStart+valueEnd:]
		}
	} else {
		tag += ` lang="` + string(locale) + `"`
	}
	return body[:htmlStart] + tag + body[tagEnd:]
}
