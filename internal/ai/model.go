package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	llm "github.com/amit-timalsina/pi-llm-go"
	"github.com/amit-timalsina/pi-llm-go/providers/anthropic"
	"github.com/amit-timalsina/pi-llm-go/providers/openai"
	"github.com/amit-timalsina/pi-llm-go/providers/openai_responses"
)

type Protocol string

const (
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolOpenAIChat      Protocol = "openai_chat"
	ProtocolAnthropic       Protocol = "anthropic_messages"
)

type AuthMode string

const (
	AuthBearer AuthMode = "bearer"
	AuthAPIKey AuthMode = "x-api-key"
	AuthNone   AuthMode = "none"
)

type ModelProfile struct {
	ID                    string
	Name                  string
	Protocol              Protocol
	BaseURL               string
	Model                 string
	AuthMode              AuthMode
	APIKeyRef             string
	ExtraHeaderRefs       map[string]string
	ContextWindow         int
	MaxOutputTokens       int
	Permission            Permission
	AllowSensitiveReads   bool
	AutoApprove           bool
	DefaultRunTimeoutSec  int
	Disabled              bool
	DisableTransportRetry bool
	LastError             string
	LastErrorAt           *time.Time
	LastTestAt            *time.Time
}

type ModelMessage struct {
	Role       string      `json:"role"`
	Text       string      `json:"text,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ModelRequest struct {
	System      string
	Messages    []ModelMessage
	Tools       []ToolDefinition
	ToolChoice  string
	MaxTokens   int
	Temperature *float64
}

type ModelEvent struct {
	Type      string
	TextDelta string
	ToolCall  *ToolCall
	Usage     Usage
}

type Usage struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	TotalTokens   int64 `json:"total_tokens"`
	LatencyMillis int64 `json:"latency_millis"`
}

type ModelResponse struct {
	Message ModelMessage
	Usage   Usage
}

// ModelClient is the model seam used by the Agent Loop. Implementations stream
// normalized events through emit and return one complete assistant message.
type ModelClient interface {
	Complete(context.Context, ModelRequest, func(ModelEvent)) (ModelResponse, error)
}

type piClient struct {
	provider llm.LLM
	profile  ModelProfile
}

type SecretResolver func(reference string) (string, error)

func NewModelClient(profile ModelProfile, resolve SecretResolver) (ModelClient, error) {
	if profile.Disabled {
		return nil, errors.New("model profile is disabled")
	}
	if strings.TrimSpace(profile.Model) == "" {
		return nil, errors.New("model ID is required")
	}
	if err := ValidateEndpoint(profile.BaseURL); err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "", "x-api-key": ""}
	var key string
	var err error
	if profile.AuthMode != AuthNone {
		if profile.APIKeyRef == "" {
			return nil, errors.New("API key reference is required")
		}
		key, err = resolve(profile.APIKeyRef)
		if err != nil {
			return nil, fmt.Errorf("read model API key: %w", err)
		}
	}
	switch profile.AuthMode {
	case AuthBearer:
		headers["Authorization"] = "Bearer " + key
	case AuthAPIKey:
		headers["x-api-key"] = key
	case AuthNone:
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", profile.AuthMode)
	}
	for name, reference := range profile.ExtraHeaderRefs {
		value, err := resolve(reference)
		if err != nil {
			return nil, fmt.Errorf("read extra header %q: %w", name, err)
		}
		headers[name] = value
	}
	if err := ValidateExtraHeaders(headersWithoutManagedAuth(headers)); err != nil {
		return nil, err
	}
	client := SecureHTTPClient(headers)
	retry := llm.DefaultRetryPolicy()
	retry.MaxAttempts = 3
	if profile.DisableTransportRetry {
		retry.MaxAttempts = 1
	}
	const constructorKey = "scriptboard-transport-managed"
	var provider llm.LLM
	switch profile.Protocol {
	case ProtocolOpenAIChat:
		provider, err = openai.New(openai.Options{
			APIKey: constructorKey, BaseURL: strings.TrimRight(profile.BaseURL, "/"),
			HTTPClient: client, Retry: &retry,
		})
	case ProtocolOpenAIResponses:
		provider, err = openai_responses.New(openai_responses.Options{
			APIKey: constructorKey, BaseURL: strings.TrimRight(profile.BaseURL, "/"),
			HTTPClient: client, Retry: &retry,
		})
	case ProtocolAnthropic:
		provider, err = anthropic.New(anthropic.Options{
			APIKey: constructorKey, BaseURL: strings.TrimRight(profile.BaseURL, "/"),
			HTTPClient: client, Retry: &retry,
		})
	default:
		return nil, fmt.Errorf("unsupported model protocol %q", profile.Protocol)
	}
	if err != nil {
		return nil, err
	}
	return &piClient{provider: provider, profile: profile}, nil
}

func headersWithoutManagedAuth(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		switch strings.ToLower(name) {
		case "authorization", "x-api-key":
			continue
		default:
			result[name] = value
		}
	}
	return result
}

func (c *piClient) Complete(ctx context.Context, request ModelRequest, emit func(ModelEvent)) (ModelResponse, error) {
	wire, err := toPIRequest(c.profile, request)
	if err != nil {
		return ModelResponse{}, err
	}
	started := time.Now()
	var text strings.Builder
	calls := make(map[int]ToolCall)
	var response ModelResponse
	for event, streamErr := range c.provider.Stream(ctx, wire) {
		if streamErr != nil {
			return ModelResponse{}, streamErr
		}
		switch value := event.(type) {
		case llm.EventTextDelta:
			text.WriteString(value.Delta)
			if emit != nil {
				emit(ModelEvent{Type: "text_delta", TextDelta: value.Delta})
			}
		case llm.EventToolCallStart:
			calls[value.BlockIndex] = ToolCall{ID: value.ID, Name: value.Name}
		case llm.EventToolCallEnd:
			call := calls[value.BlockIndex]
			call.Arguments = append(json.RawMessage(nil), value.Arguments...)
			calls[value.BlockIndex] = call
			if emit != nil {
				copy := call
				emit(ModelEvent{Type: "tool_call", ToolCall: &copy})
			}
		case llm.EventMessageEnd:
			response.Usage = Usage{
				InputTokens: int64(value.Usage.InputTokens), OutputTokens: int64(value.Usage.OutputTokens),
				TotalTokens: int64(value.Usage.TotalTokens),
			}
			if emit != nil {
				emit(ModelEvent{Type: "message_end", Usage: response.Usage})
			}
		}
	}
	response.Message = ModelMessage{Role: "assistant", Text: text.String()}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		response.Message.ToolCalls = append(response.Message.ToolCalls, calls[index])
	}
	response.Usage.LatencyMillis = time.Since(started).Milliseconds()
	return response, nil
}

func toPIRequest(profile ModelProfile, request ModelRequest) (llm.Request, error) {
	messages := make([]llm.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		wire := llm.Message{}
		switch message.Role {
		case "user":
			wire.Role = llm.RoleUser
		case "assistant":
			wire.Role = llm.RoleAssistant
		case "tool":
			wire.Role = llm.RoleTool
		default:
			return llm.Request{}, fmt.Errorf("unsupported message role %q", message.Role)
		}
		if message.Text != "" {
			wire.Content = append(wire.Content, llm.TextBlock{Text: message.Text})
		}
		for _, call := range message.ToolCalls {
			wire.Content = append(wire.Content, llm.ToolCallBlock{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
		}
		if message.ToolResult != nil {
			wire.Content = append(wire.Content, llm.ToolResultBlock{
				ToolCallID: message.ToolResult.CallID, Content: message.ToolResult.Content, IsError: message.ToolResult.IsError,
			})
		}
		messages = append(messages, wire)
	}
	tools := make([]llm.Tool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, llm.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Strict: true})
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = profile.MaxOutputTokens
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	result := llm.Request{
		Model: profile.Model, System: request.System, Messages: messages, Tools: tools,
		MaxTokens: maxTokens, Temperature: request.Temperature,
	}
	switch request.ToolChoice {
	case "", "auto":
		result.ToolChoice = &llm.ToolChoice{Type: llm.ToolChoiceAuto}
	case "none":
		result.ToolChoice = &llm.ToolChoice{Type: llm.ToolChoiceNone}
	case "required":
		result.ToolChoice = &llm.ToolChoice{Type: llm.ToolChoiceAny}
	default:
		result.ToolChoice = &llm.ToolChoice{Type: llm.ToolChoiceTool, Name: request.ToolChoice}
	}
	return result, nil
}
