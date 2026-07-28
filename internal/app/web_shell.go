package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/hoststatus"
)

type shellStatusResponse struct {
	State       string    `json:"state"`
	CollectedAt time.Time `json:"collectedAt"`
	IssueCount  int       `json:"issueCount"`
	ActiveRuns  int       `json:"activeRuns"`
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
	for {
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
	state := "current"
	if overview.Stale {
		state = "stale"
	} else if len(overview.Errors) > 0 {
		state = "attention"
	}
	return shellStatusResponse{
		State:       state,
		CollectedAt: overview.CollectedAt,
		IssueCount:  len(overview.Errors),
		ActiveRuns:  activeRuns,
	}, nil
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
	navigation := shellNavigation(locale, request.URL.Path)
	var shell bytes.Buffer
	_ = applicationShellTemplate.Execute(&shell, applicationShellData{
		Locale: locale, Username: username, CSRFToken: current.csrfToken, ReturnTo: request.URL.RequestURI(),
		Environment: environment, Status: status, StatusState: statusState, ActiveRuns: shellStatus.ActiveRuns,
		Navigation: navigation, SettingsCurrent: strings.HasPrefix(request.URL.Path, "/settings/"),
		ChineseLocaleCurrent: locale == localeSimplifiedChinese,
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
