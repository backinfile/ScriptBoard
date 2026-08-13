package web

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

func parseAllowedHosts(values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		values = []string{"127.0.0.1", "::1", "localhost"}
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		host, err := normalizeHTTPHost(value)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed host %q: %w", value, err)
		}
		result[host] = struct{}{}
	}
	return result, nil
}

func (a *App) validRequestHost(hostport string) bool {
	host, err := normalizeHTTPHost(hostport)
	if err != nil {
		return false
	}
	_, ok := a.allowedHosts[host]
	return ok
}

func normalizeHTTPHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/\\,@") {
		return "", fmt.Errorf("host is empty or contains a separator")
	}
	for _, character := range value {
		if character > unicode.MaxASCII || unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", fmt.Errorf("host contains a non-ASCII, control, or whitespace character")
		}
	}
	host := value
	if parsedHost, rawPort, err := net.SplitHostPort(value); err == nil {
		port, portErr := strconv.ParseUint(rawPort, 10, 16)
		if portErr != nil || port == 0 {
			return "", fmt.Errorf("host has an invalid port")
		}
		host = parsedHost
	} else if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	} else if strings.Count(value, ":") == 1 {
		return "", fmt.Errorf("host has an invalid port")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("host has an invalid DNS label")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("host has an invalid DNS character")
			}
		}
	}
	return host, nil
}
