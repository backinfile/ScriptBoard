package websitemonitor

import (
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"slices"
	"strings"
)

const (
	maxRequestHeaders     = 32
	maxRequestHeaderBytes = 16 << 10
)

// RequestHeader is one administrator-supplied HTTP header. A slice is used
// instead of a map so repeated header fields survive editing and export.
type RequestHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var reservedRequestHeaders = map[string]struct{}{
	"connection": {}, "content-length": {}, "proxy-connection": {},
	"transfer-encoding": {}, "upgrade": {},
	"sec-websocket-accept": {}, "sec-websocket-extensions": {},
	"sec-websocket-key": {}, "sec-websocket-version": {},
}

var requestHeaderVariableReference = regexp.MustCompile(`\{\{([A-Z][A-Z0-9_]{0,63})\}\}`)

// ParseRequestHeaders parses the form's one-header-per-line representation.
func ParseRequestHeaders(input string) ([]RequestHeader, error) {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	headers := make([]RequestHeader, 0, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("请求头第 %d 行必须使用“名称: 值”格式", index+1)
		}
		headers = append(headers, RequestHeader{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return normalizeRequestHeaders(headers)
}

func FormatRequestHeaders(headers []RequestHeader) string {
	lines := make([]string, 0, len(headers))
	for _, header := range headers {
		lines = append(lines, header.Name+": "+header.Value)
	}
	return strings.Join(lines, "\n")
}

// RequestHeaderVariables returns the unique variable names referenced by
// header values. References use the same {{VARIABLE_NAME}} spelling as Run
// arguments, but may be embedded in a header value.
func RequestHeaderVariables(headers []RequestHeader) ([]string, error) {
	seen := make(map[string]struct{})
	for _, header := range headers {
		remaining := requestHeaderVariableReference.ReplaceAllStringFunc(header.Value, func(reference string) string {
			match := requestHeaderVariableReference.FindStringSubmatch(reference)
			seen[match[1]] = struct{}{}
			return ""
		})
		if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
			return nil, fmt.Errorf("自定义请求头 %q 包含无效的 Variable 引用", header.Name)
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// ResolveRequestHeaders expands variable references without mutating the
// stored header templates, then reapplies all request-header safety limits.
func ResolveRequestHeaders(headers []RequestHeader, variables map[string]string) ([]RequestHeader, error) {
	if _, err := RequestHeaderVariables(headers); err != nil {
		return nil, err
	}
	resolved := make([]RequestHeader, len(headers))
	for index, header := range headers {
		var resolveErr error
		value := requestHeaderVariableReference.ReplaceAllStringFunc(header.Value, func(reference string) string {
			match := requestHeaderVariableReference.FindStringSubmatch(reference)
			value, exists := variables[match[1]]
			if !exists && resolveErr == nil {
				resolveErr = fmt.Errorf("Variable %s 不存在", match[1])
			}
			return value
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved[index] = RequestHeader{Name: header.Name, Value: value}
	}
	return normalizeRequestHeadersWithVariableSyntax(resolved, false)
}

func normalizeRequestHeaders(headers []RequestHeader) ([]RequestHeader, error) {
	return normalizeRequestHeadersWithVariableSyntax(headers, true)
}

func normalizeRequestHeadersWithVariableSyntax(headers []RequestHeader, validateVariableSyntax bool) ([]RequestHeader, error) {
	if len(headers) > maxRequestHeaders {
		return nil, fmt.Errorf("自定义请求头不能超过 %d 项", maxRequestHeaders)
	}
	if validateVariableSyntax {
		if _, err := RequestHeaderVariables(headers); err != nil {
			return nil, err
		}
	}
	normalized := make([]RequestHeader, 0, len(headers))
	totalBytes := 0
	hostSeen := false
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		value := strings.TrimSpace(header.Value)
		if !validHeaderName(name) {
			return nil, fmt.Errorf("自定义请求头名称 %q 无效", name)
		}
		if !validHeaderValue(value) {
			return nil, errors.New("自定义请求头值不能包含换行符或控制字符")
		}
		if _, reserved := reservedRequestHeaders[strings.ToLower(name)]; reserved {
			return nil, fmt.Errorf("自定义请求头 %q 由 HTTP/WebSocket 协议栈管理", name)
		}
		if strings.EqualFold(name, "Host") {
			if hostSeen || value == "" {
				return nil, errors.New("自定义 Host 请求头必须有值且只能配置一次")
			}
			hostSeen = true
		}
		totalBytes += len(name) + len(value)
		if totalBytes > maxRequestHeaderBytes {
			return nil, fmt.Errorf("自定义请求头总大小不能超过 %d KiB", maxRequestHeaderBytes>>10)
		}
		normalized = append(normalized, RequestHeader{Name: textproto.CanonicalMIMEHeaderKey(name), Value: value})
	}
	return normalized, nil
}

func validHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= ' ' && character != 0x7f {
			continue
		}
		return false
	}
	return true
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}
