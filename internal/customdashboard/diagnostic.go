package customdashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scriptboard/internal/registryconnection"
	"scriptboard/internal/registrymonitor"
)

const maxTestPreviewBytes = 256 << 10

const (
	DiagnosticOK               = "ok"
	DiagnosticDNS              = "dns_connection"
	DiagnosticTLS              = "tls"
	DiagnosticTimeout          = "timeout"
	DiagnosticUnauthorized     = "unauthorized"
	DiagnosticHTTP             = "http_status"
	DiagnosticNonJSON          = "non_json"
	DiagnosticTooLarge         = "response_too_large"
	DiagnosticPathMissing      = "json_path_missing"
	DiagnosticTypeMismatch     = "value_type_mismatch"
	DiagnosticRegistryAuth     = "registry_auth"
	DiagnosticRegistryManifest = "registry_manifest"
)

// RequestDiagnostic is deliberately limited to safe metadata. Response bodies,
// credentials and request headers must never be added to this persisted type.
type RequestDiagnostic struct {
	Code          string    `json:"code"`
	Stage         string    `json:"stage"`
	Summary       string    `json:"summary"`
	URL           string    `json:"url,omitempty"`
	HTTPStatus    int       `json:"httpStatus,omitempty"`
	DurationMS    int64     `json:"durationMs"`
	ContentType   string    `json:"contentType,omitempty"`
	ResponseBytes int64     `json:"responseBytes,omitempty"`
	AttemptedAt   time.Time `json:"attemptedAt"`
}

type TestResult struct {
	OK             bool                          `json:"ok"`
	Diagnostic     RequestDiagnostic             `json:"diagnostic"`
	Document       any                           `json:"document,omitempty"`
	RawResponse    string                        `json:"rawResponse,omitempty"`
	RawTruncated   bool                          `json:"rawTruncated,omitempty"`
	Value          any                           `json:"value,omitempty"`
	Secondary      any                           `json:"secondary,omitempty"`
	ValueType      string                        `json:"valueType,omitempty"`
	RequestHeaders map[string]string             `json:"requestHeaders,omitempty"`
	Images         []registrymonitor.ImageResult `json:"images,omitempty"`
}

func (m *Manager) TestCard(ctx context.Context, input CardInput, existingCardID string) (TestResult, error) {
	if err := validateCard(&input); err != nil {
		return TestResult{}, err
	}
	result, _ := m.runCardRequest(ctx, input, existingCardID)
	return result, nil
}

func (m *Manager) runCardRequest(ctx context.Context, input CardInput, existingCardID string) (TestResult, error) {
	if input.Type == CardRegistry {
		return m.runRegistryRequest(ctx, input, existingCardID)
	}
	started := time.Now()
	diagnostic := RequestDiagnostic{Code: DiagnosticOK, Stage: "complete", Summary: "请求成功", URL: redactRequestURL(input.SourceURL), AttemptedAt: m.now().UTC()}
	result := TestResult{Diagnostic: diagnostic, RequestHeaders: redactHeaders(input.Headers)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.SourceURL, nil)
	if err != nil {
		return finishRequestFailure(result, started, "request", classifyNetworkError(err), "无法创建请求", err), err
	}
	for name, value := range input.Headers {
		req.Header.Set(name, value)
	}
	response, err := m.client.Do(req)
	if err != nil {
		code := classifyNetworkError(err)
		return finishRequestFailure(result, started, "connect", code, actionableSummary(code), err), err
	}
	defer response.Body.Close()
	result.Diagnostic.HTTPStatus = response.StatusCode
	result.Diagnostic.ContentType = boundedText(response.Header.Get("Content-Type"), 120)
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	result.Diagnostic.ResponseBytes = int64(len(raw))
	preview := raw
	if len(preview) > maxTestPreviewBytes {
		preview = preview[:maxTestPreviewBytes]
		result.RawTruncated = true
	}
	result.RawResponse = string(preview)
	if readErr != nil {
		return finishRequestFailure(result, started, "response", DiagnosticDNS, "读取响应失败，请检查连接稳定性", readErr), readErr
	}
	if len(raw) > maxResponseBytes {
		err = errors.New("response exceeds 2 MiB")
		return finishRequestFailure(result, started, "response", DiagnosticTooLarge, "响应超过 2 MiB 上限，请缩小接口返回内容", err), err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, summary := DiagnosticHTTP, fmt.Sprintf("服务返回 HTTP %d，请检查接口状态", response.StatusCode)
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code, summary = DiagnosticUnauthorized, "认证失败，请检查凭据或访问权限"
		}
		err = fmt.Errorf("HTTP %d", response.StatusCode)
		return finishRequestFailure(result, started, "http", code, summary, err), err
	}
	var document any
	if err = json.Unmarshal(raw, &document); err != nil {
		return finishRequestFailure(result, started, "decode", DiagnosticNonJSON, "响应不是有效 JSON，请检查接口或 Content-Type", err), err
	}
	result.Document = document
	result.Value, result.Secondary, err = evaluateCardInput(input, document)
	if err != nil {
		code := DiagnosticTypeMismatch
		if strings.Contains(strings.ToLower(err.Error()), "field") || strings.Contains(err.Error(), "字段") {
			code = DiagnosticPathMissing
		}
		summary := "取值结果类型不符合卡片要求，请选择数值字段或调整表达式"
		if code == DiagnosticPathMissing {
			summary = "JSON 路径不存在，请从响应结构中重新选择字段"
		}
		return finishRequestFailure(result, started, "evaluate", code, summary, err), err
	}
	result.ValueType = jsonValueType(result.Value)
	result.OK = true
	result.Diagnostic.DurationMS = elapsedMilliseconds(started, time.Now())
	return result, nil
}

func evaluateCardInput(input CardInput, document any) (any, any, error) {
	var value any
	var err error
	if input.Type == CardNumber {
		if extracted, extractErr := Extract(document, input.ValuePath); extractErr == nil {
			if text, ok := extracted.(string); ok {
				value = text
			}
		}
	}
	if value == nil {
		value, err = Evaluate(input.ValuePath, document)
	}
	var secondary any
	if err == nil && input.SecondaryPath != "" {
		secondary, err = Evaluate(input.SecondaryPath, document)
	}
	return value, secondary, err
}

func (m *Manager) runRegistryRequest(ctx context.Context, input CardInput, existingCardID string) (TestResult, error) {
	started := time.Now()
	var config registrymonitor.Config
	_ = json.Unmarshal(input.Config, &config)
	result := TestResult{Diagnostic: RequestDiagnostic{Code: DiagnosticOK, Stage: "complete", Summary: "请求成功", URL: redactRequestURL(config.Endpoint), AttemptedAt: m.now().UTC()}}
	images, err := m.registry.Test(ctx, existingCardID, config, input.RegistryPassword, input.PreserveRegistryPassword && input.RegistryPassword == "")
	if err != nil {
		if errors.Is(err, registryconnection.ErrNotFound) || errors.Is(err, registryconnection.ErrInvalidConnection) {
			return finishRequestFailure(result, started, "registry_auth", DiagnosticRegistryAuth, "Registry 凭据不可用，请重新输入密码或 Token", err), err
		}
		code := classifyNetworkError(err)
		return finishRequestFailure(result, started, "connect", code, actionableSummary(code), err), err
	}
	failures := 0
	authFailure := false
	for index, image := range images {
		if image.Error != "" {
			failures++
			if strings.Contains(image.Error, "401") || strings.Contains(image.Error, "403") || strings.Contains(strings.ToLower(image.Error), "token") {
				authFailure = true
			}
			images[index].Error = safeRegistryError(image.Error)
		}
	}
	result.Images = images
	if failures > 0 {
		code, summary := DiagnosticRegistryManifest, fmt.Sprintf("%d 个镜像查询失败，请检查镜像名称或访问权限", failures)
		if authFailure {
			code, summary = DiagnosticRegistryAuth, "Registry 认证失败，请检查用户名、密码或 Token"
		}
		err = errors.New(summary)
		return finishRequestFailure(result, started, "registry_manifest", code, summary, err), err
	}
	result.OK = true
	result.Diagnostic.DurationMS = elapsedMilliseconds(started, time.Now())
	return result, nil
}

func safeRegistryError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(message, "401") || strings.Contains(message, "403") || strings.Contains(lower, "token"):
		return "Registry 认证失败"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "Registry 请求超时"
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate"):
		return "Registry TLS 连接失败"
	case strings.Contains(message, "HTTP "):
		if start := strings.Index(message, "HTTP "); start >= 0 {
			status := strings.Fields(message[start:])
			if len(status) > 1 {
				return "Registry 返回 HTTP " + boundedText(status[1], 3)
			}
		}
	}
	return "Registry 查询失败"
}

func finishRequestFailure(result TestResult, started time.Time, stage, code, summary string, err error) TestResult {
	result.OK = false
	result.Diagnostic.Code = code
	result.Diagnostic.Stage = stage
	result.Diagnostic.Summary = boundedText(summary, 240)
	result.Diagnostic.DurationMS = elapsedMilliseconds(started, time.Now().UTC())
	return result
}

func classifyNetworkError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return DiagnosticTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return DiagnosticTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return DiagnosticDNS
	}
	var unknownAuthority x509.UnknownAuthorityError
	var recordHeader tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &recordHeader) || strings.Contains(strings.ToLower(err.Error()), "tls") || strings.Contains(strings.ToLower(err.Error()), "certificate") {
		return DiagnosticTLS
	}
	return DiagnosticDNS
}

func actionableSummary(code string) string {
	switch code {
	case DiagnosticTimeout:
		return "请求超时，请检查服务负载、网络或缩短响应时间"
	case DiagnosticTLS:
		return "TLS 握手失败，请检查证书、域名和协议"
	default:
		return "无法连接数据地址，请检查 DNS、端口和网络可达性"
	}
}

func redactRequestURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	for name := range query {
		query.Set(name, "[REDACTED]")
	}
	parsed.RawQuery = query.Encode()
	return boundedText(parsed.String(), 500)
}

func redactHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") || strings.Contains(lower, "session") || strings.Contains(lower, "api-key") {
			result[name] = "[REDACTED]"
		} else {
			result[name] = boundedText(value, 200)
		}
	}
	return result
}

func boundedText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func elapsedMilliseconds(start, end time.Time) int64 {
	value := end.Sub(start).Milliseconds()
	if value < 0 {
		return 0
	}
	return value
}

func jsonValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64, float32, int, int64, json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}
