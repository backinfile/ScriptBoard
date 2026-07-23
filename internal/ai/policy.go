package ai

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type Permission struct {
	Query   bool `json:"query"`
	Execute bool `json:"execute"`
	Modify  bool `json:"modify"`
}

func (p Permission) normalized() Permission {
	if p.Execute || p.Modify {
		p.Query = true
	}
	return p
}

// EffectivePermission intersects the model ceiling, conversation grant, and
// activated Skill requirements. Execute and Modify always imply Query.
func EffectivePermission(model, conversation, skill Permission) Permission {
	model = model.normalized()
	conversation = conversation.normalized()
	skill = skill.normalized()
	return Permission{
		Query:   model.Query && conversation.Query && skill.Query,
		Execute: model.Execute && conversation.Execute && skill.Execute,
		Modify:  model.Modify && conversation.Modify && skill.Modify,
	}.normalized()
}

func ValidateEndpoint(raw string) error {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" {
		return errors.New("model endpoint must be an absolute HTTP URL")
	}
	if endpoint.User != nil {
		return errors.New("model endpoint must not contain user information")
	}
	switch strings.ToLower(endpoint.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return errors.New("plain HTTP model endpoints are restricted to loopback addresses")
	default:
		return errors.New("model endpoint must use HTTPS or loopback HTTP")
	}
}

var forbiddenExtraHeaders = map[string]struct{}{
	"host":                {},
	"content-length":      {},
	"connection":          {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"proxy-authorization": {},
}

func ValidateExtraHeaders(headers map[string]string) error {
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || strings.ContainsAny(canonical, "\r\n") {
			return fmt.Errorf("invalid extra header name %q", name)
		}
		if _, forbidden := forbiddenExtraHeaders[strings.ToLower(canonical)]; forbidden {
			return fmt.Errorf("extra header %q is controlled by the HTTP transport", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("extra header %q contains a newline", name)
		}
	}
	return nil
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	for name, value := range t.headers {
		if value == "" {
			cloned.Header.Del(name)
		} else {
			cloned.Header.Set(name, value)
		}
	}
	return t.base.RoundTrip(cloned)
}

// SecureHTTPClient applies administrator-supplied headers after provider
// defaults and refuses to forward credentials across origins.
func SecureHTTPClient(headers map[string]string) *http.Client {
	copied := make(map[string]string, len(headers))
	for name, value := range headers {
		copied[name] = value
	}
	return &http.Client{
		Transport: headerTransport{base: http.DefaultTransport, headers: copied},
		CheckRedirect: func(next *http.Request, previous []*http.Request) error {
			if len(previous) == 0 {
				return nil
			}
			first := previous[0].URL
			if !strings.EqualFold(first.Scheme, next.URL.Scheme) || !strings.EqualFold(first.Host, next.URL.Host) {
				return errors.New("cross-origin model endpoint redirect refused")
			}
			return nil
		},
	}
}
