package providerproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"scriptboard/internal/outboundpolicy"
)

const (
	maximumRequestBytes  = 32 << 20
	maximumResponseBytes = 64 << 20
)

type Config struct {
	Provider, Model, Endpoint, Credential string
}

// Session is a loopback-only, process-lifetime capability that forwards one
// model's inference API. The managed Runtime never receives the upstream URL or
// provider credential.
type Session struct {
	server     *http.Server
	listener   net.Listener
	endpoint   string
	capability string
	closeOnce  sync.Once
	closeErr   error
}

func Start(config Config) (*Session, error) {
	provider := strings.TrimSpace(config.Provider)
	model := strings.TrimSpace(config.Model)
	credential := strings.TrimSpace(config.Credential)
	if model == "" || credential == "" || strings.ContainsAny(credential, "\r\n\x00") {
		return nil, errors.New("provider model and credential are required")
	}
	upstream, route, local, err := parseUpstream(provider, config.Endpoint)
	if err != nil {
		return nil, err
	}
	capability, err := newCapability()
	if err != nil {
		return nil, fmt.Errorf("create provider proxy capability: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for provider proxy: %w", err)
	}
	basePath := strings.TrimRight(upstream.EscapedPath(), "/")
	if basePath == "" {
		basePath = ""
	}
	proxyEndpoint := "http://" + listener.Addr().String() + basePath
	policy := outboundpolicy.Policy{AllowPrivate: local, AllowAnyPort: local}
	client := &http.Client{
		Transport: policy.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("provider redirects are not allowed")
		},
	}
	handler := &proxyHandler{
		provider: provider, model: model, credential: credential, capability: capability,
		upstream: upstream, allowedPath: joinURLPath(upstream.EscapedPath(), route), client: client,
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	session := &Session{server: server, listener: listener, endpoint: proxyEndpoint, capability: capability}
	go func() { _ = server.Serve(listener) }()
	return session, nil
}

func (session *Session) Endpoint() string   { return session.endpoint }
func (session *Session) Capability() string { return session.capability }

func (session *Session) Close(ctx context.Context) error {
	session.closeOnce.Do(func() {
		session.closeErr = session.server.Shutdown(ctx)
		if session.closeErr != nil {
			_ = session.server.Close()
		}
		if closeErr := session.listener.Close(); session.closeErr == nil && closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			session.closeErr = closeErr
		}
	})
	return session.closeErr
}

type proxyHandler struct {
	provider, model, credential, capability string
	upstream                                *url.URL
	allowedPath                             string
	client                                  *http.Client
}

func (handler *proxyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.URL.EscapedPath() != handler.allowedPath || request.URL.RawQuery != "" {
		http.NotFound(response, request)
		return
	}
	if !handler.authorized(request.Header) {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.ContentLength > maximumRequestBytes {
		http.Error(response, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumRequestBytes+1))
	if err != nil || len(body) > maximumRequestBytes {
		http.Error(response, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var envelope struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Model != handler.model {
		http.Error(response, "model is not allowed", http.StatusForbidden)
		return
	}

	upstreamURL := *handler.upstream
	upstreamURL.Path = request.URL.Path
	upstreamURL.RawPath = request.URL.RawPath
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodPost, upstreamURL.String(), strings.NewReader(string(body)))
	if err != nil {
		http.Error(response, "provider request failed", http.StatusBadGateway)
		return
	}
	copyRequestHeaders(upstreamRequest.Header, request.Header)
	if handler.provider == "anthropic" {
		upstreamRequest.Header.Set("X-Api-Key", handler.credential)
	} else {
		upstreamRequest.Header.Set("Authorization", "Bearer "+handler.credential)
	}
	upstreamResponse, err := handler.client.Do(upstreamRequest)
	if err != nil {
		http.Error(response, "provider request failed", http.StatusBadGateway)
		return
	}
	defer upstreamResponse.Body.Close()
	copyResponseHeaders(response.Header(), upstreamResponse.Header)
	response.WriteHeader(upstreamResponse.StatusCode)
	_, _ = io.Copy(&flushingLimitWriter{response: response, remaining: maximumResponseBytes}, upstreamResponse.Body)
}

func (handler *proxyHandler) authorized(header http.Header) bool {
	provided := ""
	if handler.provider == "anthropic" {
		values := header.Values("X-Api-Key")
		if len(values) != 1 || len(header.Values("Authorization")) != 0 {
			return false
		}
		provided = values[0]
	} else {
		values := header.Values("Authorization")
		if len(values) != 1 || len(header.Values("X-Api-Key")) != 0 {
			return false
		}
		provided = strings.TrimPrefix(values[0], "Bearer ")
		if provided == values[0] {
			return false
		}
	}
	return len(provided) == len(handler.capability) && subtle.ConstantTimeCompare([]byte(provided), []byte(handler.capability)) == 1
}

func parseUpstream(provider, rawEndpoint string) (*url.URL, string, bool, error) {
	route := ""
	switch provider {
	case "openai":
		route = "/responses"
	case "openai-compatible":
		route = "/chat/completions"
	case "anthropic":
		route = "/v1/messages"
	default:
		return nil, "", false, fmt.Errorf("unsupported provider %q", provider)
	}
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "", false, errors.New("invalid provider endpoint")
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	local := strings.EqualFold(host, "localhost") || address != nil && address.IsLoopback()
	if parsed.Scheme != "https" {
		if parsed.Scheme != "http" || !local {
			return nil, "", false, errors.New("provider endpoint must use HTTPS or loopback HTTP")
		}
	}
	return parsed, route, local, nil
}

func newCapability() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func joinURLPath(base, route string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(route, "/")
}

func copyRequestHeaders(target, source http.Header) {
	for _, name := range []string{"Accept", "Content-Type", "Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta"} {
		for _, value := range source.Values(name) {
			target.Add(name, value)
		}
	}
}

func copyResponseHeaders(target, source http.Header) {
	for _, name := range []string{"Content-Type", "Retry-After", "Request-Id", "X-Request-Id"} {
		for _, value := range source.Values(name) {
			target.Add(name, value)
		}
	}
}

type flushingLimitWriter struct {
	response  http.ResponseWriter
	remaining int64
}

func (writer *flushingLimitWriter) Write(payload []byte) (int, error) {
	if writer.remaining <= 0 {
		return 0, errors.New("provider response too large")
	}
	if int64(len(payload)) > writer.remaining {
		payload = payload[:writer.remaining]
	}
	written, err := writer.response.Write(payload)
	writer.remaining -= int64(written)
	if flusher, ok := writer.response.(http.Flusher); ok {
		flusher.Flush()
	}
	if err == nil && writer.remaining == 0 {
		err = errors.New("provider response too large")
	}
	return written, err
}
