package hostsecurity

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestParseLinuxLogins(t *testing.T) {
	input := `2026-08-05T14:32:01+08:00 host sshd[10]: Failed password for invalid user root from 45.148.10.72 port 5512 ssh2
2026-08-05T14:18:02+08:00 host sshd[11]: Accepted publickey for deploy from 10.20.1.16 port 4422 ssh2: ED25519 SHA256:test
noise that must be ignored`
	records := parseLinuxLogins(input)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %#v", len(records), records)
	}
	if records[0].Result != ResultFailure || records[0].User != "root" || records[0].SourceIP != "45.148.10.72" || records[0].Authentication != "password" {
		t.Fatalf("failure record = %#v", records[0])
	}
	if records[1].Result != ResultSuccess || records[1].User != "deploy" || records[1].Authentication != "publickey" {
		t.Fatalf("success record = %#v", records[1])
	}
}

func TestParseUFWStatusKeepsDirection(t *testing.T) {
	input := `Status: active

To                         Action      From
--                         ------      ----
[ 1] 22/tcp                 ALLOW IN    120.26.81.32
[ 2] 53/udp                 ALLOW OUT   Anywhere                   (out)`
	active, rules := parseUFWStatus(input)
	if !active || len(rules) != 2 {
		t.Fatalf("active=%v rules=%#v", active, rules)
	}
	if rules[0].Direction != DirectionInbound || rules[0].Port != "22" || rules[0].Protocol != "tcp" {
		t.Fatalf("inbound rule = %#v", rules[0])
	}
	if rules[1].Direction != DirectionOutbound || rules[1].Port != "53" || rules[1].Protocol != "udp" {
		t.Fatalf("outbound rule = %#v", rules[1])
	}
}

func TestParseFail2BanEventsKeepsLatestBanTime(t *testing.T) {
	events := parseFail2BanEvents(`2026-08-05T10:00:00+08:00 host fail2ban.actions [10]: NOTICE [sshd] Ban 203.0.113.8
2026-08-05T12:30:00+08:00 host fail2ban.actions [10]: NOTICE [sshd] Ban 203.0.113.8
2026-08-05T13:00:00+08:00 host fail2ban.actions [10]: NOTICE [nginx] Ban 198.51.100.9`, "sshd")
	want := time.Date(2026, 8, 5, 4, 30, 0, 0, time.UTC)
	if !events["203.0.113.8"].Equal(want) || len(events) != 1 {
		t.Fatalf("events = %#v, want latest sshd ban %s", events, want)
	}
}

func TestApplyUFWRejectsConcurrentBaselineChange(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: "Status: active\n[ 1] 443/tcp ALLOW IN Anywhere\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	baseline := []FirewallRule{{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "Anywhere", Enabled: true}}
	desired := append([]FirewallRule(nil), baseline...)
	desired = append(desired, FirewallRule{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "443", Address: "Anywhere", Enabled: true})
	if err := manager.ApplyUFW(context.Background(), baseline, desired); err != ErrFirewallConflict {
		t.Fatalf("ApplyUFW error = %v, want %v", err, ErrFirewallConflict)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v, expected read only", runner.calls)
	}
}

func TestApplyUFWDeletesDescendingThenAdds(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: "Status: active\n[ 1] 22/tcp ALLOW IN 10.0.0.1\n[ 2] 80/tcp ALLOW IN Anywhere\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	baseline := []FirewallRule{
		{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "10.0.0.1", Enabled: true},
		{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "80", Address: "Anywhere", Enabled: true},
	}
	desired := []FirewallRule{
		baseline[0],
		{Direction: DirectionOutbound, Action: ActionAllow, Protocol: "udp", Port: "53", Address: "Anywhere", Enabled: true},
	}
	if err := manager.ApplyUFW(context.Background(), baseline, desired); err != nil {
		t.Fatalf("ApplyUFW: %v", err)
	}
	want := []string{
		"ufw status numbered",
		"ufw --force delete 2",
		"ufw allow out proto udp to any port 53",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestEnableUFWRequiresSSHAllowRule(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	err := manager.EnableUFW(context.Background(), []FirewallRule{{
		Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "443", Address: "Anywhere", Enabled: true,
	}})
	if err != ErrSSHRuleRequired {
		t.Fatalf("error = %v, want %v", err, ErrSSHRuleRequired)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unexpected calls: %#v", runner.calls)
	}
}

func TestEnableUFWUsesDetectedSSHPort(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath sshd": {}, "sshd -T": {stdout: "port 2222\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	err := manager.EnableUFW(context.Background(), []FirewallRule{{
		Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "2222", Address: "Anywhere", Enabled: true,
	}})
	if err != nil {
		t.Fatalf("EnableUFW: %v", err)
	}
	want := []string{"sshd -T", "ufw --force enable"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestWindowsFirewallUsesStructuredNetshArguments(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "windows", Runner: runner, Now: time.Now})
	err := manager.AddWindowsFirewallRule(context.Background(), FirewallRule{
		Direction: DirectionOutbound, Action: ActionAllow, Protocol: "udp", Port: "53",
		Address: "1.1.1.1", Name: "DNS & echo not-a-command", Profile: "public", Enabled: true,
	})
	if err != nil {
		t.Fatalf("AddWindowsFirewallRule: %v", err)
	}
	if len(runner.callArgs) != 1 || runner.callArgs[0][0] != "netsh.exe" {
		t.Fatalf("calls = %#v", runner.callArgs)
	}
	wantName := "name=DNS & echo not-a-command"
	found := false
	for _, argument := range runner.callArgs[0][1:] {
		if argument == wantName {
			found = true
		}
	}
	if !found {
		t.Fatalf("structured name argument missing from %#v", runner.callArgs[0])
	}
}

type fakeResponse struct {
	stdout string
	err    error
}

type fakeRunner struct {
	responses map[string]fakeResponse
	calls     []string
	callArgs  [][]string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	r.calls = append(r.calls, key)
	r.callArgs = append(r.callArgs, append([]string{name}, args...))
	response := r.responses[key]
	return response.stdout, response.err
}

func (r *fakeRunner) LookPath(name string) bool {
	_, ok := r.responses["lookpath "+name]
	return ok
}
