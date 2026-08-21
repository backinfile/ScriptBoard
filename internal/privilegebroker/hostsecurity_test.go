package privilegebroker

import (
	"context"
	"encoding/json"
	"testing"

	"scriptboard/internal/hostsecurity"
)

func TestBrokeredHostSecurityNeverCallsReaderMutationMethods(t *testing.T) {
	baseline := hostsecurity.FirewallRule{
		ID: "rule-1", Name: "Rule one", Direction: hostsecurity.DirectionInbound,
		Action: hostsecurity.ActionAllow, Protocol: "tcp", Port: "443", Address: "any", Enabled: true,
	}
	reader := &fixtureHostSecurity{capabilities: hostsecurity.Capabilities{OS: "windows", Rules: []hostsecurity.FirewallRule{baseline}}}
	direct := &fixtureHostSecurity{capabilities: hostsecurity.Capabilities{OS: "windows", Rules: []hostsecurity.FirewallRule{baseline}}}
	executor, err := NewHostSecurityExecutor(direct)
	if err != nil {
		t.Fatal(err)
	}
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, executor)
	defer server.Close()
	service, err := NewHostSecurityService(reader, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-host-1"})
	if err := service.SetWindowsFirewallRuleEnabled(ctx, baseline.ID, false); err != nil {
		t.Fatal(err)
	}
	if reader.mutationCalls != 0 || direct.mutationCalls != 1 || direct.lastID != baseline.ID || direct.lastEnabled {
		t.Fatalf("reader mutations=%d direct mutations=%d id=%q enabled=%v", reader.mutationCalls, direct.mutationCalls, direct.lastID, direct.lastEnabled)
	}
}

func TestBrokerExecutorRejectsChangedWindowsFirewallRevision(t *testing.T) {
	baseline := hostsecurity.FirewallRule{ID: "rule-1", Name: "Before", Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow, Protocol: "tcp", Port: "443", Address: "any", Enabled: true}
	changed := baseline
	changed.Name = "Changed outside the draft"
	reader := &fixtureHostSecurity{capabilities: hostsecurity.Capabilities{OS: "windows", Rules: []hostsecurity.FirewallRule{baseline}}}
	direct := &fixtureHostSecurity{capabilities: hostsecurity.Capabilities{OS: "windows", Rules: []hostsecurity.FirewallRule{changed}}}
	executor, _ := NewHostSecurityExecutor(direct)
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, executor)
	defer server.Close()
	service, _ := NewHostSecurityService(reader, client)
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-host-2"})
	if err := service.DeleteWindowsFirewallRule(ctx, baseline.ID); err == nil {
		t.Fatal("Broker deleted a firewall rule after its revision changed")
	}
	if direct.mutationCalls != 0 {
		t.Fatalf("direct mutation calls=%d", direct.mutationCalls)
	}
}

func TestBrokerExecutorRejectsResourceThatDisagreesWithTypedParameters(t *testing.T) {
	baseline := hostsecurity.FirewallRule{ID: "critical-rule", Name: "Critical", Direction: hostsecurity.DirectionInbound, Action: hostsecurity.ActionAllow, Protocol: "tcp", Port: "443", Address: "any", Enabled: true}
	direct := &fixtureHostSecurity{capabilities: hostsecurity.Capabilities{OS: "windows", Rules: []hostsecurity.FirewallRule{baseline}}}
	executor, _ := NewHostSecurityExecutor(direct)
	parameters, _ := json.Marshal(deleteWindowsFirewallParameters{ID: baseline.ID, Baseline: baseline})
	err := executor.Execute(context.Background(), ExecutionRequest{
		Action: ActionWindowsFirewallDelete, Resource: "harmless-rule", Revision: ruleRevision(baseline), Parameters: parameters,
	})
	if err == nil || direct.mutationCalls != 0 {
		t.Fatalf("semantic binding error=%v mutation calls=%d", err, direct.mutationCalls)
	}
}

func TestBrokeredHostSecurityBansThroughTypedAction(t *testing.T) {
	reader := &fixtureHostSecurity{}
	direct := &fixtureHostSecurity{}
	executor, _ := NewHostSecurityExecutor(direct)
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Role: "administrator"}}, executor)
	defer server.Close()
	service, _ := NewHostSecurityService(reader, client)
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "request-host-ban"})
	if err := service.Ban(ctx, "sshd", "203.0.113.8"); err != nil {
		t.Fatal(err)
	}
	if reader.mutationCalls != 0 || direct.mutationCalls != 1 || direct.lastID != "sshd:203.0.113.8" {
		t.Fatalf("reader mutations=%d direct mutations=%d target=%q", reader.mutationCalls, direct.mutationCalls, direct.lastID)
	}
}

type fixtureHostSecurity struct {
	capabilities    hostsecurity.Capabilities
	updateReport    hostsecurity.SecurityUpdateReport
	logins          hostsecurity.LoginPage
	bans            hostsecurity.BanPage
	capabilityReads int
	updateReads     int
	loginReads      int
	banReads        int
	mutationCalls   int
	lastID          string
	lastEnabled     bool
}

func (fixture *fixtureHostSecurity) Capabilities(context.Context) hostsecurity.Capabilities {
	fixture.capabilityReads++
	return fixture.capabilities
}
func (fixture *fixtureHostSecurity) SecurityUpdates(context.Context, bool) (hostsecurity.SecurityUpdateReport, error) {
	fixture.updateReads++
	return fixture.updateReport, nil
}
func (fixture *fixtureHostSecurity) Logins(context.Context, hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	fixture.loginReads++
	return fixture.logins, nil
}
func (fixture *fixtureHostSecurity) Bans(context.Context, int, int) (hostsecurity.BanPage, error) {
	fixture.banReads++
	return fixture.bans, nil
}
func (fixture *fixtureHostSecurity) Install(context.Context, string) error {
	fixture.mutationCalls++
	return nil
}
func (fixture *fixtureHostSecurity) Unban(context.Context, string, string) error {
	fixture.mutationCalls++
	return nil
}
func (fixture *fixtureHostSecurity) Ban(_ context.Context, jail, ip string) error {
	fixture.mutationCalls++
	fixture.lastID = jail + ":" + ip
	return nil
}
func (fixture *fixtureHostSecurity) EnableUFW(context.Context, []hostsecurity.FirewallRule) error {
	fixture.mutationCalls++
	return nil
}
func (fixture *fixtureHostSecurity) ApplyUFW(context.Context, []hostsecurity.FirewallRule, []hostsecurity.FirewallRule, hostsecurity.UFWDefaults, hostsecurity.UFWDefaults) error {
	fixture.mutationCalls++
	return nil
}
func (fixture *fixtureHostSecurity) AddWindowsFirewallRule(context.Context, hostsecurity.FirewallRule) error {
	fixture.mutationCalls++
	return nil
}
func (fixture *fixtureHostSecurity) SetWindowsFirewallRuleEnabled(_ context.Context, id string, enabled bool) error {
	fixture.mutationCalls++
	fixture.lastID = id
	fixture.lastEnabled = enabled
	return nil
}
func (fixture *fixtureHostSecurity) DeleteWindowsFirewallRule(_ context.Context, id string) error {
	fixture.mutationCalls++
	fixture.lastID = id
	return nil
}

var _ hostsecurity.Service = (*fixtureHostSecurity)(nil)
