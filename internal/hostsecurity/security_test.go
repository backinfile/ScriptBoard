package hostsecurity

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLinuxSecurityUpdatesUseExistingAPTMetadataAndReturnOnlySecurityOrigins(t *testing.T) {
	now := time.Date(2026, time.August, 12, 8, 0, 0, 0, time.UTC)
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath apt-get": {},
		"apt-get -s -o Debug::NoLocking=1 upgrade": {stdout: strings.Join([]string{
			"Inst openssl [3.0.1] (3.0.2 Ubuntu:24.04/noble-security [amd64])",
			"Inst curl [8.0.0] (8.0.1 Ubuntu:24.04/noble-updates [amd64])",
			"Inst linux-image-generic [1.0] (1.1 UbuntuESMApps:24.04/noble-apps-security [amd64])",
		}, "\n")},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: func() time.Time { return now }})

	report, err := manager.SecurityUpdates(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported || report.Provider != "APT package metadata" || len(report.Updates) != 2 {
		t.Fatalf("APT security update report = %#v", report)
	}
	if report.Updates[0].Identifier != "openssl" || report.Updates[1].Identifier != "linux-image-generic" {
		t.Fatalf("APT security updates = %#v", report.Updates)
	}
	if _, err := manager.SecurityUpdates(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("security update cache calls = %#v", runner.calls)
	}
}

func TestParseWindowsSecurityUpdatesRejectsUnboundedRecords(t *testing.T) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	line := strings.Join([]string{"SBUPDATE", encode("update-id"), encode("Cumulative Security Update"), encode("KB123456"), encode("Critical"), encode("Security Updates"), "1"}, "|")
	updates, err := parseWindowsSecurityUpdates(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Identifier != "update-id" || !updates[0].RestartRequired || updates[0].Severity != "Critical" {
		t.Fatalf("Windows security updates = %#v", updates)
	}
	if _, err := parseWindowsSecurityUpdates(line + "\n" + "SBUPDATE|bad"); err == nil {
		t.Fatal("malformed Windows update output was accepted")
	}
}

func TestWindowsLoginScriptTreatsNoMatchingEventsAsEmptyJSON(t *testing.T) {
	for _, expected := range []string{
		`Get-WinEvent -ErrorAction SilentlyContinue`,
		`ConvertTo-Json -InputObject $events -Compress`,
	} {
		if !strings.Contains(windowsLoginScript, expected) {
			t.Fatalf("Windows login script is missing %q", expected)
		}
	}
}

func TestNormalizeLoginQueryDefaultsToFiveRecords(t *testing.T) {
	query := normalizeLoginQuery(LoginQuery{})
	if query.PageSize != 5 {
		t.Fatalf("page size = %d, want 5", query.PageSize)
	}
	for _, pageSize := range []int{5, 20, 50, 100} {
		query = normalizeLoginQuery(LoginQuery{PageSize: pageSize})
		if query.PageSize != pageSize {
			t.Fatalf("page size = %d, want allowed value %d", query.PageSize, pageSize)
		}
	}
}

func TestLinuxLoginJournalReadIsBounded(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: func() time.Time {
		return time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	}})

	if _, err := manager.Logins(context.Background(), LoginQuery{Range: "30d", Page: 1, PageSize: 20}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "--lines 5000") {
		t.Fatalf("Linux journal call is not bounded: %#v", runner.calls)
	}
	if strings.Contains(runner.calls[0], "--since") {
		t.Fatalf("Linux journal call scans from a time boundary instead of reading the bounded tail: %#v", runner.calls)
	}
}

func TestLinuxLoginCacheAvoidsRepeatedJournalProbes(t *testing.T) {
	now := time.Date(2026, time.August, 6, 2, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: func() time.Time { return now }})
	query := LoginQuery{Range: "24h", Page: 1, PageSize: 20}

	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("Linux journal probes=%d, want 1 within the cache lifetime; calls=%#v", len(runner.calls), runner.calls)
	}
}

func TestFail2BanJournalReadUsesBoundedTail(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath fail2ban-client": {},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})

	if _, err := manager.Bans(context.Background(), 1, 20); err != nil {
		t.Fatal(err)
	}
	journalCall := runner.calls[len(runner.calls)-1]
	if !strings.Contains(journalCall, "journalctl -u fail2ban") || !strings.Contains(journalCall, "--lines 5000") {
		t.Fatalf("Fail2Ban journal call is not bounded: %#v", runner.calls)
	}
	if strings.Contains(journalCall, "--since") {
		t.Fatalf("Fail2Ban journal call scans from a time boundary instead of reading the bounded tail: %#v", runner.calls)
	}
}

func TestBansIncludesCurrentBansFromEveryActiveJail(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath fail2ban-client":                                              {},
		"fail2ban-client status":                                                {stdout: "Status\n|- Number of jail:\t2\n`- Jail list:\tsshd, nginx-http-auth\n"},
		"fail2ban-client status nginx-http-auth":                                {stdout: "Status for the jail: nginx-http-auth\n`- Actions\n   |- Currently banned:\t1\n   |- Total banned:\t3\n   `- Banned IP list:\t198.51.100.9\n"},
		"fail2ban-client status sshd":                                           {stdout: "Status for the jail: sshd\n`- Actions\n   |- Currently banned:\t1\n   |- Total banned:\t8\n   `- Banned IP list:\t203.0.113.8\n"},
		"fail2ban-client get nginx-http-auth bantime":                           {stdout: "600\n"},
		"fail2ban-client get sshd bantime":                                      {stdout: "600\n"},
		"journalctl -u fail2ban --lines 5000 --no-pager -o short-iso --reverse": {},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})

	page, err := manager.Bans(context.Background(), 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Bans) != 2 || page.Bans[0].Jail != "nginx-http-auth" || page.Bans[1].Jail != "sshd" {
		t.Fatalf("page = %#v, want current bans from both active jails", page)
	}
}

func TestBansReportsActiveJailWithoutCurrentBans(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath fail2ban-client":         {},
		"fail2ban-client status":           {stdout: "Status\n|- Number of jail:\t1\n`- Jail list:\tsshd\n"},
		"fail2ban-client status sshd":      {stdout: "Status for the jail: sshd\n`- Actions\n   |- Currently banned:\t0\n   |- Total banned:\t8\n   `- Banned IP list:\t\n"},
		"fail2ban-client get sshd bantime": {stdout: "600\n"},
		"journalctl -u fail2ban --lines 5000 --no-pager -o short-iso --reverse": {},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})

	page, err := manager.Bans(context.Background(), 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Jails) != 1 || page.Jails[0].Name != "sshd" || page.Jails[0].CurrentlyBanned != 0 || page.Jails[0].TotalBanned != 8 {
		t.Fatalf("page = %#v, want the active jail summary even without a current ban", page)
	}
}

func TestCapabilitiesCacheAvoidsRepeatedWindowsFirewallProbes(t *testing.T) {
	now := time.Date(2026, time.August, 6, 2, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "windows", Runner: runner, Now: func() time.Time { return now }})

	manager.Capabilities(context.Background())
	now = now.Add(10 * time.Second)
	manager.Capabilities(context.Background())

	probes := 0
	for _, call := range runner.calls {
		if strings.Contains(call, windowsFirewallScript) {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("Windows firewall probes=%d, want 1 within the cache lifetime; calls=%#v", probes, runner.calls)
	}
}

func TestLinuxCapabilitiesCollectEffectiveSSHLoginSurface(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"lookpath sshd":                   {},
		"lookpath systemctl":              {},
		"sshd -T":                         {stdout: "port 2222\nlistenaddress 0.0.0.0:2222\npubkeyauthentication yes\npasswordauthentication no\npermitrootlogin no\n"},
		"systemctl is-active --quiet ssh": {},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})

	capabilities := manager.Capabilities(context.Background())
	if !capabilities.SSH.Installed || !capabilities.SSH.Running {
		t.Fatalf("SSH component = %#v", capabilities.SSH)
	}
	if capabilities.SSHPort != "2222" || capabilities.SSHLogin.PasswordAuthentication != "no" || capabilities.SSHLogin.RootLogin != "no" {
		t.Fatalf("SSH login surface = %#v port=%q", capabilities.SSHLogin, capabilities.SSHPort)
	}
	capabilities.SSHLogin.ListenAddresses[0] = "mutated"
	if cached := manager.Capabilities(context.Background()); cached.SSHLogin.ListenAddresses[0] != "0.0.0.0:2222" {
		t.Fatalf("cached SSH addresses were aliased: %#v", cached.SSHLogin.ListenAddresses)
	}
}

func TestWindowsLoginCacheAvoidsRepeatedEventLogProbes(t *testing.T) {
	now := time.Date(2026, time.August, 6, 2, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "windows", Runner: runner, Now: func() time.Time { return now }})
	query := LoginQuery{Range: "24h", Page: 1, PageSize: 20}

	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}

	probes := 0
	for _, call := range runner.calls {
		if strings.Contains(call, "Get-WinEvent") {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("Windows Event Log probes=%d, want 1 within the cache lifetime; calls=%#v", probes, runner.calls)
	}
}

func TestWindowsLoginRefreshBypassesEventLogCache(t *testing.T) {
	now := time.Date(2026, time.August, 6, 2, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "windows", Runner: runner, Now: func() time.Time { return now }})
	query := LoginQuery{Range: "24h", Page: 1, PageSize: 20}

	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	query.Refresh = true
	if _, err := manager.Logins(context.Background(), query); err != nil {
		t.Fatal(err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("Windows Event Log calls=%d, want 2 after forced refresh; calls=%#v", len(runner.calls), runner.calls)
	}
}

func TestSummarizeLoginsCountsOnlyValidFailedSourcesAsHighRisk(t *testing.T) {
	records := []LoginRecord{
		{Result: ResultSuccess, SourceIP: "203.0.113.10"},
		{Result: ResultSuccess, SourceIP: "203.0.113.10"},
		{Result: ResultSuccess, SourceIP: "203.0.113.10"},
		{Result: ResultSuccess, SourceIP: "203.0.113.10"},
		{Result: ResultSuccess, SourceIP: "203.0.113.10"},
	}
	for range 4 {
		records = append(records, LoginRecord{Result: ResultFailure, SourceIP: "198.51.100.4"})
	}
	for range 5 {
		records = append(records, LoginRecord{Result: ResultFailure, SourceIP: "198.51.100.5"})
	}
	for _, source := range []string{"", "-", "local", "localhost", "127.0.0.1", "::1"} {
		for range 5 {
			records = append(records, LoginRecord{Result: ResultFailure, SourceIP: source})
		}
	}

	stats := summarizeLogins(records)
	if stats.HighRisk != 1 {
		t.Fatalf("HighRisk=%d, want only the source with 5 failures; stats=%#v", stats.HighRisk, stats)
	}
}

func TestWindowsFirewallMutationInvalidatesCapabilitiesCache(t *testing.T) {
	now := time.Date(2026, time.August, 6, 2, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "windows", Runner: runner, Now: func() time.Time { return now }})

	manager.Capabilities(context.Background())
	if err := manager.AddWindowsFirewallRule(context.Background(), FirewallRule{
		Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "443",
		Address: "any", Name: "HTTPS", Profile: "any", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	manager.Capabilities(context.Background())

	probes := 0
	for _, call := range runner.calls {
		if strings.Contains(call, windowsFirewallScript) {
			probes++
		}
	}
	if probes != 2 {
		t.Fatalf("Windows firewall probes=%d, want 2 after a mutation; calls=%#v", probes, runner.calls)
	}
}

func TestParseLinuxLogins(t *testing.T) {
	input := `2026-08-05T14:32:01+08:00 host sshd[10]: Failed password for invalid user root from 45.148.10.72 port 5512 ssh2
2026-08-05T14:18:02+08:00 host sshd[11]: Accepted publickey for deploy from 10.20.1.16 port 4422 ssh2: ED25519 SHA256:test
2026-08-05T14:13:04+08:00 host sshd[13]: Connection closed by authenticating user root 198.51.100.9 port 23093 [preauth]
2026-08-05T14:12:03+08:00 host sshd[12]: Connection closed by invalid user scriptboard-login-test 39.71.25.221 port 23089 [preauth]
2026-08-05T14:12:03+08:00 host sshd[12]: Invalid user scriptboard-login-test from 39.71.25.221 port 23089
noise that must be ignored`
	records := parseLinuxLogins(input)
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4: %#v", len(records), records)
	}
	if records[0].Result != ResultFailure || records[0].User != "root" || records[0].SourceIP != "45.148.10.72" || records[0].Authentication != "password" {
		t.Fatalf("failure record = %#v", records[0])
	}
	if records[1].Result != ResultSuccess || records[1].User != "deploy" || records[1].Authentication != "publickey" {
		t.Fatalf("success record = %#v", records[1])
	}
	if records[2].Result != ResultFailure || records[2].User != "root" || records[2].SourceIP != "198.51.100.9" || records[2].Authentication != "preauth" {
		t.Fatalf("isolated-preauth record = %#v", records[2])
	}
	if records[3].Result != ResultFailure || records[3].User != "scriptboard-login-test" || records[3].SourceIP != "39.71.25.221" || records[3].Authentication != "preauth" || !strings.Contains(records[3].Detail, "Invalid user") {
		t.Fatalf("deduplicated invalid-user record = %#v", records[3])
	}
}

func TestParseSSHLoginSurfaceUsesEffectiveConfiguration(t *testing.T) {
	input := `port 2222
listenaddress 0.0.0.0:2222
listenaddress [::]:2222
pubkeyauthentication yes
passwordauthentication yes
kbdinteractiveauthentication no
permitrootlogin prohibit-password
permitemptypasswords no
maxauthtries 4`

	surface := parseSSHLoginSurface(input)
	if surface.Port != "2222" || !reflect.DeepEqual(surface.ListenAddresses, []string{"0.0.0.0:2222", "[::]:2222"}) {
		t.Fatalf("SSH entry = port %q addresses %#v", surface.Port, surface.ListenAddresses)
	}
	if surface.PublicKeyAuthentication != "yes" || surface.PasswordAuthentication != "yes" || surface.KeyboardInteractiveAuthentication != "no" {
		t.Fatalf("SSH authentication = %#v", surface)
	}
	if surface.RootLogin != "prohibit-password" || surface.EmptyPasswords != "no" || surface.MaxAuthTries != 4 {
		t.Fatalf("SSH restrictions = %#v", surface)
	}
}

func TestParseLinuxLoginsSupportsCommonOpenSSHFailures(t *testing.T) {
	input := `2026-08-05T14:00:00+08:00 host sshd[20]: Failed keyboard-interactive/pam for root from 203.0.113.20 port 4001 ssh2
2026-08-05T14:01:00+08:00 host sshd[21]: User deploy from 203.0.113.21 not allowed because not listed in AllowUsers
2026-08-05T14:01:01+08:00 host sshd[21]: Connection closed by invalid user deploy 203.0.113.21 port 4002 [preauth]
2026-08-05T14:02:00+08:00 host sshd[22]: error: maximum authentication attempts exceeded for root from 203.0.113.22 port 4003 ssh2 [preauth]
2026-08-05T14:02:01+08:00 host sshd[22]: Connection closed by authenticating user root 203.0.113.22 port 4003 [preauth]
2026-08-05T14:03:00+08:00 host sshd[23]: pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=203.0.113.23 user=ops`
	records := parseLinuxLogins(input)
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4 common OpenSSH failures: %#v", len(records), records)
	}
	if records[0].Authentication != "keyboard-interactive/pam" || records[0].User != "root" {
		t.Fatalf("keyboard-interactive failure = %#v", records[0])
	}
	if records[1].User != "deploy" || records[1].SourceIP != "203.0.113.21" || !strings.Contains(records[1].Detail, "not allowed") {
		t.Fatalf("disallowed-user failure = %#v", records[1])
	}
	if records[2].User != "root" || records[2].SourceIP != "203.0.113.22" || !strings.Contains(records[2].Detail, "maximum authentication attempts") {
		t.Fatalf("maximum-attempts failure = %#v", records[2])
	}
	if records[3].User != "ops" || records[3].SourceIP != "203.0.113.23" || records[3].Authentication != "pam" {
		t.Fatalf("PAM failure = %#v", records[3])
	}
}

func TestParseLinuxLoginsDoesNotMergeReusedProcessIDs(t *testing.T) {
	input := `2026-08-05T14:00:00+08:00 host sshd[30]: Invalid user first from 203.0.113.30 port 5001
2026-08-05T15:00:00+08:00 host sshd[30]: Invalid user second from 203.0.113.30 port 5002`
	records := parseLinuxLogins(input)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 after sshd PID reuse: %#v", len(records), records)
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

func TestParseUFWStatusKeepsRuleCommentAsName(t *testing.T) {
	input := `Status: active

To                         Action      From
--                         ------      ----
[ 1] 18080/tcp              ALLOW IN    Anywhere                   # ScriptBoard`
	active, rules := parseUFWStatus(input)
	if !active || len(rules) != 1 {
		t.Fatalf("active=%v rules=%#v", active, rules)
	}
	if rules[0].Name != "ScriptBoard" || rules[0].Address != "Anywhere" {
		t.Fatalf("rule = %#v, want comment as name and a clean address", rules[0])
	}
}

func TestParseUFWStatusIncludesIPv6Rules(t *testing.T) {
	input := `Status: active
[ 1] 22/tcp                    ALLOW IN    Anywhere
[ 2] 22/tcp (v6)               ALLOW IN    Anywhere (v6)`
	active, rules := parseUFWStatus(input)
	if !active || len(rules) != 2 {
		t.Fatalf("active=%v rules=%#v, want both IPv4 and IPv6 rules", active, rules)
	}
	if !rules[1].IPv6 || rules[1].Port != "22" || rules[1].Protocol != "tcp" || rules[1].Address != "Anywhere" {
		t.Fatalf("IPv6 rule = %#v", rules[1])
	}
}

func TestDiffRulesDistinguishesIPv4AndIPv6(t *testing.T) {
	baseline := []FirewallRule{
		{Number: 1, Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "Anywhere", Enabled: true},
		{Number: 2, Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "Anywhere", Enabled: true, IPv6: true},
	}
	deletes, additions := diffRules(baseline, baseline[1:])
	if !reflect.DeepEqual(deletes, []int{1}) || len(additions) != 0 {
		t.Fatalf("deletes=%v additions=%#v, want only IPv4 rule 1 deleted", deletes, additions)
	}
}

func TestDiffRulesTreatsNameChangeAsRuleReplacement(t *testing.T) {
	baseline := []FirewallRule{{Number: 1, Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "18080", Address: "Anywhere", Name: "Old name", Enabled: true}}
	desired := []FirewallRule{{Number: 1, Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "18080", Address: "Anywhere", Name: "New name", Enabled: true}}

	deletes, additions := diffRules(baseline, desired)
	if !reflect.DeepEqual(deletes, []int{1}) || len(additions) != 1 || additions[0].Name != "New name" {
		t.Fatalf("deletes=%v additions=%#v, want the renamed rule to be replaced", deletes, additions)
	}
}

func TestParseUFWDefaults(t *testing.T) {
	defaults := parseUFWDefaults("Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), deny (routed)\n")
	if defaults.Incoming != PolicyDeny || defaults.Outgoing != PolicyAllow {
		t.Fatalf("defaults = %#v, want deny incoming and allow outgoing", defaults)
	}
}

func TestParseUFWConfigDefaults(t *testing.T) {
	defaults := parseUFWConfigDefaults("DEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\n")
	if defaults.Incoming != PolicyDeny || defaults.Outgoing != PolicyAllow {
		t.Fatalf("config defaults = %#v, want deny incoming and allow outgoing", defaults)
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
		"ufw status verbose":  {stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), deny (routed)\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	baseline := []FirewallRule{{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "Anywhere", Enabled: true}}
	desired := append([]FirewallRule(nil), baseline...)
	desired = append(desired, FirewallRule{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "443", Address: "Anywhere", Enabled: true})
	defaults := UFWDefaults{Incoming: PolicyDeny, Outgoing: PolicyAllow}
	if err := manager.ApplyUFW(context.Background(), baseline, desired, defaults, defaults); err != ErrFirewallConflict {
		t.Fatalf("ApplyUFW error = %v, want %v", err, ErrFirewallConflict)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %#v, expected read only", runner.calls)
	}
}

func TestApplyUFWAddsBeforeDeleting(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: "Status: active\n[ 1] 22/tcp ALLOW IN 10.0.0.1\n[ 2] 80/tcp ALLOW IN Anywhere\n"},
		"ufw status verbose":  {stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), deny (routed)\n"},
		"lookpath sshd":       {},
		"sshd -T":             {stdout: "port 22\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	baseline := []FirewallRule{
		{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "10.0.0.1", Enabled: true},
		{Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "80", Address: "Anywhere", Enabled: true},
	}
	desired := []FirewallRule{
		baseline[0],
		{Direction: DirectionOutbound, Action: ActionAllow, Protocol: "udp", Port: "53", Address: "Anywhere", Name: "DNS egress", Enabled: true},
	}
	defaults := UFWDefaults{Incoming: PolicyDeny, Outgoing: PolicyAllow}
	if err := manager.ApplyUFW(context.Background(), baseline, desired, defaults, defaults); err != nil {
		t.Fatalf("ApplyUFW: %v", err)
	}
	want := []string{
		"ufw status numbered",
		"ufw status verbose",
		"sshd -T",
		"ufw allow out proto udp to any port 53 comment DNS egress",
		"ufw --force delete 2",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestApplyUFWPreservesApplicationProfileAndLimitRules(t *testing.T) {
	status := "Status: active\n[ 1] OpenSSH LIMIT IN Anywhere\n[ 2] 443/tcp REJECT IN Anywhere\n"
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: status},
		"ufw status verbose":  {stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), deny (routed)\n"},
		"lookpath sshd":       {},
		"sshd -T":             {stdout: "port 22\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	_, baseline := parseUFWStatus(status)
	desired := append(append([]FirewallRule(nil), baseline...), FirewallRule{
		Direction: DirectionOutbound, Action: ActionAllow, Protocol: "udp", Port: "53", Address: "Anywhere", Enabled: true,
	})
	defaults := UFWDefaults{Incoming: PolicyDeny, Outgoing: PolicyAllow}

	if err := manager.ApplyUFW(context.Background(), baseline, desired, defaults, defaults); err != nil {
		t.Fatalf("ApplyUFW with preserved application rules: %v", err)
	}
	if len(baseline) != 2 || baseline[0].Port != "OpenSSH" || baseline[0].Action != ActionLimit || baseline[1].Action != ActionReject {
		t.Fatalf("parsed application rules = %#v", baseline)
	}
	want := []string{
		"ufw status numbered",
		"ufw status verbose",
		"sshd -T",
		"ufw allow out proto udp to any port 53",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestApplyUFWUpdatesDefaultPolicies(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: "Status: inactive\n"},
		"ufw status verbose":  {stdout: "Status: inactive\nDefault: deny (incoming), allow (outgoing), deny (routed)\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	baseline := UFWDefaults{Incoming: PolicyDeny, Outgoing: PolicyAllow}
	desired := UFWDefaults{Incoming: PolicyAllow, Outgoing: PolicyDeny}
	if err := manager.ApplyUFW(context.Background(), nil, nil, baseline, desired); err != nil {
		t.Fatalf("ApplyUFW defaults: %v", err)
	}
	want := []string{"ufw status numbered", "ufw status verbose", "ufw default allow incoming", "ufw default deny outgoing"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestApplyUFWRequiresSSHRuleWhenActiveIncomingDefaultsToDeny(t *testing.T) {
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"ufw status numbered": {stdout: "Status: active\n"},
		"ufw status verbose":  {stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), deny (routed)\n"},
	}}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	defaults := UFWDefaults{Incoming: PolicyDeny, Outgoing: PolicyAllow}
	if err := manager.ApplyUFW(context.Background(), nil, nil, defaults, defaults); err != ErrSSHRuleRequired {
		t.Fatalf("ApplyUFW error = %v, want %v", err, ErrSSHRuleRequired)
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

func TestEnableUFWDoesNotTreatIPv6OnlyRuleAsIPv4Protection(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	err := manager.EnableUFW(context.Background(), []FirewallRule{{
		Direction: DirectionInbound, Action: ActionAllow, Protocol: "tcp", Port: "22", Address: "Anywhere", Enabled: true, IPv6: true,
	}})
	if err != ErrSSHRuleRequired {
		t.Fatalf("error = %v, want IPv4 SSH rule requirement", err)
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

func TestBanUsesStructuredFail2BanArguments(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	if err := manager.Ban(context.Background(), "sshd", "2001:db8::8", 3600); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	want := []string{"fail2ban-client get sshd bantime", "fail2ban-client set sshd bantime 3600", "fail2ban-client set sshd banip 2001:db8::8"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestUnbanSupportsValidatedActiveJailNames(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	if err := manager.Unban(context.Background(), "nginx-http-auth", "198.51.100.9"); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	want := []string{"fail2ban-client set nginx-http-auth unbanip 198.51.100.9"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestBanRejectsInvalidAddressBeforeExecution(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	if err := manager.Ban(context.Background(), "sshd", "203.0.113.8; reboot", 3600); err != ErrInvalidIPAddress {
		t.Fatalf("Ban error = %v, want %v", err, ErrInvalidIPAddress)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unexpected calls: %#v", runner.calls)
	}
}

func TestBanRejectsUnsupportedDurationBeforeExecution(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(Options{GOOS: "linux", Runner: runner, Now: time.Now})
	if err := manager.Ban(context.Background(), "sshd", "203.0.113.8", 61); err != ErrInvalidBanDuration {
		t.Fatalf("Ban error = %v, want %v", err, ErrInvalidBanDuration)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unexpected calls: %#v", runner.calls)
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
	foundName := false
	for _, argument := range runner.callArgs[0][1:] {
		if argument == wantName {
			foundName = true
		}
	}
	if !foundName {
		t.Fatalf("structured name argument missing from %#v", runner.callArgs[0])
	}
}

func TestWindowsFirewallTogglePassesGpoBooleanString(t *testing.T) {
	if !strings.Contains(windowsToggleFirewallScript, `-Enabled $enabled`) {
		t.Fatalf("Windows firewall toggle must pass the validated GpoBoolean string directly: %s", windowsToggleFirewallScript)
	}
	if strings.Contains(windowsToggleFirewallScript, `[bool]::Parse`) {
		t.Fatalf("Windows firewall toggle must not convert the value to System.Boolean: %s", windowsToggleFirewallScript)
	}
}

func TestWindowsFirewallProbeUsesRemotePortForOutboundRules(t *testing.T) {
	if !strings.Contains(windowsFirewallScript, `if ($_.Direction -eq 'Outbound') {$port.RemotePort} else {$port.LocalPort}`) {
		t.Fatalf("Windows firewall probe does not select RemotePort for outbound rules: %s", windowsFirewallScript)
	}
}

func TestWindowsFirewallProbeBulkLoadsFilters(t *testing.T) {
	for _, expected := range []string{
		`Security.Principal.WindowsPrincipal`,
		`Administrator=$administrator`,
		`[pscustomobject]@{Name=$_.Name;Enabled=($_.Enabled -eq 'True')}`,
		`if ([string]::IsNullOrWhiteSpace($_.Group)) {0} else {1}`,
		`$ports = @($rules | Get-NetFirewallPortFilter)`,
		`$addresses = @($rules | Get-NetFirewallAddressFilter)`,
		`$portMap[$port.InstanceID] = $port`,
		`$addressMap[$address.InstanceID] = $address`,
	} {
		if !strings.Contains(windowsFirewallScript, expected) {
			t.Fatalf("Windows firewall probe is missing bulk lookup %q", expected)
		}
	}
	for _, perRuleQuery := range []string{`$_ | Get-NetFirewallPortFilter`, `$_ | Get-NetFirewallAddressFilter`} {
		if strings.Contains(windowsFirewallScript, perRuleQuery) {
			t.Fatalf("Windows firewall probe contains slow per-rule query %q", perRuleQuery)
		}
	}
}

func TestParseWindowsFirewallIncludesAdministratorState(t *testing.T) {
	profiles, rules, administrator, known := parseWindowsFirewall(`{"Administrator":true,"Profiles":[{"Name":"Public","Enabled":true}],"Rules":[]}`)
	if !known || !administrator {
		t.Fatalf("administrator state = known %v administrator %v, want true true", known, administrator)
	}
	if len(profiles) != 1 || !profiles[0].Enabled || len(rules) != 0 {
		t.Fatalf("parsed firewall data = profiles %#v rules %#v", profiles, rules)
	}

	_, _, administrator, known = parseWindowsFirewall(`{"Administrator":false,"Profiles":[],"Rules":[]}`)
	if !known || administrator {
		t.Fatalf("standard-user state = known %v administrator %v, want true false", known, administrator)
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
