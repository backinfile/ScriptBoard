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
	linuxLoginPattern    = regexp.MustCompile(`^(\S+)\s+.*?sshd?\[\d+\]:\s+(Accepted|Failed)\s+(password|publickey|keyboard-interactive)(?:\s+for\s+invalid\s+user|\s+for)\s+(\S+)\s+from\s+([0-9a-fA-F:.]+)\s+port\s+\d+`)
	ufwRulePattern       = regexp.MustCompile(`^\[\s*(\d+)\]\s+(\S+)\s+(ALLOW|DENY)(?:\s+(IN|OUT))?\s+(.+?)\s*$`)
	fail2BanEventPattern = regexp.MustCompile(`^(\S+)\s+.*?fail2ban\.actions\s+\[.*?\]:\s+NOTICE\s+\[(\S+)\]\s+Ban\s+([0-9a-fA-F:.]+)\s*$`)
)

func parseLinuxLogins(output string) []LoginRecord {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	records := make([]LoginRecord, 0, len(lines))
	for _, line := range lines {
		match := linuxLoginPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 0 {
			continue
		}
		stamp, err := time.Parse(time.RFC3339, match[1])
		if err != nil {
			stamp, err = time.Parse("2006-01-02T15:04:05-0700", match[1])
		}
		if err != nil {
			continue
		}
		result := ResultFailure
		if match[2] == "Accepted" {
			result = ResultSuccess
		}
		records = append(records, LoginRecord{
			Time: stamp.UTC(), Result: result, User: match[4], SourceIP: match[5], Type: "ssh",
			Authentication: match[3], Detail: strings.TrimSpace(line),
		})
	}
	return records
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
		direction := DirectionInbound
		address := strings.TrimSpace(match[5])
		if match[4] == "OUT" || strings.HasSuffix(strings.ToLower(address), "(out)") {
			direction = DirectionOutbound
			address = strings.TrimSpace(address[:len(address)-len("(out)")])
		}
		action := ActionAllow
		if match[3] == "DENY" {
			action = ActionDeny
		}
		if address == "" {
			address = "Anywhere"
		}
		rules = append(rules, FirewallRule{
			Number: number, Direction: direction, Action: action, Protocol: protocol,
			Port: port, Address: address, Name: fmt.Sprintf("UFW #%d", number), Enabled: true,
		})
	}
	return active, rules
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
	Profiles []struct {
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

func parseWindowsFirewall(output string) ([]FirewallProfile, []FirewallRule) {
	var value windowsFirewallJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &value); err != nil {
		return nil, nil
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
	return profiles, rules
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
