package web_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/hostsecurity"
	app "scriptboard/internal/web"
)

func TestHostSecurityPageAndUFWDraftFlow(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{capabilities: hostsecurity.Capabilities{
		OS: "linux", Hostname: "prod-web-01", CollectedAt: time.Now().UTC(),
		Administrator: true, AdministratorKnown: true,
		SSH: hostsecurity.Component{Installed: true, Running: true},
		SSHLogin: hostsecurity.SSHLoginSurface{
			Port: "22", ListenAddresses: []string{"0.0.0.0:22", "[::]:22"},
			PublicKeyAuthentication: "yes", PasswordAuthentication: "yes", RootLogin: "prohibit-password",
		},
		Fail2Ban: hostsecurity.Component{Installed: true, Running: true},
		UFW:      hostsecurity.Component{Installed: true, Running: true}, UFWEnabled: true,
		UFWDefaults: hostsecurity.UFWDefaults{Incoming: hostsecurity.PolicyDeny, Outgoing: hostsecurity.PolicyAllow},
		Rules: []hostsecurity.FirewallRule{{
			Number: 1, Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow,
			Protocol: "tcp", Port: "22", Address: "10.0.0.1", Name: "SSH", Enabled: true,
		}},
	}, logins: hostsecurity.LoginPage{
		Records: []hostsecurity.LoginRecord{{Time: time.Now().UTC(), Result: hostsecurity.ResultFailure, User: "root", SourceIP: "203.0.113.8", Type: "ssh", Authentication: "password"}},
		Total:   1, Page: 1, Pages: 1, Stats: hostsecurity.LoginStats{Failure: 1, UniqueSources: 1},
	}, bans: hostsecurity.BanPage{Bans: []hostsecurity.Ban{{IP: "203.0.113.8", Jail: "sshd"}}, Total: 1, Page: 1, Pages: 1}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})

	overview := getSecurityPage(t, client, serverURL+"/monitor/security")
	if !bytes.Contains(overview, []byte("Failed sign-ins")) {
		t.Fatalf("security overview did not render login data: %s", overview)
	}
	for _, expected := range [][]byte{
		[]byte(`data-security-login-surface`), []byte("Remote sign-in monitoring"),
		[]byte(`data-security-login-verdict`), []byte("Monitoring status"),
		[]byte("SSH remote entry"), []byte("Public key authentication"), []byte("Password authentication"),
		[]byte("Root remote sign-in"), []byte("Brute-force protection"),
		[]byte("0.0.0.0:22"), []byte("prohibit-password"), []byte(`class="security-login-check__source"`),
		[]byte("Password authentication is enabled"),
	} {
		if !bytes.Contains(overview, expected) {
			t.Fatalf("security overview missing remote sign-in monitoring %q: %s", expected, overview)
		}
	}
	for _, forbidden := range [][]byte{[]byte(`security-activity-section`), []byte("Recent sign-ins"), []byte("203.0.113.8")} {
		if bytes.Contains(overview, forbidden) {
			t.Fatalf("security overview still renders recent sign-ins %q: %s", forbidden, overview)
		}
	}
	service.mu.Lock()
	overviewLoginCalls := service.loginCalls
	overviewBanCalls := service.banCalls
	service.mu.Unlock()
	if overviewLoginCalls != 0 {
		t.Fatalf("Linux security overview read login records %d times; journal reads belong on the logins tab", overviewLoginCalls)
	}
	if overviewBanCalls != 0 {
		t.Fatalf("Linux security overview read bans %d times; Fail2Ban journal reads belong on the defense tab", overviewBanCalls)
	}
	loginsPage := getSecurityPage(t, client, serverURL+"/monitor/security?tab=logins")
	if !bytes.Contains(loginsPage, []byte("203.0.113.8")) {
		t.Fatalf("security logins tab did not render login data: %s", loginsPage)
	}
	service.mu.Lock()
	defaultPageSize := service.lastQuery.PageSize
	service.mu.Unlock()
	if defaultPageSize != 5 {
		t.Fatalf("default login page size = %d, want 5", defaultPageSize)
	}
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	for _, expected := range [][]byte{
		[]byte(`href="/monitor/security" aria-current="page"`),
		[]byte("Host Security"), []byte("Fail2Ban"), []byte("UFW Firewall"), []byte("root privileges"),
		[]byte(`data-security-ban-drawer-trigger`), []byte(`aria-haspopup="dialog"`),
		[]byte(`class="security-ban-drawer-host"`), []byte(`data-security-ban-loading`), []byte(`aria-hidden="true"`),
		[]byte(`action="/monitor/security/firewall/draft/defaults"`), []byte("Default traffic policy"),
	} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("security page missing %q: %s", expected, page)
		}
	}
	for _, forbidden := range [][]byte{[]byte("关闭 UFW"), []byte("/monitor/security/firewall/disable")} {
		if bytes.Contains(page, forbidden) {
			t.Fatalf("security page exposes removed UFW close action %q", forbidden)
		}
	}

	bans := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense&bans=1")
	for _, expected := range [][]byte{
		[]byte(`data-security-ban-drawer`), []byte(`class="security-ban-drawer-host is-open"`),
		[]byte(`role="dialog"`), []byte(`aria-modal="true"`), []byte("203.0.113.8"),
	} {
		if !bytes.Contains(bans, expected) {
			t.Fatalf("security ban drawer missing %q: %s", expected, bans)
		}
	}
	if bytes.Contains(bans, []byte(`<dialog class="security-dialog" open aria-labelledby="security-ban-dialog-title">`)) {
		t.Fatalf("ban details still render as a modal dialog: %s", bans)
	}

	response, err := client.PostForm(serverURL+"/monitor/security/firewall/draft/rules", url.Values{
		"csrf_token": {formToken(t, page)}, "direction": {"out"}, "action": {"allow"},
		"protocol": {"udp"}, "port": {"53"}, "address": {"1.1.1.1"}, "name": {"DNS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add draft status = %d", response.StatusCode)
	}
	service.mu.Lock()
	applyCalls := service.applyCalls
	service.mu.Unlock()
	if applyCalls != 0 {
		t.Fatal("UFW rule was applied before confirmation")
	}
	response, err = client.PostForm(serverURL+"/monitor/security/firewall/draft/defaults", url.Values{
		"csrf_token": {formToken(t, page)}, "incoming": {"deny"}, "outgoing": {"deny"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update default policies status = %d", response.StatusCode)
	}

	review := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense&review=1")
	if !bytes.Contains(review, []byte("Review UFW changes")) || !bytes.Contains(review, []byte("DNS")) || !bytes.Contains(review, []byte("Default outgoing")) {
		t.Fatalf("review dialog missing draft: %s", review)
	}
	response, err = client.PostForm(serverURL+"/monitor/security/firewall/draft/apply", url.Values{"csrf_token": {formToken(t, review)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply draft status = %d", response.StatusCode)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.applyCalls != 1 || len(service.appliedDesired) != 2 || service.appliedDesired[1].Name != "DNS" || service.appliedDefaults.Outgoing != hostsecurity.PolicyDeny {
		t.Fatalf("applied draft = calls %d rules %#v defaults %#v", service.applyCalls, service.appliedDesired, service.appliedDefaults)
	}
}

func TestHostSecurityLoginFiltersArePassedToService(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{
		capabilities: hostsecurity.Capabilities{
			OS: "windows", Hostname: "win-app-02", CollectedAt: time.Now().UTC(), Firewall: hostsecurity.Component{Installed: true, Running: true},
			Administrator: true, AdministratorKnown: true,
			Profiles: []hostsecurity.FirewallProfile{{Name: "Public", Enabled: true}},
			Rules:    []hostsecurity.FirewallRule{{ID: "RemoteDesktop-Rule-ID", Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow, Protocol: "tcp", Port: "3389", Address: "10.0.0.0/24", Name: "Remote Desktop", Profile: "Public", Enabled: true}},
		},
		logins: hostsecurity.LoginPage{Page: 1, Pages: 1},
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=logins&range=7d&result=failure&type=rdp&page_size=50&from=2026-08-01&to=2026-08-05&refresh=1")
	if !bytes.Contains(page, []byte("Windows sign-in audit")) || !bytes.Contains(page, []byte(`value="failure" selected`)) {
		t.Fatalf("filtered login page is incomplete: %s", page)
	}
	service.mu.Lock()
	query := service.lastQuery
	service.mu.Unlock()
	if query.Range != "7d" || query.Result != hostsecurity.ResultFailure || query.Type != "rdp" || query.PageSize != 50 || query.Start.IsZero() || query.End.IsZero() || !query.Refresh {
		t.Fatalf("query = %#v", query)
	}
	defense := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	for _, expected := range [][]byte{[]byte("Windows Defender Firewall"), []byte("Remote Desktop"), []byte(`/monitor/security/windows-firewall/rules/new`), []byte(`data-task-link`), []byte(`data-security-tabs`), []byte(`name="rule_protocol"`), []byte(`name="rule_port"`), []byte(`name="rule_address"`), []byte("Refresh data"), []byte("Administrator privileges")} {
		if !bytes.Contains(defense, expected) {
			t.Fatalf("Windows security page missing %q: %s", expected, defense)
		}
	}
	task := getSecurityPage(t, client, serverURL+"/monitor/security/windows-firewall/rules/new")
	for _, expected := range [][]byte{[]byte(`data-task-kind="windows-firewall-rule"`), []byte(`action="/monitor/security/windows-firewall/rules"`), []byte(`name="profile"`), []byte(`name="address"`)} {
		if !bytes.Contains(task, expected) {
			t.Fatalf("Windows firewall rule task missing %q: %s", expected, task)
		}
	}
}

func TestHostSecurityUpdatesPageIsReadOnlyAndShowsBoundedProviderInventory(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{
		capabilities: hostsecurity.Capabilities{OS: "windows", Hostname: "win-update-01", CollectedAt: time.Now().UTC(), Firewall: hostsecurity.Component{Installed: true, Running: true}},
		updateReport: hostsecurity.SecurityUpdateReport{Supported: true, Provider: "Windows Update Agent", CollectedAt: time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC), Updates: []hostsecurity.SecurityUpdate{{
			Identifier: "update-id", Title: "Cumulative Security Update", Version: "KB123456", Severity: "Critical", Source: "Security Updates", RestartRequired: true,
		}}},
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=updates&refresh=1")
	for _, expected := range [][]byte{[]byte("Available OS security updates"), []byte("Windows Update Agent"), []byte("Cumulative Security Update"), []byte("KB123456"), []byte("Critical"), []byte("Required"), []byte("does not refresh indexes, download, or install")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("security updates page missing %q: %s", expected, page)
		}
	}
	for _, forbidden := range [][]byte{[]byte("Install update"), []byte("apt-get update"), []byte("Download update")} {
		if bytes.Contains(page, forbidden) {
			t.Fatalf("read-only security updates page exposes mutation text %q: %s", forbidden, page)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.updateCalls != 1 || !service.updateRefresh {
		t.Fatalf("update calls=%d refresh=%v", service.updateCalls, service.updateRefresh)
	}
}

func TestHostSecurityBaselineExplainsEffectiveChecksAndCapturesStatusOnlyHistory(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{
		capabilities: hostsecurity.Capabilities{
			OS: "linux", Hostname: "baseline-01", CollectedAt: time.Now().UTC(), AdministratorKnown: true,
			Firewall: hostsecurity.Component{Installed: true, Running: true}, SSH: hostsecurity.Component{Installed: true, Running: true},
			SSHLogin: hostsecurity.SSHLoginSurface{PublicKeyAuthentication: "yes", PasswordAuthentication: "yes", RootLogin: "prohibit-password", EmptyPasswords: "no", MaxAuthTries: 3},
			Fail2Ban: hostsecurity.Component{Installed: true, Running: true},
		},
		updateReport: hostsecurity.SecurityUpdateReport{Supported: true, Provider: "APT package metadata", Updates: []hostsecurity.SecurityUpdate{{Identifier: "openssl"}}},
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, HostSecurity: service})
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=baseline")
	for _, expected := range [][]byte{[]byte("Host security baseline"), []byte("77 / 100"), []byte("SSH password authentication"), []byte("OS security updates"), []byte("Web control plane least privilege"), []byte("not a compliance certification"), []byte("Capture baseline snapshot"), []byte("No baseline snapshot exists yet")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("security baseline page missing %q: %s", expected, page)
		}
	}
	for _, forbidden := range [][]byte{[]byte("Apply baseline"), []byte("Fix all")} {
		if bytes.Contains(page, forbidden) {
			t.Fatalf("read-only baseline exposes mutation %q: %s", forbidden, page)
		}
	}
	response, err := client.PostForm(serverURL+"/monitor/security/baseline/snapshot", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("capture baseline status=%d", response.StatusCode)
	}
	capturedPage := getSecurityPage(t, client, serverURL+response.Header.Get("Location"))
	if !bytes.Contains(capturedPage, []byte("Security baseline snapshot captured")) || !bytes.Contains(capturedPage, []byte("Current check statuses match the latest snapshot")) {
		t.Fatalf("captured baseline page=%s", capturedPage)
	}
	history, err := os.ReadFile(filepath.Join(stateRoot, "security-baseline", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(history, []byte("Web process")) || bytes.Contains(history, []byte("pending security updates")) {
		t.Fatalf("baseline history persisted evidence text: %s", history)
	}
}

func TestWindowsFirewallRulesSupportFilteringAndPagination(t *testing.T) {
	t.Parallel()
	rules := make([]hostsecurity.FirewallRule, 25)
	for index := range rules {
		protocol := "tcp"
		if index%2 == 1 {
			protocol = "udp"
		}
		rules[index] = hostsecurity.FirewallRule{
			ID: strconv.Itoa(index), Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow,
			Protocol: protocol, Port: strconv.Itoa(10000 + index), Address: "10.20.0." + strconv.Itoa(index),
			Name: "Fixture rule " + strconv.Itoa(index), Enabled: index%3 != 0,
		}
	}
	service := &securityFixtureService{
		capabilities: hostsecurity.Capabilities{OS: "windows", Firewall: hostsecurity.Component{Installed: true, Running: true}, Rules: rules},
		logins:       hostsecurity.LoginPage{Page: 1, Pages: 1},
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})

	secondPage := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense&rule_page=2")
	if !bytes.Contains(secondPage, []byte("10020")) || bytes.Contains(secondPage, []byte("10000")) || !bytes.Contains(secondPage, []byte("25 records · 2 / 2")) {
		t.Fatalf("Windows firewall pagination is incomplete: %s", secondPage)
	}
	filtered := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense&rule_protocol=udp&rule_port=1001&rule_address=10.20.0.13&rule_direction=in&rule_status=enabled")
	if !bytes.Contains(filtered, []byte("10013")) || bytes.Contains(filtered, []byte("10011")) || !bytes.Contains(filtered, []byte(`value="udp" selected`)) {
		t.Fatalf("Windows firewall filters are incomplete: %s", filtered)
	}
}

func TestHostSecurityPageShowsStandardUserPrivilege(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{capabilities: hostsecurity.Capabilities{
		OS: "windows", Hostname: "win-standard", CollectedAt: time.Now().UTC(),
		AdministratorKnown: true, Firewall: hostsecurity.Component{Installed: true, Running: true},
	}, logins: hostsecurity.LoginPage{Page: 1, Pages: 1}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})

	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=overview")
	if !bytes.Contains(page, []byte("Standard user privileges")) || bytes.Contains(page, []byte("Administrator privileges")) {
		t.Fatalf("standard-user privilege state is not explicit: %s", page)
	}
}

func TestHostSecurityDeferredShellSkipsSystemCollection(t *testing.T) {
	service := &securityFixtureService{}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})
	request, err := http.NewRequest(http.MethodGet, serverURL+"/monitor/security?tab=overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-ScriptBoard-Navigation", "pjax")
	request.Header.Set("X-ScriptBoard-Data", "shell")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d: %s", response.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`data-deferred-state="loading"`)) {
		t.Fatalf("security shell has no loading state: %s", body)
	}
	if !bytes.Contains(body, []byte("Host security data")) || bytes.Contains(body, []byte("Windows Security Event Log")) {
		t.Fatalf("security shell loading copy is not platform-neutral: %s", body)
	}
	if service.capabilityCalls != 0 || service.loginCalls != 0 {
		t.Fatalf("deferred shell performed system collection: capabilities=%d logins=%d", service.capabilityCalls, service.loginCalls)
	}
}

func TestHostSecurityCanToggleFirstAddedDraftRule(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{capabilities: hostsecurity.Capabilities{
		OS: "linux", Administrator: true, AdministratorKnown: true,
		UFW: hostsecurity.Component{Installed: true, Running: true},
	}}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})

	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	response, err := client.PostForm(serverURL+"/monitor/security/firewall/draft/rules", url.Values{
		"csrf_token": {formToken(t, page)}, "direction": {"in"}, "action": {"allow"},
		"protocol": {"tcp"}, "port": {"22"}, "address": {"Anywhere"}, "name": {"SSH"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add first draft status = %d", response.StatusCode)
	}

	draftPage := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	response, err = client.PostForm(serverURL+"/monitor/security/firewall/draft/toggle", url.Values{
		"csrf_token": {formToken(t, draftPage)}, "index": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	toggleBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("toggle first draft status = %d body=%s", response.StatusCode, toggleBody)
	}

	toggledPage := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	if !bytes.Contains(toggledPage, []byte(`data-enabled="false"`)) || !bytes.Contains(toggledPage, []byte("Enable rule")) {
		t.Fatalf("first added draft rule was not disabled: %s", toggledPage)
	}
}

func getSecurityPage(t *testing.T, client *http.Client, target string) []byte {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d body=%s", target, response.StatusCode, body)
	}
	return body
}

type securityFixtureService struct {
	mu              sync.Mutex
	capabilities    hostsecurity.Capabilities
	logins          hostsecurity.LoginPage
	bans            hostsecurity.BanPage
	updateReport    hostsecurity.SecurityUpdateReport
	lastQuery       hostsecurity.LoginQuery
	capabilityCalls int
	loginCalls      int
	banCalls        int
	updateCalls     int
	updateRefresh   bool
	applyCalls      int
	appliedDesired  []hostsecurity.FirewallRule
	appliedDefaults hostsecurity.UFWDefaults
}

func (s *securityFixtureService) Capabilities(context.Context) hostsecurity.Capabilities {
	s.mu.Lock()
	s.capabilityCalls++
	s.mu.Unlock()
	return s.capabilities
}

func (s *securityFixtureService) SecurityUpdates(_ context.Context, refresh bool) (hostsecurity.SecurityUpdateReport, error) {
	s.mu.Lock()
	s.updateCalls++
	s.updateRefresh = refresh
	s.mu.Unlock()
	return s.updateReport, nil
}

func (s *securityFixtureService) Logins(_ context.Context, query hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	s.mu.Lock()
	s.lastQuery = query
	s.loginCalls++
	s.mu.Unlock()
	return s.logins, nil
}

func (s *securityFixtureService) Bans(context.Context, int, int) (hostsecurity.BanPage, error) {
	s.mu.Lock()
	s.banCalls++
	s.mu.Unlock()
	return s.bans, nil
}

func (s *securityFixtureService) Install(context.Context, string) error { return nil }

func (s *securityFixtureService) Unban(context.Context, string, string) error { return nil }

func (s *securityFixtureService) EnableUFW(context.Context, []hostsecurity.FirewallRule) error {
	return nil
}

func (s *securityFixtureService) ApplyUFW(_ context.Context, _ []hostsecurity.FirewallRule, desired []hostsecurity.FirewallRule, _ hostsecurity.UFWDefaults, desiredDefaults hostsecurity.UFWDefaults) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	s.appliedDesired = append([]hostsecurity.FirewallRule(nil), desired...)
	s.appliedDefaults = desiredDefaults
	return nil
}

func (s *securityFixtureService) AddWindowsFirewallRule(context.Context, hostsecurity.FirewallRule) error {
	return nil
}

func (s *securityFixtureService) SetWindowsFirewallRuleEnabled(context.Context, string, bool) error {
	return nil
}

func (s *securityFixtureService) DeleteWindowsFirewallRule(context.Context, string) error {
	return nil
}
