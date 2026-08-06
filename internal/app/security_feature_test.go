package app_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/hostsecurity"
)

func TestHostSecurityPageAndUFWDraftFlow(t *testing.T) {
	t.Parallel()
	service := &securityFixtureService{capabilities: hostsecurity.Capabilities{
		OS: "linux", Hostname: "prod-web-01", CollectedAt: time.Now().UTC(),
		Administrator: true, AdministratorKnown: true,
		Fail2Ban: hostsecurity.Component{Installed: true, Running: true},
		UFW:      hostsecurity.Component{Installed: true, Running: true}, UFWEnabled: true,
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
	if !bytes.Contains(overview, []byte("203.0.113.8")) || !bytes.Contains(overview, []byte("Failed sign-ins")) {
		t.Fatalf("security overview did not render login data: %s", overview)
	}
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	for _, expected := range [][]byte{
		[]byte(`href="/monitor/security" aria-current="page"`),
		[]byte("Host Security"), []byte("Fail2Ban"), []byte("UFW Firewall"), []byte("root privileges"),
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

	review := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense&review=1")
	if !bytes.Contains(review, []byte("Review UFW changes")) || !bytes.Contains(review, []byte("DNS")) {
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
	if service.applyCalls != 1 || len(service.appliedDesired) != 2 || service.appliedDesired[1].Name != "DNS" {
		t.Fatalf("applied draft = calls %d rules %#v", service.applyCalls, service.appliedDesired)
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
	if !bytes.Contains(body, []byte("Windows Security Event Log")) {
		t.Fatalf("security shell does not explain the slow Windows collection: %s", body)
	}
	if service.capabilityCalls != 0 || service.loginCalls != 0 {
		t.Fatalf("deferred shell performed system collection: capabilities=%d logins=%d", service.capabilityCalls, service.loginCalls)
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
	lastQuery       hostsecurity.LoginQuery
	capabilityCalls int
	loginCalls      int
	applyCalls      int
	appliedDesired  []hostsecurity.FirewallRule
}

func (s *securityFixtureService) Capabilities(context.Context) hostsecurity.Capabilities {
	s.mu.Lock()
	s.capabilityCalls++
	s.mu.Unlock()
	return s.capabilities
}

func (s *securityFixtureService) Logins(_ context.Context, query hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	s.mu.Lock()
	s.lastQuery = query
	s.loginCalls++
	s.mu.Unlock()
	return s.logins, nil
}

func (s *securityFixtureService) Bans(context.Context, int, int) (hostsecurity.BanPage, error) {
	return s.bans, nil
}

func (s *securityFixtureService) Install(context.Context, string) error { return nil }

func (s *securityFixtureService) Unban(context.Context, string, string) error { return nil }

func (s *securityFixtureService) EnableUFW(context.Context, []hostsecurity.FirewallRule) error {
	return nil
}

func (s *securityFixtureService) ApplyUFW(_ context.Context, _ []hostsecurity.FirewallRule, desired []hostsecurity.FirewallRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	s.appliedDesired = append([]hostsecurity.FirewallRule(nil), desired...)
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
