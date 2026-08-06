package hostsecurity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LoginResult string

const (
	ResultSuccess LoginResult = "success"
	ResultFailure LoginResult = "failure"
)

type Direction string

const (
	DirectionInbound  Direction = "in"
	DirectionOutbound Direction = "out"
)

type FirewallAction string

const (
	ActionAllow FirewallAction = "allow"
	ActionDeny  FirewallAction = "deny"
)

type Component struct {
	Installed bool
	Running   bool
	Error     string
}

type Capabilities struct {
	OS                 string
	Hostname           string
	CollectedAt        time.Time
	Administrator      bool
	AdministratorKnown bool
	Fail2Ban           Component
	UFW                Component
	Firewall           Component
	UFWEnabled         bool
	SSHPort            string
	Rules              []FirewallRule
	Profiles           []FirewallProfile
}

type LoginRecord struct {
	Time           time.Time
	Result         LoginResult
	User           string
	SourceIP       string
	Type           string
	Authentication string
	EventID        string
	Detail         string
}

type LoginQuery struct {
	Range    string
	Start    time.Time
	End      time.Time
	Result   LoginResult
	Type     string
	Page     int
	PageSize int
	Refresh  bool
}

type LoginPage struct {
	Records     []LoginRecord
	Total       int
	Page        int
	Pages       int
	Stats       LoginStats
	CollectedAt time.Time
	Cached      bool
}

type LoginStats struct {
	Success       int
	Failure       int
	UniqueSources int
	HighRisk      int
	RDP           int
	Locked        int
}

type Ban struct {
	IP        string
	Jail      string
	BannedAt  time.Time
	Duration  time.Duration
	Remaining time.Duration
}

type BanPage struct {
	Bans  []Ban
	Total int
	Page  int
	Pages int
}

type FirewallRule struct {
	ID        string
	Number    int
	Direction Direction
	Action    FirewallAction
	Protocol  string
	Port      string
	Address   string
	Name      string
	Profile   string
	Enabled   bool
}

type FirewallProfile struct {
	Name    string
	Enabled bool
}

var (
	ErrUnsupported      = errors.New("host security operation is not supported on this platform")
	ErrInvalidComponent = errors.New("invalid security component")
	ErrInvalidRule      = errors.New("invalid firewall rule")
	ErrFirewallConflict = errors.New("firewall rules changed since the draft was created")
	ErrSSHRuleRequired  = errors.New("an enabled inbound SSH allow rule is required")
	ErrInvalidIPAddress = errors.New("invalid IP address")
	ErrComponentMissing = errors.New("security component is not installed")
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
	LookPath(string) bool
}

type Options struct {
	GOOS               string
	Runner             Runner
	Now                func() time.Time
	CapabilityCacheTTL time.Duration
	LoginCacheTTL      time.Duration
}

type loginCacheEntry struct {
	records     []LoginRecord
	collectedAt time.Time
}

type Service interface {
	Capabilities(context.Context) Capabilities
	Logins(context.Context, LoginQuery) (LoginPage, error)
	Bans(context.Context, int, int) (BanPage, error)
	Install(context.Context, string) error
	Unban(context.Context, string, string) error
	EnableUFW(context.Context, []FirewallRule) error
	ApplyUFW(context.Context, []FirewallRule, []FirewallRule) error
	AddWindowsFirewallRule(context.Context, FirewallRule) error
	SetWindowsFirewallRuleEnabled(context.Context, string, bool) error
	DeleteWindowsFirewallRule(context.Context, string) error
}

type Manager struct {
	goos                 string
	runner               Runner
	now                  func() time.Time
	capabilityCacheTTL   time.Duration
	capabilityCache      Capabilities
	capabilityCacheValid bool
	loginCacheTTL        time.Duration
	loginCache           map[string]loginCacheEntry
	mu                   sync.Mutex
	loginMu              sync.Mutex
}

func NewManager(options Options) *Manager {
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	runner := options.Runner
	if runner == nil {
		runner = commandRunner{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	cacheTTL := options.CapabilityCacheTTL
	if cacheTTL == 0 {
		cacheTTL = 30 * time.Second
	}
	loginCacheTTL := options.LoginCacheTTL
	if loginCacheTTL == 0 {
		loginCacheTTL = 30 * time.Second
	}
	return &Manager{
		goos: goos, runner: runner, now: now, capabilityCacheTTL: cacheTTL,
		loginCacheTTL: loginCacheTTL, loginCache: make(map[string]loginCacheEntry),
	}
}

func (m *Manager) Capabilities(ctx context.Context) Capabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	if m.capabilityCacheValid && m.capabilityCacheTTL > 0 && now.Sub(m.capabilityCache.CollectedAt) >= 0 && now.Sub(m.capabilityCache.CollectedAt) < m.capabilityCacheTTL {
		return cloneCapabilities(m.capabilityCache)
	}
	view := m.collectCapabilities(ctx, now)
	m.capabilityCache = cloneCapabilities(view)
	m.capabilityCacheValid = true
	return cloneCapabilities(view)
}

func (m *Manager) collectCapabilities(ctx context.Context, now time.Time) Capabilities {
	view := Capabilities{OS: m.goos, CollectedAt: now}
	if name, err := os.Hostname(); err == nil {
		view.Hostname = strings.TrimSpace(name)
	}
	if m.goos == "windows" {
		view.Firewall = Component{Installed: true}
		output, err := m.runner.Run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsFirewallScript)
		if err != nil {
			view.Firewall.Error = conciseError(err)
			return view
		}
		view.Profiles, view.Rules, view.Administrator, view.AdministratorKnown = parseWindowsFirewall(output)
		for _, profile := range view.Profiles {
			view.Firewall.Running = view.Firewall.Running || profile.Enabled
		}
		return view
	}
	if m.goos != "linux" {
		return view
	}
	if current, err := user.Current(); err == nil {
		view.AdministratorKnown = true
		view.Administrator = current.Uid == "0"
	}
	view.SSHPort = m.sshPort(ctx)
	view.Fail2Ban.Installed = m.runner.LookPath("fail2ban-client")
	view.UFW.Installed = m.runner.LookPath("ufw")
	view.Firewall = view.UFW
	if view.Fail2Ban.Installed {
		_, err := m.runner.Run(ctx, "systemctl", "is-active", "--quiet", "fail2ban")
		view.Fail2Ban.Running = err == nil
	}
	if view.UFW.Installed {
		output, err := m.runner.Run(ctx, "ufw", "status", "numbered")
		if err != nil {
			view.UFW.Error = conciseError(err)
			view.Firewall = view.UFW
			return view
		}
		view.UFWEnabled, view.Rules = parseUFWStatus(output)
		view.UFW.Running = view.UFWEnabled
		view.Firewall = view.UFW
	}
	return view
}

func cloneCapabilities(value Capabilities) Capabilities {
	value.Rules = append([]FirewallRule(nil), value.Rules...)
	value.Profiles = append([]FirewallProfile(nil), value.Profiles...)
	return value
}

func (m *Manager) invalidateCapabilitiesLocked() {
	m.capabilityCacheValid = false
}

func (m *Manager) Logins(ctx context.Context, query LoginQuery) (LoginPage, error) {
	query = normalizeLoginQuery(query)
	rangeStart := m.now().Add(-rangeDuration(query.Range))
	loadQuery := query
	if loadQuery.Start.IsZero() || loadQuery.Start.After(rangeStart) {
		loadQuery.Start = rangeStart
	}
	var records []LoginRecord
	collectedAt := m.now().UTC()
	cached := false
	var err error
	if m.goos == "windows" {
		records, collectedAt, cached, err = m.cachedWindowsLogins(ctx, query, loadQuery)
	} else if m.goos == "linux" {
		records, err = m.linuxLogins(ctx, loadQuery)
	} else {
		return LoginPage{}, ErrUnsupported
	}
	if err != nil {
		return LoginPage{}, err
	}
	stats := summarizeLogins(filterLogins(records, LoginQuery{Start: rangeStart}))
	filtered := filterLogins(records, query)
	pages := max(1, (len(filtered)+query.PageSize-1)/query.PageSize)
	query.Page = min(max(1, query.Page), pages)
	start := min((query.Page-1)*query.PageSize, len(filtered))
	end := min(start+query.PageSize, len(filtered))
	return LoginPage{
		Records: filtered[start:end], Total: len(filtered), Page: query.Page, Pages: pages, Stats: stats,
		CollectedAt: collectedAt, Cached: cached,
	}, nil
}

func (m *Manager) cachedWindowsLogins(ctx context.Context, query, loadQuery LoginQuery) ([]LoginRecord, time.Time, bool, error) {
	key := windowsLoginCacheKey(query)
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	now := m.now().UTC()
	if entry, ok := m.loginCache[key]; ok && !query.Refresh && m.loginCacheTTL > 0 && now.Sub(entry.collectedAt) >= 0 && now.Sub(entry.collectedAt) < m.loginCacheTTL {
		return append([]LoginRecord(nil), entry.records...), entry.collectedAt, true, nil
	}
	records, err := m.windowsLogins(ctx, loadQuery)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	collectedAt := m.now().UTC()
	if m.loginCacheTTL > 0 {
		m.loginCache[key] = loginCacheEntry{records: append([]LoginRecord(nil), records...), collectedAt: collectedAt}
	}
	return records, collectedAt, false, nil
}

func windowsLoginCacheKey(query LoginQuery) string {
	return strings.Join([]string{
		query.Range,
		query.Start.UTC().Format(time.RFC3339Nano),
		query.End.UTC().Format(time.RFC3339Nano),
	}, "|")
}

func (m *Manager) linuxLogins(ctx context.Context, query LoginQuery) ([]LoginRecord, error) {
	since := query.Start
	if since.IsZero() {
		since = m.now().Add(-rangeDuration(query.Range))
	}
	output, err := m.runner.Run(ctx, "journalctl", "-u", "ssh", "-u", "sshd", "--since", since.Format(time.RFC3339), "--no-pager", "-o", "short-iso", "--reverse")
	if err != nil {
		return nil, fmt.Errorf("read SSH journal: %w", err)
	}
	return parseLinuxLogins(output), nil
}

func (m *Manager) windowsLogins(ctx context.Context, query LoginQuery) ([]LoginRecord, error) {
	since := query.Start
	if since.IsZero() {
		since = m.now().Add(-rangeDuration(query.Range))
	}
	script := strings.ReplaceAll(windowsLoginScript, "__SINCE__", since.UTC().Format(time.RFC3339))
	output, err := m.runner.Run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return nil, fmt.Errorf("read Windows Security Event Log: %w", err)
	}
	return parseWindowsLogins(output)
}

func normalizeLoginQuery(query LoginQuery) LoginQuery {
	if query.Range != "7d" && query.Range != "30d" {
		query.Range = "24h"
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize != 50 && query.PageSize != 100 {
		query.PageSize = 20
	}
	return query
}

func rangeDuration(value string) time.Duration {
	switch value {
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func filterLogins(records []LoginRecord, query LoginQuery) []LoginRecord {
	result := make([]LoginRecord, 0, len(records))
	for _, record := range records {
		if !query.Start.IsZero() && record.Time.Before(query.Start) || !query.End.IsZero() && !record.Time.Before(query.End) {
			continue
		}
		if query.Result != "" && record.Result != query.Result {
			continue
		}
		if query.Type != "" && !strings.EqualFold(record.Type, query.Type) && !strings.EqualFold(record.Authentication, query.Type) {
			continue
		}
		result = append(result, record)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time.After(result[j].Time) })
	return result
}

func summarizeLogins(records []LoginRecord) LoginStats {
	stats := LoginStats{}
	uniqueSources := make(map[string]struct{})
	failedSources := make(map[string]int)
	for _, record := range records {
		if source, ok := validLoginSource(record.SourceIP); ok {
			uniqueSources[source] = struct{}{}
			if record.Result == ResultFailure {
				failedSources[source]++
			}
		}
		switch record.Result {
		case ResultSuccess:
			stats.Success++
		case ResultFailure:
			stats.Failure++
		}
		if strings.EqualFold(record.Type, "rdp") {
			stats.RDP++
		}
		if record.EventID == "4740" {
			stats.Locked++
		}
	}
	stats.UniqueSources = len(uniqueSources)
	for _, count := range failedSources {
		if count >= 5 {
			stats.HighRisk++
		}
	}
	return stats
}

func validLoginSource(value string) (string, bool) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "-", "local", "localhost", "0.0.0.0", "::", "127.0.0.1", "::1":
		return "", false
	default:
		return value, true
	}
}

func (m *Manager) Bans(ctx context.Context, page, pageSize int) (BanPage, error) {
	if m.goos != "linux" {
		return BanPage{}, ErrUnsupported
	}
	if !m.runner.LookPath("fail2ban-client") {
		return BanPage{}, ErrComponentMissing
	}
	output, err := m.runner.Run(ctx, "fail2ban-client", "status", "sshd")
	if err != nil {
		return BanPage{}, fmt.Errorf("read Fail2Ban status: %w", err)
	}
	bans := parseFail2BanStatus(output, "sshd")
	var duration time.Duration
	if value, durationErr := m.runner.Run(ctx, "fail2ban-client", "get", "sshd", "bantime"); durationErr == nil {
		if seconds, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64); parseErr == nil && seconds > 0 {
			duration = time.Duration(seconds) * time.Second
		}
	}
	if events, eventsErr := m.runner.Run(ctx, "journalctl", "-u", "fail2ban", "--since", "-30 days", "--no-pager", "-o", "short-iso", "--reverse"); eventsErr == nil {
		started := parseFail2BanEvents(events, "sshd")
		now := m.now().UTC()
		for index := range bans {
			bans[index].Duration = duration
			bans[index].BannedAt = started[bans[index].IP]
			if duration > 0 && !bans[index].BannedAt.IsZero() {
				bans[index].Remaining = max(time.Duration(0), duration-now.Sub(bans[index].BannedAt))
			}
		}
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	pages := max(1, (len(bans)+pageSize-1)/pageSize)
	page = min(max(1, page), pages)
	start := min((page-1)*pageSize, len(bans))
	end := min(start+pageSize, len(bans))
	return BanPage{Bans: bans[start:end], Total: len(bans), Page: page, Pages: pages}, nil
}

func (m *Manager) Install(ctx context.Context, component string) error {
	if m.goos != "linux" {
		return ErrUnsupported
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch component {
	case "fail2ban", "ufw":
	default:
		return ErrInvalidComponent
	}
	if _, err := m.runner.Run(ctx, "apt-get", "update"); err != nil {
		return fmt.Errorf("update package index: %w", err)
	}
	if _, err := m.runner.Run(ctx, "apt-get", "install", "-y", component); err != nil {
		return fmt.Errorf("install %s: %w", component, err)
	}
	if component == "fail2ban" {
		if _, err := m.runner.Run(ctx, "systemctl", "enable", "--now", "fail2ban"); err != nil {
			return fmt.Errorf("start fail2ban: %w", err)
		}
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func (m *Manager) Unban(ctx context.Context, jail, ip string) error {
	if m.goos != "linux" {
		return ErrUnsupported
	}
	if jail != "sshd" || net.ParseIP(ip) == nil {
		return ErrInvalidIPAddress
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "fail2ban-client", "set", jail, "unbanip", ip); err != nil {
		return fmt.Errorf("unban IP: %w", err)
	}
	return nil
}

func (m *Manager) EnableUFW(ctx context.Context, rules []FirewallRule) error {
	if m.goos != "linux" {
		return ErrUnsupported
	}
	if !hasSSHAllowRule(rules, m.sshPort(ctx)) {
		return ErrSSHRuleRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "ufw", "--force", "enable"); err != nil {
		return fmt.Errorf("enable UFW: %w", err)
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func hasSSHAllowRule(rules []FirewallRule, port string) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.Direction == DirectionInbound && rule.Action == ActionAllow && (rule.Protocol == "tcp" || rule.Protocol == "any") && rule.Port == port {
			return true
		}
	}
	return false
}

func (m *Manager) sshPort(ctx context.Context) string {
	const fallback = "22"
	if !m.runner.LookPath("sshd") {
		return fallback
	}
	output, err := m.runner.Run(ctx, "sshd", "-T")
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "port" && validPort(fields[1]) {
			return fields[1]
		}
	}
	return fallback
}

func (m *Manager) ApplyUFW(ctx context.Context, baseline, desired []FirewallRule) error {
	if m.goos != "linux" {
		return ErrUnsupported
	}
	for _, rule := range desired {
		if err := validateRule(rule); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	output, err := m.runner.Run(ctx, "ufw", "status", "numbered")
	if err != nil {
		return fmt.Errorf("read UFW rules: %w", err)
	}
	_, current := parseUFWStatus(output)
	if !sameRuleSet(current, baseline) {
		return ErrFirewallConflict
	}
	deletes, additions := diffRules(baseline, desired)
	sort.Sort(sort.Reverse(sort.IntSlice(deletes)))
	for _, number := range deletes {
		if _, err := m.runner.Run(ctx, "ufw", "--force", "delete", strconv.Itoa(number)); err != nil {
			return fmt.Errorf("delete UFW rule %d: %w", number, err)
		}
	}
	for _, rule := range additions {
		if _, err := m.runner.Run(ctx, "ufw", ufwAddArguments(rule)...); err != nil {
			return fmt.Errorf("add UFW rule %s: %w", rule.Name, err)
		}
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func (m *Manager) AddWindowsFirewallRule(ctx context.Context, rule FirewallRule) error {
	if m.goos != "windows" {
		return ErrUnsupported
	}
	if err := validateRule(rule); err != nil || !validRuleName(rule.Name) {
		return ErrInvalidRule
	}
	direction := "in"
	portKey := "localport="
	if rule.Direction == DirectionOutbound {
		direction = "out"
		portKey = "remoteport="
	}
	action := "allow"
	if rule.Action == ActionDeny {
		action = "block"
	}
	protocol := strings.ToUpper(rule.Protocol)
	if protocol == "ANY" {
		protocol = "any"
	}
	address := rule.Address
	if strings.EqualFold(address, "anywhere") {
		address = "any"
	}
	profile := strings.ToLower(strings.TrimSpace(rule.Profile))
	if profile == "" {
		profile = "any"
	}
	if profile != "any" && profile != "domain" && profile != "private" && profile != "public" {
		return ErrInvalidRule
	}
	args := []string{"advfirewall", "firewall", "add", "rule", "name=" + rule.Name, "dir=" + direction, "action=" + action, "protocol=" + protocol, portKey + rule.Port, "remoteip=" + address, "profile=" + profile, "enable=yes"}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "netsh.exe", args...); err != nil {
		return fmt.Errorf("add Windows firewall rule: %w", err)
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func (m *Manager) SetWindowsFirewallRuleEnabled(ctx context.Context, id string, enabled bool) error {
	if m.goos != "windows" {
		return ErrUnsupported
	}
	if !validWindowsRuleID(id) {
		return ErrInvalidRule
	}
	value := "False"
	if enabled {
		value = "True"
	}
	encodedID := base64.StdEncoding.EncodeToString([]byte(id))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsToggleFirewallScript, encodedID, value); err != nil {
		return fmt.Errorf("update Windows firewall rule: %w", err)
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func (m *Manager) DeleteWindowsFirewallRule(ctx context.Context, id string) error {
	if m.goos != "windows" {
		return ErrUnsupported
	}
	if !validWindowsRuleID(id) {
		return ErrInvalidRule
	}
	encodedID := base64.StdEncoding.EncodeToString([]byte(id))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsDeleteFirewallScript, encodedID); err != nil {
		return fmt.Errorf("delete Windows firewall rule: %w", err)
	}
	m.invalidateCapabilitiesLocked()
	return nil
}

func validRuleName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 120 {
		return false
	}
	return !strings.ContainsAny(value, "\r\n\x00")
}

func validWindowsRuleID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}

func validateRule(rule FirewallRule) error {
	if rule.Direction != DirectionInbound && rule.Direction != DirectionOutbound || rule.Action != ActionAllow && rule.Action != ActionDeny {
		return ErrInvalidRule
	}
	protocol := strings.ToLower(rule.Protocol)
	if protocol != "tcp" && protocol != "udp" && protocol != "any" {
		return ErrInvalidRule
	}
	if !validPort(rule.Port) || !validAddress(rule.Address) || len(rule.Name) > 120 {
		return ErrInvalidRule
	}
	return nil
}

// ValidateFirewallRule validates the structured fields accepted by the web
// surface before a rule is added to a UFW draft.
func ValidateFirewallRule(rule FirewallRule) error {
	return validateRule(rule)
}

func validPort(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}

func validAddress(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "anywhere") || strings.EqualFold(value, "any") {
		return true
	}
	if net.ParseIP(value) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(value)
	return err == nil
}

func sameRuleSet(left, right []FirewallRule) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, rule := range left {
		counts[ruleKey(rule)]++
	}
	for _, rule := range right {
		key := ruleKey(rule)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}

func diffRules(baseline, desired []FirewallRule) ([]int, []FirewallRule) {
	desiredCounts := make(map[string]int)
	for _, rule := range desired {
		if rule.Enabled {
			desiredCounts[ruleKey(rule)]++
		}
	}
	var deletes []int
	for index, rule := range baseline {
		key := ruleKey(rule)
		if desiredCounts[key] > 0 {
			desiredCounts[key]--
		} else {
			number := rule.Number
			if number == 0 {
				number = index + 1
			}
			deletes = append(deletes, number)
		}
	}
	baselineCounts := make(map[string]int)
	for _, rule := range baseline {
		baselineCounts[ruleKey(rule)]++
	}
	var additions []FirewallRule
	for _, rule := range desired {
		if !rule.Enabled {
			continue
		}
		key := ruleKey(rule)
		if baselineCounts[key] > 0 {
			baselineCounts[key]--
		} else {
			additions = append(additions, rule)
		}
	}
	return deletes, additions
}

func ruleKey(rule FirewallRule) string {
	address := strings.ToLower(strings.TrimSpace(rule.Address))
	if address == "" || address == "any" {
		address = "anywhere"
	}
	return strings.Join([]string{string(rule.Direction), string(rule.Action), strings.ToLower(rule.Protocol), rule.Port, address}, "|")
}

func ufwAddArguments(rule FirewallRule) []string {
	args := []string{string(rule.Action), string(rule.Direction)}
	if rule.Protocol != "any" {
		args = append(args, "proto", strings.ToLower(rule.Protocol))
	}
	address := strings.TrimSpace(rule.Address)
	if address == "" {
		address = "any"
	} else if strings.EqualFold(address, "anywhere") {
		address = "any"
	}
	if rule.Direction == DirectionInbound {
		args = append(args, "from", address, "to", "any", "port", rule.Port)
	} else {
		args = append(args, "to", address, "port", rule.Port)
	}
	return args
}

func conciseError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 240 {
		return text[:240]
	}
	return text
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 2000 {
			message = message[:2000]
		}
		if message != "" {
			return string(output), fmt.Errorf("%s: %s", err, message)
		}
	}
	return string(output), err
}

func (commandRunner) LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

const windowsFirewallScript = `$administrator = (New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$profiles = @(Get-NetFirewallProfile | ForEach-Object {[pscustomobject]@{Name=$_.Name;Enabled=($_.Enabled -eq 'True')}})
$rules = @(Get-NetFirewallRule -PolicyStore ActiveStore | Sort-Object @{Expression={if ([string]::IsNullOrWhiteSpace($_.Group)) {0} else {1}}},DisplayName | Select-Object -First 200)
$ports = @($rules | Get-NetFirewallPortFilter)
$addresses = @($rules | Get-NetFirewallAddressFilter)
$portMap = @{}
foreach ($port in $ports) {$portMap[$port.InstanceID] = $port}
$addressMap = @{}
foreach ($address in $addresses) {$addressMap[$address.InstanceID] = $address}
$rules = @($rules | ForEach-Object {
  $port = $portMap[$_.InstanceID]
  $address = $addressMap[$_.InstanceID]
  [pscustomobject]@{ID=$_.Name;Name=$_.DisplayName;Direction=$_.Direction.ToString();Action=$_.Action.ToString();Profile=$_.Profile.ToString();Protocol=$port.Protocol;Port=$port.LocalPort;Address=$address.RemoteAddress;Enabled=($_.Enabled -eq 'True')}
})
[pscustomobject]@{Administrator=$administrator;Profiles=$profiles;Rules=$rules} | ConvertTo-Json -Depth 5 -Compress`

const windowsToggleFirewallScript = `& { param($encodedId,$enabled) $id=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encodedId)); Set-NetFirewallRule -Name $id -Enabled $enabled | Out-Null }`

const windowsDeleteFirewallScript = `& { param($encodedId) $id=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encodedId)); Remove-NetFirewallRule -Name $id }`

const windowsLoginScript = `$since = [DateTime]::Parse('__SINCE__').ToUniversalTime()
$events = @(Get-WinEvent -ErrorAction SilentlyContinue -FilterHashtable @{LogName='Security';Id=4624,4625,4740;StartTime=$since} -MaxEvents 5000 | ForEach-Object {
  [xml]$xml = $_.ToXml(); $data = @{}; foreach ($item in $xml.Event.EventData.Data) {$data[$item.Name] = [string]$item.'#text'}
  [pscustomobject]@{Time=$_.TimeCreated.ToUniversalTime().ToString('o');EventID=[string]$_.Id;User=$data.TargetUserName;IP=$data.IpAddress;LogonType=$data.LogonType;Status=$data.Status;SubStatus=$data.SubStatus}
})
ConvertTo-Json -InputObject $events -Compress`
