package websitemonitor

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"scriptboard/internal/outboundpolicy"
	"scriptboard/internal/secretredaction"
)

const maxResponseBodyBytes = 1 << 20

type NetworkProbe struct{}

func (NetworkProbe) Check(ctx context.Context, config Config) Evidence {
	if config.Kind == KindWebSocket {
		return checkWebSocket(ctx, config)
	}
	started := time.Now()
	result := Evidence{CheckedAt: started.UTC()}
	skipTLSVerification := config.SkipTLSVerificationAt(started)
	if config.Kind != KindHTTP {
		result.ErrorCategory = "internal"
		result.Summary = "暂不支持这种检查方式"
		return result
	}
	request, err := http.NewRequestWithContext(ctx, config.HTTPMethod, config.URL, strings.NewReader(config.HTTPBody))
	if err != nil {
		result.ErrorCategory = "internal"
		result.Summary = "无法创建网站请求"
		result.TechnicalError = secretredaction.String(err.Error())
		return result
	}
	applyHTTPRequestHeaders(request, config.RequestHeaders)
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", UserAgent)
	}
	if config.HTTPMethod == http.MethodPost && config.HTTPContentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", config.HTTPContentType)
	}
	policy := outboundPolicy(config)
	transport := policy.Transport()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: skipTLSVerification} //nolint:gosec -- bounded per-monitor administrator exception
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if config.DisableRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) > 0 && (via[0].Header.Get("Authorization") != "" || via[0].Header.Get("Cookie") != "") {
				return http.ErrUseLastResponse
			}
			if len(via) > 5 {
				return errors.New("website redirect limit exceeded")
			}
			return nil
		},
	}
	if config.Timeout > 0 {
		client.Timeout = config.Timeout
	}
	response, err := client.Do(request)
	result.Latency = time.Since(started)
	result.CheckedAt = time.Now().UTC()
	if err != nil {
		result.ErrorCategory = categorizeNetworkError(err)
		result.Summary = "网站请求未完成"
		result.TechnicalError = secretredaction.String(err.Error())
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	result.Certificate = certificateFromHTTP(response, !skipTLSVerification, result.CheckedAt)
	if !acceptedHTTPStatus(config, response.StatusCode) {
		result.ErrorCategory = "http-status"
		result.Summary = fmt.Sprintf("网站返回了不符合预期的 HTTP %d", response.StatusCode)
		return result
	}
	if config.ResponseKeyword != "" {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
		if readErr != nil {
			result.ErrorCategory = "connect"
			result.Summary = "读取网站响应时连接中断"
			result.TechnicalError = secretredaction.String(readErr.Error())
			return result
		}
		if len(body) > maxResponseBodyBytes {
			result.ErrorCategory = "body-limit"
			result.Summary = "在读取上限内没有找到要求的返回内容"
			return result
		}
		text := strings.ToValidUTF8(string(body), "\uFFFD")
		if !strings.Contains(text, config.ResponseKeyword) {
			result.ErrorCategory = "keyword"
			result.Summary = "网站响应中没有要求的内容"
			return result
		}
	}
	result.Success = true
	result.Summary = fmt.Sprintf("网站返回 HTTP %d", response.StatusCode)
	return result
}

func checkWebSocket(ctx context.Context, config Config) Evidence {
	started := time.Now()
	result := Evidence{CheckedAt: started.UTC()}
	skipTLSVerification := config.SkipTLSVerificationAt(started)
	dialer := websocket.Dialer{
		NetDialContext:  outboundPolicy(config).DialContext,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLSVerification}, //nolint:gosec -- bounded per-monitor administrator exception
	}
	headers := http.Header{}
	applyRequestHeaders(headers, config.RequestHeaders)
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", UserAgent)
	}
	connection, response, err := dialer.DialContext(ctx, config.URL, headers)
	result.Latency = time.Since(started)
	result.CheckedAt = time.Now().UTC()
	if err != nil {
		result.ErrorCategory = categorizeNetworkError(err)
		result.Summary = "WebSocket 连接未建立"
		result.TechnicalError = secretredaction.String(err.Error())
		if response != nil {
			result.StatusCode = response.StatusCode
			_ = response.Body.Close()
		}
		return result
	}
	defer connection.Close()
	result.StatusCode = http.StatusSwitchingProtocols
	result.Certificate = certificateFromConnection(connection.UnderlyingConn(), !skipTLSVerification, result.CheckedAt)
	if config.WebSocketSuccess == "" || config.WebSocketSuccess == WebSocketHandshake {
		result.Success = true
		result.Summary = "WebSocket 连接已建立"
		return result
	}
	connection.SetReadLimit(maxResponseBodyBytes)
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetReadDeadline(deadline)
		_ = connection.SetWriteDeadline(deadline)
	}
	if config.WebSocketSuccess == WebSocketPingPong {
		return checkWebSocketPingPong(ctx, connection, config, result)
	}
	if err := writeApplicationMessage(connection, config); err != nil {
		result.ErrorCategory = "connect"
		result.Summary = "WebSocket 应用消息未发送"
		result.TechnicalError = secretredaction.String(err.Error())
		return result
	}
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			result.ErrorCategory = categorizeNetworkError(err)
			result.Summary = "等待 WebSocket 应用消息时未收到有效响应"
			result.TechnicalError = secretredaction.String(err.Error())
			return result
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		if config.WebSocketSuccess == WebSocketAnyMessage {
			result.Success = true
			result.Summary = "已收到 WebSocket 应用消息"
			return result
		}
		if matchingApplicationMessage(messageType, payload, config) {
			result.Success = true
			result.Summary = "已收到匹配的 WebSocket 应用消息"
			return result
		}
	}
}

func outboundPolicy(config Config) outboundpolicy.Policy {
	local := config.Scope == ScopeLocal
	return outboundpolicy.Policy{AllowPrivate: local, AllowAnyPort: local}
}

func applyRequestHeaders(target http.Header, headers []RequestHeader) {
	for _, header := range headers {
		target.Add(header.Name, header.Value)
	}
}

func applyHTTPRequestHeaders(request *http.Request, headers []RequestHeader) {
	for _, header := range headers {
		if strings.EqualFold(header.Name, "Host") {
			request.Host = header.Value
			continue
		}
		request.Header.Add(header.Name, header.Value)
	}
}

func checkWebSocketPingPong(ctx context.Context, connection *websocket.Conn, config Config, result Evidence) Evidence {
	payload, err := decodePayload(config.PingPayloadFormat, config.PingPayload)
	if err != nil || len(payload) > 125 {
		result.ErrorCategory = "internal"
		result.Summary = "Ping 载荷无效"
		if err != nil {
			result.TechnicalError = secretredaction.String(err.Error())
		}
		return result
	}
	matched := make(chan struct{}, 1)
	connection.SetPongHandler(func(received string) error {
		if bytes.Equal([]byte(received), payload) {
			select {
			case matched <- struct{}{}:
			default:
			}
		}
		return nil
	})
	readError := make(chan error, 1)
	go func() {
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				readError <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(config.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.WriteControl(websocket.PingMessage, payload, deadline); err != nil {
		result.ErrorCategory = categorizeNetworkError(err)
		result.Summary = "Ping 控制帧未发送"
		result.TechnicalError = secretredaction.String(err.Error())
		return result
	}
	select {
	case <-matched:
		result.Success = true
		result.Summary = "Pong 载荷与 Ping 完全一致"
		return result
	case err := <-readError:
		result.ErrorCategory = categorizeNetworkError(err)
		result.Summary = "等待匹配的 Pong 时连接已结束"
		result.TechnicalError = secretredaction.String(err.Error())
		return result
	case <-ctx.Done():
		result.ErrorCategory = "timeout"
		result.Summary = "等待匹配的 Pong 超时"
		result.TechnicalError = ctx.Err().Error()
		return result
	}
}

func writeApplicationMessage(connection *websocket.Conn, config Config) error {
	switch config.SendType {
	case "", MessageNone:
		return nil
	case MessageText:
		return connection.WriteMessage(websocket.TextMessage, []byte(config.SendPayload))
	case MessageBinary:
		payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.SendPayload))
		if err != nil {
			return fmt.Errorf("decode binary WebSocket payload: %w", err)
		}
		return connection.WriteMessage(websocket.BinaryMessage, payload)
	default:
		return fmt.Errorf("unsupported WebSocket send type %q", config.SendType)
	}
}

func matchingApplicationMessage(messageType int, payload []byte, config Config) bool {
	if config.ReceiveType == MessageText {
		return messageType == websocket.TextMessage &&
			strings.Contains(strings.ToValidUTF8(string(payload), "\uFFFD"), config.ExpectedMessage)
	}
	if config.ReceiveType == MessageBinary {
		expected, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.ExpectedMessage))
		return err == nil && messageType == websocket.BinaryMessage && bytes.Equal(payload, expected)
	}
	return false
}

func decodePayload(format PayloadFormat, value string) ([]byte, error) {
	switch format {
	case "", PayloadNone:
		return nil, nil
	case PayloadText:
		return []byte(value), nil
	case PayloadHex:
		compact := strings.Join(strings.Fields(value), "")
		return hex.DecodeString(compact)
	case PayloadBase64:
		return base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	default:
		return nil, fmt.Errorf("unsupported payload format %q", format)
	}
}

func acceptedHTTPStatus(config Config, status int) bool {
	if config.HTTPSuccessMode == HTTPSuccessAnyResponse {
		return true
	}
	if config.HTTPSuccessMode != HTTPSuccessExact {
		return status >= 200 && status <= 399
	}
	for _, expected := range ExpectedHTTPStatusRanges(config) {
		if status >= expected.Start && status <= expected.End {
			return true
		}
	}
	return false
}

func categorizeNetworkError(err error) string {
	if err == context.DeadlineExceeded || errorsIsTimeout(err) {
		return "timeout"
	}
	if strings.Contains(strings.ToLower(err.Error()), "tls") || strings.Contains(strings.ToLower(err.Error()), "certificate") {
		return "tls"
	}
	return "connect"
}

func errorsIsTimeout(err error) bool {
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

func certificateFromHTTP(response *http.Response, verified bool, now time.Time) Certificate {
	if response.TLS == nil || len(response.TLS.PeerCertificates) == 0 {
		return Certificate{}
	}
	leaf := response.TLS.PeerCertificates[0]
	return Certificate{
		Subject:       leaf.Subject.String(),
		Issuer:        leaf.Issuer.String(),
		NotBefore:     leaf.NotBefore,
		NotAfter:      leaf.NotAfter,
		DaysRemaining: int(leaf.NotAfter.Sub(now).Hours() / 24),
		TLSVersion:    tlsVersionName(response.TLS.Version),
		Verified:      verified,
	}
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return ""
	}
}

func certificateFromConnection(connection net.Conn, verified bool, now time.Time) Certificate {
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return Certificate{}
	}
	state := tlsConnection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Certificate{}
	}
	leaf := state.PeerCertificates[0]
	return Certificate{
		Subject:       leaf.Subject.String(),
		Issuer:        leaf.Issuer.String(),
		NotBefore:     leaf.NotBefore,
		NotAfter:      leaf.NotAfter,
		DaysRemaining: int(leaf.NotAfter.Sub(now).Hours() / 24),
		TLSVersion:    tlsVersionName(state.Version),
		Verified:      verified,
	}
}
