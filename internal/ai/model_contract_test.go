package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelProtocolsNormalizeStreamingTextAndUsage(t *testing.T) {
	tests := []struct {
		name, path, payload string
		protocol            Protocol
	}{
		{
			name: "OpenAI Chat Completions", path: "/chat/completions", protocol: ProtocolOpenAIChat,
			payload: `data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}

data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}

data: {"id":"c1","model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}

data: [DONE]

`,
		},
		{
			name: "OpenAI Responses", path: "/responses", protocol: ProtocolOpenAIResponses,
			payload: `event: response.created
data: {"type":"response.created","response":{"id":"r1","model":"m","status":"in_progress"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg1","status":"in_progress","role":"assistant","content":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"Hello"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r1","model":"m","status":"completed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}

`,
		},
		{
			name: "Anthropic Messages", path: "/v1/messages", protocol: ProtocolAnthropic,
			payload: `event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"m","usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Errorf("path = %q, want %q", request.URL.Path, test.path)
				}
				if request.Method != http.MethodPost {
					t.Errorf("method = %q", request.Method)
				}
				response.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(response, test.payload)
			}))
			defer server.Close()
			client, err := NewModelClient(ModelProfile{
				Name: "test", Protocol: test.protocol, BaseURL: server.URL, Model: "m", AuthMode: AuthNone,
				ContextWindow: 128000, MaxOutputTokens: 64,
			}, func(string) (string, error) { return "", nil })
			if err != nil {
				t.Fatal(err)
			}
			var deltas strings.Builder
			result, err := client.Complete(context.Background(), ModelRequest{
				Messages: []ModelMessage{{Role: "user", Text: "hello"}}, MaxTokens: 64,
			}, func(event ModelEvent) {
				deltas.WriteString(event.TextDelta)
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Message.Text != "Hello" || deltas.String() != "Hello" {
				t.Fatalf("message=%q deltas=%q", result.Message.Text, deltas.String())
			}
			if result.Usage.TotalTokens != 5 {
				t.Fatalf("usage = %#v", result.Usage)
			}
		})
	}
}

func TestOpenAIChatNormalizesFragmentedToolArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(response, `data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}

data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}

data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.sh\"}"}}]},"finish_reason":null}]}

data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`)
	}))
	defer server.Close()
	client, err := NewModelClient(ModelProfile{
		Protocol: ProtocolOpenAIChat, BaseURL: server.URL, Model: "m", AuthMode: AuthNone, MaxOutputTokens: 64,
	}, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), ModelRequest{
		Messages: []ModelMessage{{Role: "user", Text: "read"}}, Tools: []ToolDefinition{{
			Name: "read_file", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "read_file" ||
		string(result.Message.ToolCalls[0].Arguments) != `{"path":"a.sh"}` {
		t.Fatalf("tool calls = %#v", result.Message.ToolCalls)
	}
}
