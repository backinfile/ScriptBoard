package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/hoststatus"
	"scriptboard/internal/identity"
	"scriptboard/internal/websitemonitor"
)

type shellStatusResponse struct {
	State                     string    `json:"state"`
	CollectedAt               time.Time `json:"collectedAt"`
	IssueCount                int       `json:"issueCount"`
	ActiveRuns                int       `json:"activeRuns"`
	WebsiteState              string    `json:"websiteState"`
	WebsiteDown               int       `json:"websiteDown"`
	WebsiteVerifying          int       `json:"websiteVerifying"`
	StoppedPinnedApplications int       `json:"stoppedPinnedApplications"`
	ApplicationIssueCount     int       `json:"applicationIssueCount"`
}

type shellStatusCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	now       func() time.Time
	load      func(context.Context) (shellStatusResponse, error)
	value     shellStatusResponse
	expiresAt time.Time
	valid     bool
	refresh   *shellStatusRefresh
}

type shellStatusRefresh struct {
	done  chan struct{}
	value shellStatusResponse
	err   error
}

func newShellStatusCache(ttl time.Duration, now func() time.Time, load func(context.Context) (shellStatusResponse, error)) *shellStatusCache {
	return &shellStatusCache{ttl: ttl, now: now, load: load}
}

func (c *shellStatusCache) Read(ctx context.Context) (shellStatusResponse, error) {
	c.mu.Lock()
	if c.valid && c.now().Before(c.expiresAt) {
		value := c.value
		c.mu.Unlock()
		return value, nil
	}
	if c.refresh != nil {
		refresh := c.refresh
		c.mu.Unlock()
		select {
		case <-refresh.done:
			return refresh.value, refresh.err
		case <-ctx.Done():
			return shellStatusResponse{}, ctx.Err()
		}
	}
	c.refresh = &shellStatusRefresh{done: make(chan struct{})}
	refresh := c.refresh
	c.mu.Unlock()

	value, err := c.load(ctx)

	c.mu.Lock()
	refresh.value = value
	refresh.err = err
	if err == nil {
		c.value = value
		c.expiresAt = c.now().Add(c.ttl)
		c.valid = true
	}
	c.refresh = nil
	close(refresh.done)
	c.mu.Unlock()
	return value, err
}

func (a *App) loadShellStatus(ctx context.Context) (shellStatusResponse, error) {
	overview, err := a.hostStatus.Overview(ctx, hoststatus.Range15Minutes)
	if err != nil {
		return shellStatusResponse{}, err
	}
	activeRuns := 0
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs WHERE status IN ('starting', 'running', 'stopping', 'timing_out')").Scan(&activeRuns); err != nil {
		return shellStatusResponse{}, err
	}
	websiteMonitors, err := a.websiteMonitor.List(ctx, websitemonitor.Filter{})
	if err != nil {
		return shellStatusResponse{}, err
	}
	websiteState, websiteDown, websiteVerifying := summarizeWebsiteShellStatus(websiteMonitors)
	stoppedPinnedApplications := 0
	applicationIssueCount := 0
	applicationView, applicationErr := a.applicationStatus.View(ctx, appstatus.Query{Limit: 1})
	if applicationErr != nil || applicationView.CollectedAt.IsZero() {
		applicationIssueCount = 1
	} else {
		for source := range applicationView.Errors {
			if source != "docker" {
				applicationIssueCount++
			}
		}
		for _, application := range applicationView.Pinned {
			if !application.Running {
				stoppedPinnedApplications++
			}
		}
	}
	state := "current"
	if overview.Stale {
		state = "stale"
	} else if len(overview.Errors) > 0 {
		state = "attention"
	}
	return shellStatusResponse{
		State:                     state,
		CollectedAt:               overview.CollectedAt,
		IssueCount:                len(overview.Errors),
		ActiveRuns:                activeRuns,
		WebsiteState:              websiteState,
		WebsiteDown:               websiteDown,
		WebsiteVerifying:          websiteVerifying,
		StoppedPinnedApplications: stoppedPinnedApplications,
		ApplicationIssueCount:     applicationIssueCount,
	}, nil
}

func summarizeWebsiteShellStatus(monitors []websitemonitor.Monitor) (state string, down int, verifying int) {
	for _, monitor := range monitors {
		switch monitor.State {
		case websitemonitor.StateDown:
			down++
		case websitemonitor.StatePending, websitemonitor.StateVerifying:
			verifying++
		}
	}
	if down > 0 {
		return "down", down, verifying
	}
	if verifying > 0 {
		return "verifying", down, verifying
	}
	return "up", down, verifying
}

func (a *App) shellStatus(response http.ResponseWriter, request *http.Request) {
	status, err := a.shellStatusCache.Read(request.Context())
	if err != nil {
		http.Error(response, "Unable to read active runs", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(status)
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
	ApplicationName                       string
	Version                               string
	Role                                  string
	Environment, Status, StatusState      string
	CurrentErrorCount                     int
	ActiveRuns                            int
	WebsiteState                          string
	WebsiteDown, WebsiteVerifying         int
	StoppedPinnedApplications             int
	ApplicationIssueCount                 int
	Navigation                            []shellNavigationGroup
	SettingsCurrent, ChineseLocaleCurrent bool
	CanManageUsers                        bool
}

func (a *App) addApplicationShell(request *http.Request, body []byte) []byte {
	current, username, ok := a.loadSession(request)
	if !ok {
		return body
	}
	locale := resolveWebLocale(request)
	shellStatus := shellStatusResponse{State: "stale"}
	if cached, statusErr := a.shellStatusCache.Read(request.Context()); statusErr == nil {
		shellStatus = cached
	}
	environment := webText(locale, "shell.local")
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		remoteHost = request.RemoteAddr
	}
	if ip := net.ParseIP(remoteHost); ip != nil && !ip.IsLoopback() {
		environment = webText(locale, "shell.remote")
	}
	statusState := shellStatus.State
	status := webText(locale, "status."+statusState)
	navigation := shellNavigation(locale, request.URL.Path, current.role)
	if identity.Allows(current.role, identity.PermissionObserve) {
		if dashboards, dashboardErr := a.customDashboards.ListDashboards(request.Context()); dashboardErr == nil {
			for index := range navigation {
				if navigation[index].Label != webText(locale, "nav.monitor") {
					continue
				}
				for _, dashboard := range dashboards {
					if !dashboard.ShowAsTab {
						continue
					}
					href := "/monitor/dashboard/" + dashboard.ID
					navigation[index].Items = append(navigation[index].Items, shellNavigationItem{
						Href: href, Label: dashboard.Name, Icon: "panel-top", Current: request.URL.Path == href,
					})
				}
				break
			}
		}
	}
	var shell bytes.Buffer
	applicationName, err := a.loadInstanceDisplayName(request.Context())
	if err != nil {
		applicationName = defaultApplicationName
	}
	_ = applicationShellTemplate.Execute(&shell, applicationShellData{
		Locale: locale, Username: username, Role: string(current.role), CSRFToken: current.csrfToken, ReturnTo: request.URL.RequestURI(), Version: displayShellVersion(buildinfo.Current().Version),
		ApplicationName: applicationName,
		Environment:     environment, Status: status, StatusState: statusState, CurrentErrorCount: currentShellErrorCount(shellStatus), ActiveRuns: shellStatus.ActiveRuns,
		WebsiteState: shellStatus.WebsiteState, WebsiteDown: shellStatus.WebsiteDown, WebsiteVerifying: shellStatus.WebsiteVerifying,
		StoppedPinnedApplications: shellStatus.StoppedPinnedApplications, ApplicationIssueCount: shellStatus.ApplicationIssueCount,
		Navigation: navigation, SettingsCurrent: strings.HasPrefix(request.URL.Path, "/settings/"),
		ChineseLocaleCurrent: locale == localeSimplifiedChinese,
		CanManageUsers:       identity.Allows(current.role, identity.PermissionManageUsers),
	})

	bodyText := prepareApplicationDocument(body, locale)
	bodyStart := strings.Index(bodyText, "<body")
	if bodyStart < 0 {
		return body
	}
	bodyEnd := strings.Index(bodyText[bodyStart:], ">")
	if bodyEnd < 0 {
		return body
	}
	bodyText = bodyText[:bodyStart+len("<body")] + ` data-app-shell data-locale="` + string(locale) + `"` + bodyText[bodyStart+len("<body"):]
	bodyStart = strings.Index(bodyText, "<body")
	bodyEnd = strings.Index(bodyText[bodyStart:], ">")
	insertAt := bodyStart + bodyEnd + 1
	return []byte(bodyText[:insertAt] + shell.String() + bodyText[insertAt:])
}

func displayShellVersion(version string) string {
	if version == "development" {
		return "dev"
	}
	return version
}

func currentShellErrorCount(status shellStatusResponse) int {
	return max(0, status.IssueCount) +
		max(0, status.WebsiteDown) +
		max(0, status.StoppedPinnedApplications) +
		max(0, status.ApplicationIssueCount)
}

func prepareApplicationDocument(body []byte, locale webLocale) string {
	bodyText := setHTMLLocale(string(body), locale)
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
	return bodyText
}

func shellNavigation(locale webLocale, path string, role identity.Role) []shellNavigationGroup {
	type itemSpec struct {
		href, key, icon string
		permission      identity.Permission
	}
	specs := []struct {
		key   string
		items []itemSpec
	}{
		{key: "nav.monitor", items: []itemSpec{{"/monitor", "nav.overview", "activity", identity.PermissionObserve}, {"/monitor/applications", "nav.applications", "app-window", identity.PermissionObserve}, {"/monitor/containers", "nav.containers", "package", identity.PermissionObserve}, {"/monitor/kubernetes", "nav.kubernetes", "box", identity.PermissionObserve}, {"/monitor/websites", "nav.websites", "network", identity.PermissionObserve}, {"/monitor/security", "nav.security", "shield-check", identity.PermissionObserve}}},
		{key: "nav.resources", items: []itemSpec{{"/resources/files", "nav.files", "folder-code", identity.PermissionReadFiles}, {"/resources/variables", "nav.variables", "braces", identity.PermissionManageExecution}, {"/resources/databases", "nav.databases", "database", identity.PermissionManageDatabases}}},
		{key: "nav.configuration", items: []itemSpec{{"/config/quick-runs", "nav.quick_runs", "zap", identity.PermissionObserve}, {"/config/schedules", "nav.schedules", "calendar-clock", identity.PermissionObserve}, {"/config/external-interfaces", "nav.external_interfaces", "plug", identity.PermissionManageExecution}, {"/config/dashboards", "nav.dashboards", "layout-dashboard", identity.PermissionObserve}}},
		{key: "nav.history", items: []itemSpec{{"/history/runs", "nav.runs", "square-terminal", identity.PermissionObserve}, {"/history/audit", "nav.audit", "scroll-text", identity.PermissionReadAudit}}},
	}
	groups := make([]shellNavigationGroup, 0, len(specs))
	for _, group := range specs {
		items := make([]shellNavigationItem, 0, len(group.items))
		for _, item := range group.items {
			if !identity.Allows(role, item.permission) {
				continue
			}
			current := currentShellNavigationItem(path, item.href)
			items = append(items, shellNavigationItem{Href: item.href, Label: webText(locale, item.key), Icon: item.icon, Current: current})
		}
		if len(items) > 0 {
			groups = append(groups, shellNavigationGroup{Label: webText(locale, group.key), Items: items})
		}
	}
	return groups
}

func currentShellNavigationItem(path, href string) bool {
	if path == href {
		return true
	}
	if href == "/resources/files" {
		return path == "/resources/trash"
	}
	if href == "/monitor" {
		return false
	}
	return strings.HasPrefix(path, href)
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
