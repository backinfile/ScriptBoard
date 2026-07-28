package app

import (
	"bytes"
	"encoding/json"
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
		{key: "nav.monitor", items: []itemSpec{{"/monitor", "nav.overview", "activity"}}},
		{key: "nav.resources", items: []itemSpec{{"/resources/files/", "nav.files", "folder-code"}, {"/resources/variables", "nav.variables", "braces"}}},
		{key: "nav.configuration", items: []itemSpec{{"/config/quick-runs", "nav.quick_runs", "zap"}, {"/config/schedules", "nav.schedules", "calendar-clock"}}},
		{key: "nav.history", items: []itemSpec{{"/monitor/runs", "nav.runs", "square-terminal"}, {"/history/audit", "nav.audit", "scroll-text"}}},
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
