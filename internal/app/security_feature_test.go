package app_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
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
		[]byte("Host Security"), []byte("Fail2Ban"), []byte("UFW Firewall"),
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
			Profiles: []hostsecurity.FirewallProfile{{Name: "Public", Enabled: true}},
			Rules:    []hostsecurity.FirewallRule{{ID: "RemoteDesktop-Rule-ID", Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow, Protocol: "tcp", Port: "3389", Address: "10.0.0.0/24", Name: "Remote Desktop", Profile: "Public", Enabled: true}},
		},
		logins: hostsecurity.LoginPage{Page: 1, Pages: 1},
	}
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: filepath.Join(t.TempDir(), "state"), HostSecurity: service})
	page := getSecurityPage(t, client, serverURL+"/monitor/security?tab=logins&range=7d&result=failure&type=rdp&page_size=50&from=2026-08-01&to=2026-08-05")
	if !bytes.Contains(page, []byte("Windows sign-in audit")) || !bytes.Contains(page, []byte(`value="failure" selected`)) {
		t.Fatalf("filtered login page is incomplete: %s", page)
	}
	service.mu.Lock()
	query := service.lastQuery
	service.mu.Unlock()
	if query.Range != "7d" || query.Result != hostsecurity.ResultFailure || query.Type != "rdp" || query.PageSize != 50 || query.Start.IsZero() || query.End.IsZero() {
		t.Fatalf("query = %#v", query)
	}
	defense := getSecurityPage(t, client, serverURL+"/monitor/security?tab=defense")
	for _, expected := range [][]byte{[]byte("Windows Defender Firewall"), []byte("Remote Desktop"), []byte("/monitor/security/windows-firewall/rules")} {
		if !bytes.Contains(defense, expected) {
			t.Fatalf("Windows security page missing %q: %s", expected, defense)
		}
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
	mu             sync.Mutex
	capabilities   hostsecurity.Capabilities
	logins         hostsecurity.LoginPage
	bans           hostsecurity.BanPage
	lastQuery      hostsecurity.LoginQuery
	applyCalls     int
	appliedDesired []hostsecurity.FirewallRule
}

func (s *securityFixtureService) Capabilities(context.Context) hostsecurity.Capabilities {
	return s.capabilities
}

func (s *securityFixtureService) Logins(_ context.Context, query hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	s.mu.Lock()
	s.lastQuery = query
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
