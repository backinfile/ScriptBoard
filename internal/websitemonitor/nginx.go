package websitemonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxNginxFiles        = 256
	maxNginxFileBytes    = 2 << 20
	maxNginxTotalBytes   = 16 << 20
	maxNginxIncludeDepth = 16
)

type NginxScanRequest struct {
	ConfigPath string
}

type NginxCandidate struct {
	Digest    string
	Name      string
	URL       string
	DialHost  string
	Source    string
	Duplicate bool
	Warnings  []string
}

type NginxPreview struct {
	Candidates      []NginxCandidate
	Warnings        []string
	Sources         []string
	SelectableCount int
	DuplicateCount  int
}

type NginxImportRequest struct {
	Scan    NginxScanRequest
	Digests []string
}

type nginxDirective struct {
	name     string
	args     []string
	children []nginxDirective
	source   string
}

type nginxScanState struct {
	prefix     string
	fileCount  int
	totalBytes int64
	stack      map[string]bool
	sources    []string
	warnings   []string
}

type nginxConfigSource struct {
	path   string
	prefix string
}

func (m *Manager) ScanNginx(ctx context.Context, request NginxScanRequest) (NginxPreview, error) {
	configSources, discoveryWarnings, err := m.nginxConfigPaths(ctx, request)
	if err != nil {
		return NginxPreview{}, err
	}
	state := &nginxScanState{
		stack:    make(map[string]bool),
		warnings: discoveryWarnings,
	}
	var directives []nginxDirective
	for _, source := range configSources {
		state.prefix = source.prefix
		loaded, err := state.load(ctx, source.path, 0)
		if err != nil {
			return NginxPreview{}, err
		}
		directives = append(directives, loaded...)
	}
	preview := NginxPreview{
		Sources:  append([]string(nil), state.sources...),
		Warnings: append([]string(nil), state.warnings...),
	}
	existing, err := m.List(ctx, Filter{})
	if err != nil {
		return NginxPreview{}, err
	}
	var walk func([]nginxDirective, bool)
	walk = func(nodes []nginxDirective, inHTTP bool) {
		for _, node := range nodes {
			nextHTTP := inHTTP || node.name == "http"
			if node.name == "server" && inHTTP {
				candidates, warnings := nginxServerCandidates(node)
				preview.Warnings = append(preview.Warnings, warnings...)
				for _, candidate := range candidates {
					for _, monitor := range existing {
						if monitor.Config.URL == candidate.URL &&
							monitor.Config.DialHost == candidate.DialHost &&
							monitor.Config.Kind == KindHTTP {
							candidate.Duplicate = true
							break
						}
					}
					preview.Candidates = append(preview.Candidates, candidate)
				}
			}
			if len(node.children) > 0 {
				walk(node.children, nextHTTP)
			}
		}
	}
	walk(directives, false)
	slices.SortFunc(preview.Candidates, func(left, right NginxCandidate) int {
		return strings.Compare(left.URL+"\x00"+left.DialHost, right.URL+"\x00"+right.DialHost)
	})
	for _, candidate := range preview.Candidates {
		if candidate.Duplicate {
			preview.DuplicateCount++
		} else {
			preview.SelectableCount++
		}
	}
	return preview, nil
}

func (m *Manager) nginxConfigPaths(ctx context.Context, request NginxScanRequest) ([]nginxConfigSource, []string, error) {
	var sources []nginxConfigSource
	var warnings []string
	if request.ConfigPath != "" {
		if !filepath.IsAbs(filepath.FromSlash(request.ConfigPath)) {
			return nil, nil, errors.New("Nginx 配置文件必须是绝对路径")
		}
		path, err := filepath.Abs(filepath.FromSlash(request.ConfigPath))
		if err != nil {
			return nil, nil, fmt.Errorf("解析 Nginx 配置路径: %w", err)
		}
		sources = append(sources, nginxConfigSource{path: path, prefix: filepath.Dir(path)})
	}
	processes, err := m.options.NginxProcesses.Processes(ctx)
	if err != nil {
		warnings = append(warnings, "无法读取运行中 Nginx 的启动参数："+err.Error())
	}
	for _, process := range processes {
		if !strings.EqualFold(process.Name, "nginx") && !strings.EqualFold(process.Name, "nginx.exe") {
			continue
		}
		prefix, config := nginxProcessArguments(process)
		if config == "" && prefix != "" {
			config = filepath.Join(prefix, "conf", "nginx.conf")
		}
		if config == "" {
			continue
		}
		config = filepath.FromSlash(config)
		if !filepath.IsAbs(config) {
			base := prefix
			if base == "" {
				base = process.CWD
			}
			config = filepath.Join(base, config)
		}
		absolute, absoluteErr := filepath.Abs(config)
		if absoluteErr == nil {
			includePrefix := prefix
			if includePrefix == "" {
				includePrefix = process.CWD
			}
			if includePrefix == "" {
				includePrefix = filepath.Dir(absolute)
			}
			if resolvedPrefix, prefixErr := filepath.Abs(includePrefix); prefixErr == nil {
				includePrefix = resolvedPrefix
			}
			sources = append(sources, nginxConfigSource{path: absolute, prefix: includePrefix})
		}
	}
	defaults := []string{"/etc/nginx/nginx.conf", "/usr/local/nginx/conf/nginx.conf"}
	if runtime.GOOS == "windows" {
		defaults = []string{`C:\nginx\conf\nginx.conf`}
	}
	for _, candidate := range defaults {
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			sources = append(sources, nginxConfigSource{path: candidate, prefix: filepath.Dir(candidate)})
		}
	}
	seen := make(map[string]bool)
	unique := sources[:0]
	for _, source := range sources {
		key := filepath.Clean(source.path)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			unique = append(unique, source)
		}
	}
	if len(unique) == 0 {
		warnings = append(warnings, "没有找到可读取的 Nginx 配置；可以填写配置文件的绝对路径后重试")
	}
	return unique, warnings, nil
}

func nginxProcessArguments(process NginxProcess) (string, string) {
	var prefix, config string
	for index := 1; index < len(process.Args); index++ {
		argument := process.Args[index]
		switch {
		case argument == "-p" && index+1 < len(process.Args):
			index++
			prefix = process.Args[index]
		case strings.HasPrefix(argument, "-p") && len(argument) > 2:
			prefix = strings.TrimPrefix(argument, "-p")
		case argument == "-c" && index+1 < len(process.Args):
			index++
			config = process.Args[index]
		case strings.HasPrefix(argument, "-c") && len(argument) > 2:
			config = strings.TrimPrefix(argument, "-c")
		}
	}
	prefix = filepath.FromSlash(prefix)
	if prefix != "" && !filepath.IsAbs(prefix) && process.CWD != "" {
		prefix = filepath.Join(process.CWD, prefix)
	}
	return prefix, config
}

func (m *Manager) ImportNginx(ctx context.Context, request NginxImportRequest) ([]Monitor, error) {
	if len(request.Digests) == 0 {
		return nil, operationError(
			ErrorSelectionRequired, "请至少选择一个 Nginx 网站", "digest", nil,
		)
	}
	preview, err := m.ScanNginx(ctx, request.Scan)
	if err != nil {
		return nil, err
	}
	available := make(map[string]NginxCandidate, len(preview.Candidates))
	for _, candidate := range preview.Candidates {
		available[candidate.Digest] = candidate
	}
	seenDigests := make(map[string]bool, len(request.Digests))
	nameCounts := make(map[string]int)
	var configs []Config
	for _, digest := range request.Digests {
		if seenDigests[digest] {
			continue
		}
		seenDigests[digest] = true
		candidate, ok := available[digest]
		if !ok {
			return nil, operationError(
				ErrorStaleScan, "Nginx 配置已变化，请重新扫描后再加入", "digest", nil,
			)
		}
		if candidate.Duplicate {
			return nil, operationError(
				ErrorDuplicate, fmt.Sprintf("%s 已在监控，不能重复加入", candidate.URL), "digest", nil,
			)
		}
		nameCounts[candidate.Name]++
		name := candidate.Name
		if nameCounts[candidate.Name] > 1 {
			name = fmt.Sprintf("%s (%d)", candidate.Name, nameCounts[candidate.Name])
		}
		configs = append(configs, Config{
			Name: name, Scope: ScopeLocal, Kind: KindHTTP,
			URL: candidate.URL, DialHost: candidate.DialHost,
			HTTPMethod: http.MethodGet, Frequency: time.Minute, Timeout: 10 * time.Second,
			HTTPSuccessMode: HTTPSuccessRange, Source: "nginx:" + candidate.Source,
		})
	}
	return m.createMany(ctx, configs)
}

func (state *nginxScanState) load(ctx context.Context, path string, depth int) ([]nginxDirective, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > maxNginxIncludeDepth {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：include 层级超过 %d，已跳过", path, maxNginxIncludeDepth))
		return nil, nil
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：无法读取，已跳过", path))
		return nil, nil
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, err
	}
	if state.stack[canonical] {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：检测到 include 循环，已跳过", canonical))
		return nil, nil
	}
	if state.fileCount >= maxNginxFiles {
		state.warnings = append(state.warnings, fmt.Sprintf("配置文件超过 %d 个，其余文件已跳过", maxNginxFiles))
		return nil, nil
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：不是可读取的普通文件，已跳过", canonical))
		return nil, nil
	}
	if info.Size() > maxNginxFileBytes {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：文件超过 2 MiB，已跳过", canonical))
		return nil, nil
	}
	if state.totalBytes+info.Size() > maxNginxTotalBytes {
		state.warnings = append(state.warnings, "本次扫描已达到 16 MiB 总读取上限")
		return nil, nil
	}
	content, err := os.ReadFile(canonical)
	if err != nil {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：读取失败，已跳过", canonical))
		return nil, nil
	}
	state.fileCount++
	state.totalBytes += int64(len(content))
	state.sources = append(state.sources, canonical)
	state.stack[canonical] = true
	defer delete(state.stack, canonical)

	tokens, err := tokenizeNginx(string(content))
	if err != nil {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：语法无法完整解析：%v", canonical, err))
		return nil, nil
	}
	position := 0
	nodes, err := parseNginxDirectives(tokens, &position, canonical)
	if err != nil {
		state.warnings = append(state.warnings, fmt.Sprintf("%s：语法无法完整解析：%v", canonical, err))
		return nil, nil
	}
	return state.expandIncludes(ctx, nodes, depth), nil
}

func (state *nginxScanState) expandIncludes(ctx context.Context, nodes []nginxDirective, depth int) []nginxDirective {
	result := make([]nginxDirective, 0, len(nodes))
	for _, node := range nodes {
		if node.name == "include" && len(node.args) > 0 {
			pattern := filepath.FromSlash(node.args[0])
			if strings.Contains(pattern, "$") {
				state.warnings = append(state.warnings, fmt.Sprintf("%s：动态 include %q 无法解析，已跳过", node.source, node.args[0]))
				continue
			}
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(state.prefix, pattern)
			}
			matches, err := filepath.Glob(pattern)
			if err != nil || len(matches) == 0 {
				state.warnings = append(state.warnings, fmt.Sprintf("%s：include %q 没有可读取文件", node.source, node.args[0]))
				continue
			}
			slices.Sort(matches)
			for _, match := range matches {
				included, err := state.load(ctx, match, depth+1)
				if err != nil {
					state.warnings = append(state.warnings, fmt.Sprintf("%s：include %q 读取失败：%v", node.source, match, err))
					continue
				}
				result = append(result, included...)
			}
			continue
		}
		if len(node.children) > 0 {
			node.children = state.expandIncludes(ctx, node.children, depth)
		}
		result = append(result, node)
	}
	return result
}

func tokenizeNginx(content string) ([]string, error) {
	var tokens []string
	var value strings.Builder
	flush := func() {
		if value.Len() > 0 {
			tokens = append(tokens, value.String())
			value.Reset()
		}
	}
	quote := rune(0)
	escaped := false
	comment := false
	for _, current := range content {
		if comment {
			if current == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			value.WriteRune(current)
			escaped = false
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			} else {
				value.WriteRune(current)
			}
			continue
		}
		switch current {
		case '#':
			flush()
			comment = true
		case '\'', '"':
			quote = current
		case '{', '}', ';':
			flush()
			tokens = append(tokens, string(current))
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			value.WriteRune(current)
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("未结束的引号或转义")
	}
	flush()
	return tokens, nil
}

func parseNginxDirectives(tokens []string, position *int, source string) ([]nginxDirective, error) {
	var result []nginxDirective
	for *position < len(tokens) {
		if tokens[*position] == "}" {
			*position++
			return result, nil
		}
		node := nginxDirective{name: tokens[*position], source: source}
		*position++
		for *position < len(tokens) {
			token := tokens[*position]
			*position++
			switch token {
			case ";":
				result = append(result, node)
				goto next
			case "{":
				children, err := parseNginxDirectives(tokens, position, source)
				if err != nil {
					return nil, err
				}
				node.children = children
				result = append(result, node)
				goto next
			case "}":
				return nil, fmt.Errorf("指令 %q 缺少分号", node.name)
			default:
				node.args = append(node.args, token)
			}
		}
		return nil, fmt.Errorf("指令 %q 未结束", node.name)
	next:
	}
	return result, nil
}

func nginxServerCandidates(server nginxDirective) ([]NginxCandidate, []string) {
	var listens []nginxListen
	var names []string
	serverSSL := false
	var warnings []string
	for _, directive := range server.children {
		switch directive.name {
		case "listen":
			listen, warning := parseNginxListen(directive.args)
			if warning != "" {
				warnings = append(warnings, fmt.Sprintf("%s：%s", directive.source, warning))
			} else {
				listens = append(listens, listen)
			}
		case "server_name":
			for _, name := range directive.args {
				if validExactServerName(name) {
					names = append(names, name)
				} else {
					warnings = append(warnings, fmt.Sprintf("%s：server_name %q 不是可导入的精确主机名，已跳过", directive.source, name))
				}
			}
		case "ssl":
			serverSSL = len(directive.args) > 0 && directive.args[0] == "on"
		}
	}
	if len(listens) == 0 {
		listens = []nginxListen{{port: 80, dialHost: "127.0.0.1"}}
	}
	var candidates []NginxCandidate
	for _, name := range names {
		for _, listen := range listens {
			secure := serverSSL || listen.ssl
			scheme := "http"
			defaultPort := 80
			if secure {
				scheme = "https"
				defaultPort = 443
			}
			host := name
			if net.ParseIP(name) != nil && strings.Contains(name, ":") {
				host = "[" + name + "]"
			}
			if listen.port != defaultPort {
				host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(listen.port))
			}
			candidate := NginxCandidate{
				Name: name, URL: scheme + "://" + host + "/",
				DialHost: listen.dialHost, Source: server.source,
			}
			sum := sha256.Sum256([]byte(candidate.URL + "\x00" + candidate.DialHost))
			candidate.Digest = hex.EncodeToString(sum[:])
			candidates = append(candidates, candidate)
		}
	}
	return candidates, warnings
}

type nginxListen struct {
	port     int
	dialHost string
	ssl      bool
}

func parseNginxListen(args []string) (nginxListen, string) {
	if len(args) == 0 {
		return nginxListen{}, "listen 指令没有地址"
	}
	value := args[0]
	if strings.HasPrefix(value, "unix:") {
		return nginxListen{}, "Unix Socket listen 不支持网站监控"
	}
	result := nginxListen{dialHost: "127.0.0.1"}
	result.ssl = slices.Contains(args[1:], "ssl")
	if port, err := strconv.Atoi(value); err == nil {
		result.port = port
		return result, ""
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return nginxListen{}, fmt.Sprintf("listen %q 无法解析", value)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nginxListen{}, fmt.Sprintf("listen %q 端口无效", value)
	}
	result.port = port
	switch strings.Trim(host, "[]") {
	case "", "*", "0.0.0.0":
		result.dialHost = "127.0.0.1"
	case "::":
		result.dialHost = "::1"
	default:
		result.dialHost = strings.Trim(host, "[]")
	}
	return result, ""
}

var nginxHostnamePattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validExactServerName(value string) bool {
	if value == "" || value == "_" || strings.ContainsAny(value, "*$~") {
		return false
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return true
	}
	if len(value) > 253 || !nginxHostnamePattern.MatchString(value) {
		return false
	}
	parsed, err := url.Parse("http://" + value + "/")
	return err == nil && parsed.Hostname() == value
}
