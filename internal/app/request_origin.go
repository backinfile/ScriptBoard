package app

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func validRequestOrigin(request *http.Request) bool {
	rawOrigin := strings.TrimSpace(request.Header.Get("Origin"))
	if rawOrigin == "" {
		return true
	}
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	wantScheme := "http"
	if isSecureRequest(request) {
		wantScheme = "https"
	}
	return strings.EqualFold(origin.Scheme, wantScheme) &&
		strings.EqualFold(normalizeOriginHost(origin.Host, wantScheme), normalizeOriginHost(request.Host, wantScheme))
}

func normalizeOriginHost(host, scheme string) string {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		return strings.TrimSuffix(strings.ToLower(host), ".")
	}
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		return strings.TrimSuffix(strings.ToLower(hostname), ".")
	}
	return strings.ToLower(net.JoinHostPort(strings.TrimSuffix(hostname, "."), port))
}
