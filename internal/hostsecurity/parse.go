package hostsecurity

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	linuxLoginPattern          = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+(Accepted|Failed)\s+(\S+)(?:\s+for\s+invalid\s+user|\s+for)\s+(\S+)\s+from\s+([0-9a-fA-F:.]+)\s+port\s+(\d+)`)
	linuxInvalidUserPattern    = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+Invalid user\s+(\S+)\s+from\s+([0-9a-fA-F:.]+)\s+port\s+(\d+)`)
	linuxDisallowedUserPattern = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+User\s+(\S+)\s+from\s+([0-9a-fA-F:.]+)\s+not allowed\b`)
	linuxMaxAuthPattern        = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+(?:error:\s*)?maximum authentication attempts exceeded for(?: invalid user)?\s+(\S+)\s+from\s+([0-9a-fA-F:.]+)\s+port\s+(\d+)`)
	linuxPAMFailurePattern     = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+pam_unix\(sshd:auth\): authentication failure;.*\brhost=([0-9a-fA-F:.]+)\s+user=(\S+)`)
	linuxPreAuthPattern        = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[(\d+)\]:\s+(?:Connection (?:closed|reset) by|Disconnected from) (?:invalid|authenticating) user\s+(\S+)\s+([0-9a-fA-F:.]+)\s+port\s+(\d+)\s+\[preauth\]`)
	ufwRulePattern             = regexp.MustCompile(`^\[\s*(\d+)\]\s+(\S+)(\s+\(v6\))?\s+(ALLOW|DENY)(?:\s+(IN|OUT))?\s+(.+?)\s*$`)
	ufwDefaultsPattern         = regexp.MustCompile(`(?i)^Default:\s*(allow|deny)\s*\(incoming\),\s*(allow|deny)\s*\(outgoing\)`)
	fail2BanEventPattern       = regexp.MustCompile(`^(\S+)\s+.*?fail2ban\.actions\s+\[.*?\]:\s+NOTICE\s+\[(\S+)\]\s+Ban\s+([0-9a-fA-F:.]+)\s*$`)
)

const linuxLoginSessionWindow = 10 * time.Minute

type linuxLoginEvent struct {
	record    LoginRecord
	processID string
	port      string
	priority  int
}

func parseSSHLoginSurface(output string) SSHLoginSurface {
	surface := SSHLoginSurface{}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.Join(fields[1:], " ")
		switch key {
		case "port":
			if validPort(value) {
				surface.Port = value
			}
		case "listenaddress":
			if value != "" {
				surface.ListenAddresses = append(surface.ListenAddresses, value)
			}
		case "pubkeyauthentication":
			surface.PublicKeyAuthentication = value
		case "passwordauthentication":
			surface.PasswordAuthentication = value
		case "kbdinteractiveauthentication", "challengeresponseauthentication":
			if surface.KeyboardInteractiveAuthentication == "" || key == "kbdinteractiveauthentication" {
				surface.KeyboardInteractiveAuthentication = value
			}
		case "permitrootlogin":
			surface.RootLogin = value
		case "permitemptypasswords":
			surface.EmptyPasswords = value
		case "maxauthtries":
			if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
				surface.MaxAuthTries = parsed
			}
		}
	}
	return surface
}

func parseLinuxLogins(output string) []LoginRecord {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	records := make([]LoginRecord, 0, len(lines))
	type sessionRecord struct {
		index    int
		priority int
		port     string
		time     time.Time
		sourceIP string
	}
	sessions := make(map[string]sessionRecord)
	appendRecord := func(event linuxLoginEvent) {
		previous, found := sessions[event.processID]
		sameSession := found && previous.sourceIP == event.record.SourceIP && absDuration(previous.time.Sub(event.record.Time)) <= linuxLoginSessionWindow
		if sameSession && previous.port != "" && event.port != "" && previous.port != event.port {
			sameSession = false
		}
		if sameSession {
			if event.priority > previous.priority {
				port := event.port
				if port == "" {
					port = previous.port
				}
				records[previous.index] = event.record
				sessions[event.processID] = sessionRecord{index: previous.index, priority: event.priority, port: port, time: event.record.Time, sourceIP: event.record.SourceIP}
				return
			}
			if event.priority < 3 {
				if previous.port == "" && event.port != "" {
					previous.port = event.port
					sessions[event.processID] = previous
				}
				return
			}
		}
		records = append(records, event.record)
		sessions[event.processID] = sessionRecord{index: len(records) - 1, priority: event.priority, port: event.port, time: event.record.Time, sourceIP: event.record.SourceIP}
	}
	for _, line := range lines {
		detail := strings.TrimSpace(line)
		if event, ok := parseLinuxLoginEvent(detail); ok {
			appendRecord(event)
		}
	}
	return records
}

func parseLinuxLoginEvent(detail string) (linuxLoginEvent, bool) {
	if match := linuxLoginPattern.FindStringSubmatch(detail); len(match) != 0 {
		result := ResultFailure
		if match[3] == "Accepted" {
			result = ResultSuccess
		}
		return newLinuxLoginEvent(match[1], match[2], match[7], match[5], match[6], match[4], detail, result, 3)
	}
	if match := linuxInvalidUserPattern.FindStringSubmatch(detail); len(match) != 0 {
		return newLinuxLoginEvent(match[1], match[2], match[5], match[3], match[4], "preauth", detail, ResultFailure, 2)
	}
	if match := linuxDisallowedUserPattern.FindStringSubmatch(detail); len(match) != 0 {
		return newLinuxLoginEvent(match[1], match[2], "", match[3], match[4], "preauth", detail, ResultFailure, 2)
	}
	if match := linuxMaxAuthPattern.FindStringSubmatch(detail); len(match) != 0 {
		return newLinuxLoginEvent(match[1], match[2], match[5], match[3], match[4], "preauth", detail, ResultFailure, 2)
	}
	if match := linuxPAMFailurePattern.FindStringSubmatch(detail); len(match) != 0 {
		return newLinuxLoginEvent(match[1], match[2], "", match[4], match[3], "pam", detail, ResultFailure, 2)
	}
	if match := linuxPreAuthPattern.FindStringSubmatch(detail); len(match) != 0 {
		return newLinuxLoginEvent(match[1], match[2], match[5], match[3], match[4], "preauth", detail, ResultFailure, 1)
	}
	return linuxLoginEvent{}, false
}

func newLinuxLoginEvent(timestamp, processID, port, user, sourceIP, authentication, detail string, result LoginResult, priority int) (linuxLoginEvent, bool) {
	stamp, err := parseLinuxJournalTimestamp(timestamp)
	if err != nil {
		return linuxLoginEvent{}, false
	}
	return linuxLoginEvent{
		record:    LoginRecord{Time: stamp.UTC(), Result: result, User: user, SourceIP: sourceIP, Type: "ssh", Authentication: authentication, Detail: detail},
		processID: processID, port: port, priority: priority,
	}, true
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func parseLinuxJournalTimestamp(value string) (time.Time, error) {
	stamp, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return stamp, nil
	}
	return time.Parse("2006-01-02T15:04:05-0700", value)
}

func parseUFWStatus(output string) (bool, []FirewallRule) {
	active := false
	var rules []FirewallRule
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "status:") {
			active = strings.Contains(strings.ToLower(trimmed), "active") && !strings.Contains(strings.ToLower(trimmed), "inactive")
			continue
		}
		match := ufwRulePattern.FindStringSubmatch(trimmed)
		if len(match) == 0 {
			continue
		}
		number, _ := strconv.Atoi(match[1])
		port, protocol := splitUFWTarget(match[2])
		ipv6 := match[3] != ""
		direction := DirectionInbound
		address := strings.TrimSpace(match[6])
		if strings.HasSuffix(strings.ToLower(address), "(v6)") {
			ipv6 = true
			address = strings.TrimSpace(address[:len(address)-len("(v6)")])
		}
		if match[5] == "OUT" || strings.HasSuffix(strings.ToLower(address), "(out)") {
			direction = DirectionOutbound
			if strings.HasSuffix(strings.ToLower(address), "(out)") {
				address = strings.TrimSpace(address[:len(address)-len("(out)")])
			}
		}
		action := ActionAllow
		if match[4] == "DENY" {
			action = ActionDeny
		}
		if address == "" {
			address = "Anywhere"
		}
		rules = append(rules, FirewallRule{
			Number: number, Direction: direction, Action: action, Protocol: protocol,
			Port: port, Address: address, Name: fmt.Sprintf("UFW #%d", number), Enabled: true, IPv6: ipv6,
		})
	}
	return active, rules
}

func parseUFWDefaults(output string) UFWDefaults {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		match := ufwDefaultsPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 3 {
			continue
		}
		return UFWDefaults{Incoming: FirewallPolicy(strings.ToLower(match[1])), Outgoing: FirewallPolicy(strings.ToLower(match[2]))}
	}
	return UFWDefaults{}
}

func parseUFWConfigDefaults(output string) UFWDefaults {
	defaults := UFWDefaults{}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.ToUpper(strings.Trim(strings.TrimSpace(parts[1]), `"'`))
		policy := FirewallPolicy("")
		switch value {
		case "ACCEPT":
			policy = PolicyAllow
		case "DROP", "REJECT":
			policy = PolicyDeny
		}
		switch parts[0] {
		case "DEFAULT_INPUT_POLICY":
			defaults.Incoming = policy
		case "DEFAULT_OUTPUT_POLICY":
			defaults.Outgoing = policy
		}
	}
	return defaults
}

func splitUFWTarget(value string) (string, string) {
	parts := strings.SplitN(value, "/", 2)
	port := strings.TrimSpace(parts[0])
	protocol := "any"
	if len(parts) == 2 {
		protocol = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return port, protocol
}

func parseFail2BanStatus(output, jail string) []Ban {
	var ips []string
	for _, line := range strings.Split(output, "\n") {
		if index := strings.Index(line, "Banned IP list:"); index >= 0 {
			ips = strings.Fields(line[index+len("Banned IP list:"):])
			break
		}
	}
	bans := make([]Ban, 0, len(ips))
	for _, ip := range ips {
		bans = append(bans, Ban{IP: ip, Jail: jail})
	}
	return bans
}

func parseFail2BanEvents(output, jail string) map[string]time.Time {
	result := make(map[string]time.Time)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		match := fail2BanEventPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 0 || match[2] != jail {
			continue
		}
		stamp, err := time.Parse(time.RFC3339, match[1])
		if err != nil {
			stamp, err = time.Parse("2006-01-02T15:04:05-0700", match[1])
		}
		if err == nil {
			if previous := result[match[3]]; previous.IsZero() || stamp.After(previous) {
				result[match[3]] = stamp.UTC()
			}
		}
	}
	return result
}

type windowsLoginJSON struct {
	Time, EventID, User, IP, LogonType, Status, SubStatus string
}

func parseWindowsLogins(output string) ([]LoginRecord, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var values []windowsLoginJSON
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, fmt.Errorf("decode Windows login events: %w", err)
		}
	} else {
		var value windowsLoginJSON
		if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
			return nil, fmt.Errorf("decode Windows login event: %w", err)
		}
		values = append(values, value)
	}
	records := make([]LoginRecord, 0, len(values))
	for _, value := range values {
		stamp, err := time.Parse(time.RFC3339Nano, value.Time)
		if err != nil {
			continue
		}
		result := ResultFailure
		if value.EventID == "4624" {
			result = ResultSuccess
		}
		loginType := "network"
		if value.LogonType == "10" {
			loginType = "rdp"
		}
		if value.EventID == "4740" {
			loginType = "lockout"
		}
		detail := strings.TrimSpace(strings.Join([]string{value.Status, value.SubStatus}, " "))
		records = append(records, LoginRecord{
			Time: stamp.UTC(), Result: result, User: value.User, SourceIP: normalizeWindowsIP(value.IP),
			Type: loginType, Authentication: "windows", EventID: value.EventID, Detail: detail,
		})
	}
	return records, nil
}

func normalizeWindowsIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return "local"
	}
	return value
}

type windowsFirewallJSON struct {
	Administrator *bool
	Profiles      []struct {
		Name    string
		Enabled bool
	}
	Rules []struct {
		ID, Name, Direction, Action, Profile string
		Protocol                             any
		Port                                 any
		Address                              any
		Enabled                              bool
	}
}

func parseWindowsFirewall(output string) ([]FirewallProfile, []FirewallRule, bool, bool) {
	var value windowsFirewallJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &value); err != nil {
		return nil, nil, false, false
	}
	profiles := make([]FirewallProfile, 0, len(value.Profiles))
	for _, item := range value.Profiles {
		profiles = append(profiles, FirewallProfile{Name: item.Name, Enabled: item.Enabled})
	}
	rules := make([]FirewallRule, 0, len(value.Rules))
	for _, item := range value.Rules {
		direction := DirectionInbound
		if strings.EqualFold(item.Direction, "outbound") {
			direction = DirectionOutbound
		}
		action := ActionAllow
		if strings.EqualFold(item.Action, "block") {
			action = ActionDeny
		}
		rules = append(rules, FirewallRule{
			ID:        item.ID,
			Direction: direction, Action: action, Protocol: stringifyJSONValue(item.Protocol),
			Port: stringifyJSONValue(item.Port), Address: stringifyJSONValue(item.Address),
			Name: item.Name, Profile: item.Profile, Enabled: item.Enabled,
		})
	}
	administrator, administratorKnown := false, value.Administrator != nil
	if administratorKnown {
		administrator = *value.Administrator
	}
	return profiles, rules, administrator, administratorKnown
}

func stringifyJSONValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "Any"
	case string:
		return typed
	case float64:
		return strconv.Itoa(int(typed))
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, stringifyJSONValue(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(typed)
	}
}
