package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/hostsecurity"
)

type securityFirewallDraft struct {
	Baseline  []hostsecurity.FirewallRule
	Desired   []hostsecurity.FirewallRule
	Changes   []securityFirewallChange
	UpdatedAt time.Time
}

type securityFirewallChange struct {
	Kind, Title, Detail string
}

type securityPageView struct {
	Locale           webLocale
	CSRFToken        string
	Tab              string
	Range            string
	Result           string
	LoginType        string
	FromDate         string
	ToDate           string
	PageSize         int
	LoginPrevious    int
	LoginNext        int
	BanPrevious      int
	BanNext          int
	HasLoginPrevious bool
	HasLoginNext     bool
	HasBanPrevious   bool
	HasBanNext       bool
	Capabilities     hostsecurity.Capabilities
	LoginPage        hostsecurity.LoginPage
	RecentLogins     []hostsecurity.LoginRecord
	DisplayedLogins  []hostsecurity.LoginRecord
	BanPage          hostsecurity.BanPage
	Rules            []hostsecurity.FirewallRule
	DraftChanges     []securityFirewallChange
	DraftUpdatedAt   time.Time
	CanManage        bool
	ShowBans         bool
	ShowReview       bool
	Notice           string
	LoginError       string
	BanError         string
	Linux            bool
	Windows          bool
	HasDraft         bool
	HasSSHAllowRule  bool
}

func (a *App) securityPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	tab := request.URL.Query().Get("tab")
	if tab != "logins" && tab != "defense" {
		tab = "overview"
	}
	rangeValue := request.URL.Query().Get("range")
	if rangeValue != "7d" && rangeValue != "30d" {
		rangeValue = "24h"
	}
	dateRange, dateErr := parseLocalDateRange(url.Values{
		"from": {request.URL.Query().Get("from")},
		"to":   {request.URL.Query().Get("to")},
	})
	if dateErr != nil {
		dateRange = localDateRange{}
	}
	page := positiveInt(request.URL.Query().Get("page"), 1)
	pageSize := positiveInt(request.URL.Query().Get("page_size"), 20)
	if pageSize != 50 && pageSize != 100 {
		pageSize = 20
	}
	query := hostsecurity.LoginQuery{
		Range: rangeValue, Start: dateRange.From, End: dateRange.ToExclusive,
		Result: hostsecurity.LoginResult(request.URL.Query().Get("result")),
		Type:   request.URL.Query().Get("type"), Page: page, PageSize: pageSize,
	}
	if query.Result != hostsecurity.ResultSuccess && query.Result != hostsecurity.ResultFailure {
		query.Result = ""
	}

	capabilityContext, cancelCapabilities := context.WithTimeout(request.Context(), 12*time.Second)
	capabilities := a.hostSecurity.Capabilities(capabilityContext)
	cancelCapabilities()
	view := securityPageView{
		Locale: locale, CSRFToken: current.csrfToken, Tab: tab, Range: rangeValue,
		Result: string(query.Result), LoginType: query.Type, FromDate: dateRange.FromDate, ToDate: dateRange.ToDate,
		PageSize: pageSize, Capabilities: capabilities, CanManage: roleAllows(current.role, permissionManageSystem),
		ShowBans: request.URL.Query().Get("bans") == "1", ShowReview: request.URL.Query().Get("review") == "1",
		Notice: securityNotice(locale, request.URL.Query().Get("notice")), Linux: capabilities.OS == "linux", Windows: capabilities.OS == "windows",
	}

	if tab == "overview" || tab == "logins" || tab == "defense" && capabilities.OS == "windows" {
		loginContext, cancelLogins := context.WithTimeout(request.Context(), 15*time.Second)
		loginPage, err := a.hostSecurity.Logins(loginContext, query)
		cancelLogins()
		if err != nil {
			view.LoginError = err.Error()
		} else {
			view.LoginPage = loginPage
			view.HasLoginPrevious = loginPage.Page > 1
			view.HasLoginNext = loginPage.Page < loginPage.Pages
			view.LoginPrevious = max(1, loginPage.Page-1)
			view.LoginNext = min(loginPage.Pages, loginPage.Page+1)
			limit := min(4, len(loginPage.Records))
			view.RecentLogins = loginPage.Records[:limit]
			view.DisplayedLogins = loginPage.Records
			if tab == "overview" {
				view.DisplayedLogins = view.RecentLogins
			}
		}
	}
	if capabilities.Fail2Ban.Installed && capabilities.Fail2Ban.Running && (tab == "overview" || tab == "defense") {
		banContext, cancelBans := context.WithTimeout(request.Context(), 8*time.Second)
		banPage, err := a.hostSecurity.Bans(banContext, positiveInt(request.URL.Query().Get("ban_page"), 1), 20)
		cancelBans()
		if err != nil {
			view.BanError = err.Error()
		} else {
			view.BanPage = banPage
			view.HasBanPrevious = banPage.Page > 1
			view.HasBanNext = banPage.Page < banPage.Pages
			view.BanPrevious = max(1, banPage.Page-1)
			view.BanNext = min(banPage.Pages, banPage.Page+1)
		}
	}
	view.Rules = append([]hostsecurity.FirewallRule(nil), capabilities.Rules...)
	if draft, ok := a.securityDraft(current.userID); ok {
		view.Rules = draft.Desired
		view.DraftChanges = draft.Changes
		view.DraftUpdatedAt = draft.UpdatedAt
		view.HasDraft = len(draft.Changes) > 0
	}
	view.HasSSHAllowRule = securityHasSSHAllowRule(view.Rules, capabilities.SSHPort)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = securityTemplate.Execute(response, view)
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func securityNotice(locale webLocale, code string) string {
	if code == "" {
		return ""
	}
	allowed := map[string]string{
		"installed": "security.notice_installed", "unbanned": "security.notice_unbanned",
		"drafted": "security.notice_drafted", "discarded": "security.notice_discarded",
		"synced": "security.notice_synced", "ufw_enabled": "security.notice_ufw_enabled",
		"windows_rule_saved": "security.notice_windows_rule_saved",
	}
	key, ok := allowed[code]
	if !ok {
		return ""
	}
	return webText(locale, key)
}

func (a *App) installSecurityComponent(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	component := request.PathValue("component")
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Minute)
	defer cancel()
	if err := a.hostSecurity.Install(ctx, component); err != nil {
		a.recordAuditForRequest(request, "install_security_component", component, "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "install_security_component", component, "succeeded")
	securityRedirect(response, request, "defense", "installed", nil)
}

func (a *App) unbanSecurityIP(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	ip := strings.TrimSpace(request.FormValue("ip"))
	jail := strings.TrimSpace(request.FormValue("jail"))
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	if err := a.hostSecurity.Unban(ctx, jail, ip); err != nil {
		a.recordAuditForRequest(request, "fail2ban_unban", jail+":"+ip, "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "fail2ban_unban", jail+":"+ip, "succeeded")
	securityRedirect(response, request, "defense", "unbanned", url.Values{"bans": {"1"}})
}

func (a *App) addSecurityFirewallDraftRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	rule := hostsecurity.FirewallRule{
		Direction: hostsecurity.Direction(request.FormValue("direction")), Action: hostsecurity.FirewallAction(request.FormValue("action")),
		Protocol: strings.ToLower(strings.TrimSpace(request.FormValue("protocol"))), Port: strings.TrimSpace(request.FormValue("port")),
		Address: strings.TrimSpace(request.FormValue("address")), Name: strings.TrimSpace(request.FormValue("name")), Enabled: true,
	}
	if rule.Address == "" || strings.EqualFold(rule.Address, "any") {
		rule.Address = "Anywhere"
	}
	if err := hostsecurity.ValidateFirewallRule(rule); err != nil {
		writeSecurityError(response, request, err)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	capabilities := a.hostSecurity.Capabilities(request.Context())
	if !capabilities.UFW.Installed {
		writeSecurityError(response, request, hostsecurity.ErrComponentMissing)
		return
	}
	a.securityDraftMu.Lock()
	draft := a.securityDrafts[current.userID]
	if draft.Baseline == nil {
		draft.Baseline = cloneSecurityRules(capabilities.Rules)
		draft.Desired = cloneSecurityRules(capabilities.Rules)
	}
	draft.Desired = append(draft.Desired, rule)
	draft.Changes = append(draft.Changes, securityFirewallChange{Kind: "add", Title: rule.Name, Detail: securityRuleDescription(rule)})
	draft.UpdatedAt = time.Now().UTC()
	a.securityDrafts[current.userID] = draft
	a.securityDraftMu.Unlock()
	securityRedirect(response, request, "defense", "drafted", nil)
}

func (a *App) toggleSecurityFirewallDraftRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	index, err := strconv.Atoi(request.FormValue("index"))
	if err != nil {
		writeSecurityError(response, request, hostsecurity.ErrInvalidRule)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	capabilities := a.hostSecurity.Capabilities(request.Context())
	a.securityDraftMu.Lock()
	draft := a.securityDrafts[current.userID]
	if draft.Baseline == nil {
		draft.Baseline = cloneSecurityRules(capabilities.Rules)
		draft.Desired = cloneSecurityRules(capabilities.Rules)
	}
	if index < 0 || index >= len(draft.Desired) {
		a.securityDraftMu.Unlock()
		writeSecurityError(response, request, hostsecurity.ErrInvalidRule)
		return
	}
	draft.Desired[index].Enabled = !draft.Desired[index].Enabled
	rule := draft.Desired[index]
	draft.Changes = append(draft.Changes, securityFirewallChange{Kind: "update", Title: rule.Name, Detail: securityRuleDescription(rule)})
	draft.UpdatedAt = time.Now().UTC()
	a.securityDrafts[current.userID] = draft
	a.securityDraftMu.Unlock()
	securityRedirect(response, request, "defense", "drafted", nil)
}

func (a *App) deleteSecurityFirewallDraftRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	index, err := strconv.Atoi(request.FormValue("index"))
	if err != nil {
		writeSecurityError(response, request, hostsecurity.ErrInvalidRule)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	capabilities := a.hostSecurity.Capabilities(request.Context())
	a.securityDraftMu.Lock()
	draft := a.securityDrafts[current.userID]
	if draft.Baseline == nil {
		draft.Baseline = cloneSecurityRules(capabilities.Rules)
		draft.Desired = cloneSecurityRules(capabilities.Rules)
	}
	if index < 0 || index >= len(draft.Desired) {
		a.securityDraftMu.Unlock()
		writeSecurityError(response, request, hostsecurity.ErrInvalidRule)
		return
	}
	rule := draft.Desired[index]
	draft.Desired = append(draft.Desired[:index], draft.Desired[index+1:]...)
	draft.Changes = append(draft.Changes, securityFirewallChange{Kind: "delete", Title: rule.Name, Detail: securityRuleDescription(rule)})
	draft.UpdatedAt = time.Now().UTC()
	a.securityDrafts[current.userID] = draft
	a.securityDraftMu.Unlock()
	securityRedirect(response, request, "defense", "drafted", nil)
}

func (a *App) discardSecurityFirewallDraft(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	a.securityDraftMu.Lock()
	delete(a.securityDrafts, current.userID)
	a.securityDraftMu.Unlock()
	a.recordAuditForRequest(request, "discard_ufw_draft", "ufw", "succeeded")
	securityRedirect(response, request, "defense", "discarded", nil)
}

func (a *App) applySecurityFirewallDraft(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	a.securityDraftMu.Lock()
	draft, ok := a.securityDrafts[current.userID]
	if !ok || len(draft.Changes) == 0 {
		a.securityDraftMu.Unlock()
		writeSecurityError(response, request, hostsecurity.ErrInvalidRule)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
	err := a.hostSecurity.ApplyUFW(ctx, cloneSecurityRules(draft.Baseline), cloneSecurityRules(draft.Desired))
	cancel()
	if err == nil {
		delete(a.securityDrafts, current.userID)
	}
	a.securityDraftMu.Unlock()
	if err != nil {
		a.recordAuditForRequest(request, "apply_ufw_draft", fmt.Sprintf("%d changes", len(draft.Changes)), "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "apply_ufw_draft", fmt.Sprintf("%d changes", len(draft.Changes)), "succeeded")
	securityRedirect(response, request, "defense", "synced", nil)
}

func (a *App) enableSecurityFirewall(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	if _, ok := a.securityDraft(current.userID); ok {
		writeSecurityError(response, request, errors.New("synchronize or discard pending UFW changes first"))
		return
	}
	capabilities := a.hostSecurity.Capabilities(request.Context())
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	err := a.hostSecurity.EnableUFW(ctx, capabilities.Rules)
	cancel()
	if err != nil {
		a.recordAuditForRequest(request, "enable_ufw", "ufw", "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "enable_ufw", "ufw", "succeeded")
	securityRedirect(response, request, "defense", "ufw_enabled", nil)
}

func (a *App) addWindowsFirewallRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	rule := hostsecurity.FirewallRule{
		Direction: hostsecurity.Direction(request.FormValue("direction")), Action: hostsecurity.FirewallAction(request.FormValue("action")),
		Protocol: strings.ToLower(strings.TrimSpace(request.FormValue("protocol"))), Port: strings.TrimSpace(request.FormValue("port")),
		Address: strings.TrimSpace(request.FormValue("address")), Name: strings.TrimSpace(request.FormValue("name")),
		Profile: strings.TrimSpace(request.FormValue("profile")), Enabled: true,
	}
	if rule.Address == "" {
		rule.Address = "any"
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	err := a.hostSecurity.AddWindowsFirewallRule(ctx, rule)
	cancel()
	if err != nil {
		a.recordAuditForRequest(request, "add_windows_firewall_rule", rule.Name, "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "add_windows_firewall_rule", rule.Name, "succeeded")
	securityRedirect(response, request, "defense", "windows_rule_saved", nil)
}

func (a *App) toggleWindowsFirewallRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	id := strings.TrimSpace(request.FormValue("id"))
	name := strings.TrimSpace(request.FormValue("name"))
	enabled := request.FormValue("enabled") == "true"
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	err := a.hostSecurity.SetWindowsFirewallRuleEnabled(ctx, id, enabled)
	cancel()
	if err != nil {
		a.recordAuditForRequest(request, "toggle_windows_firewall_rule", name, "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "toggle_windows_firewall_rule", name, "succeeded")
	securityRedirect(response, request, "defense", "windows_rule_saved", nil)
}

func (a *App) deleteWindowsFirewallRule(response http.ResponseWriter, request *http.Request) {
	if !a.validSecurityWrite(response, request) {
		return
	}
	id := strings.TrimSpace(request.FormValue("id"))
	name := strings.TrimSpace(request.FormValue("name"))
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	err := a.hostSecurity.DeleteWindowsFirewallRule(ctx, id)
	cancel()
	if err != nil {
		a.recordAuditForRequest(request, "delete_windows_firewall_rule", name, "failed")
		writeSecurityError(response, request, err)
		return
	}
	a.recordAuditForRequest(request, "delete_windows_firewall_rule", name, "succeeded")
	securityRedirect(response, request, "defense", "windows_rule_saved", nil)
}

func (a *App) validSecurityWrite(response http.ResponseWriter, request *http.Request) bool {
	if validSessionCSRF(request) {
		return true
	}
	http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
	return false
}

func (a *App) securityDraft(userID string) (securityFirewallDraft, bool) {
	a.securityDraftMu.Lock()
	defer a.securityDraftMu.Unlock()
	draft, ok := a.securityDrafts[userID]
	if !ok {
		return securityFirewallDraft{}, false
	}
	draft.Baseline = cloneSecurityRules(draft.Baseline)
	draft.Desired = cloneSecurityRules(draft.Desired)
	draft.Changes = append([]securityFirewallChange(nil), draft.Changes...)
	return draft, true
}

func cloneSecurityRules(rules []hostsecurity.FirewallRule) []hostsecurity.FirewallRule {
	return append([]hostsecurity.FirewallRule(nil), rules...)
}

func securityRuleDescription(rule hostsecurity.FirewallRule) string {
	state := "enabled"
	if !rule.Enabled {
		state = "disabled"
	}
	return fmt.Sprintf("%s · %s %s/%s · %s · %s", rule.Direction, rule.Action, rule.Protocol, rule.Port, rule.Address, state)
}

func securityHasSSHAllowRule(rules []hostsecurity.FirewallRule, port string) bool {
	if port == "" {
		port = "22"
	}
	for _, rule := range rules {
		if rule.Enabled && rule.Direction == hostsecurity.DirectionInbound && rule.Action == hostsecurity.ActionAllow && rule.Port == port && (rule.Protocol == "tcp" || rule.Protocol == "any") {
			return true
		}
	}
	return false
}

func securityRedirect(response http.ResponseWriter, request *http.Request, tab, notice string, extra url.Values) {
	query := url.Values{"tab": {tab}}
	if notice != "" {
		query.Set("notice", notice)
	}
	for key, values := range extra {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	http.Redirect(response, request, "/monitor/security?"+query.Encode(), http.StatusSeeOther)
}

func writeSecurityError(response http.ResponseWriter, request *http.Request, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, hostsecurity.ErrUnsupported) {
		status = http.StatusNotImplemented
	} else if errors.Is(err, hostsecurity.ErrFirewallConflict) {
		status = http.StatusConflict
	}
	http.Error(response, webText(resolveWebLocale(request), "security.operation_failed")+": "+err.Error(), status)
}
