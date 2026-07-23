package ai

import (
	"context"
	"encoding/json"
	"testing"
)

type scriptedClient struct {
	responses []ModelResponse
	requests  []ModelRequest
}

func (c *scriptedClient) Complete(_ context.Context, request ModelRequest, emit func(ModelEvent)) (ModelResponse, error) {
	c.requests = append(c.requests, request)
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

type recordingExecutor struct {
	validated []Action
	executed  []Action
}

func (e *recordingExecutor) Validate(_ context.Context, action Action) error {
	e.validated = append(e.validated, action)
	return nil
}

func (e *recordingExecutor) Execute(_ context.Context, action Action) (json.RawMessage, error) {
	e.executed = append(e.executed, action)
	return json.RawMessage(`{"ok":true}`), nil
}

func createAgentConversation(t *testing.T, store *Store, autoApprove bool, permission Permission) Conversation {
	t.Helper()
	if err := store.SaveProfile(context.Background(), ModelProfile{
		ID: "p", Name: "P", Protocol: ProtocolOpenAIChat, BaseURL: "https://example.com/v1",
		Model: "m", AuthMode: AuthNone, Permission: permission, AutoApprove: autoApprove,
	}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), "p", permission, "hello")
	if err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestAgentExecutesQueryImmediatelyAndFeedsResultBack(t *testing.T) {
	store := openTestStore(t)
	conversation := createAgentConversation(t, store, false, Permission{Query: true})
	client := &scriptedClient{responses: []ModelResponse{
		{Message: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "q1", Name: "list_files", Arguments: json.RawMessage(`{"path":""}`),
		}}}},
		{Message: ModelMessage{Role: "assistant", Text: "Found one file."}},
		{Message: ModelMessage{Role: "assistant", Text: "Files"}},
	}}
	registry := NewToolRegistry()
	queryCalls := 0
	if err := registry.RegisterQuery(ToolDefinition{
		Name: "list_files", Description: "List files", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, json.RawMessage, CallContext) (json.RawMessage, error) {
		queryCalls++
		return json.RawMessage(`{"items":["a.sh"]}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := NewCoordinator(store, registry, &recordingExecutor{}, func(ModelProfile) (ModelClient, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Send(context.Background(), conversation.ID, "list files", nil); err != nil {
		t.Fatal(err)
	}
	agent.Wait()
	if queryCalls != 1 || len(client.requests) != 3 {
		t.Fatalf("query calls=%d model requests=%d", queryCalls, len(client.requests))
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if last.ToolResult == nil || last.ToolResult.Content != `{"items":["a.sh"]}` {
		t.Fatalf("tool result = %#v", last)
	}
}

func TestAgentFreezesAndAutoExecutesPreparedActions(t *testing.T) {
	store := openTestStore(t)
	conversation := createAgentConversation(t, store, true, Permission{Modify: true})
	client := &scriptedClient{responses: []ModelResponse{
		{Message: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "a1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.sh","content":"echo ok"}`),
		}}}},
		{Message: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "s1", Name: SubmitBatchToolName, Arguments: json.RawMessage(`{"action_ids":["a1"]}`),
		}}}},
		{Message: ModelMessage{Role: "assistant", Text: "Done."}},
		{Message: ModelMessage{Role: "assistant", Text: "Create script"}},
	}}
	registry := NewToolRegistry()
	if err := registry.RegisterAction(ToolDefinition{
		Name: "write_file", Description: "Write a file", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, RiskModify, func(_ context.Context, arguments json.RawMessage, _ CallContext) (Action, error) {
		return Action{Kind: "file.write", Risk: RiskModify, Summary: "write a.sh", Input: arguments}, nil
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	agent, err := NewCoordinator(store, registry, executor, func(ModelProfile) (ModelClient, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	agent.Wait()
	result, err := agent.Send(context.Background(), conversation.ID, "create script", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID == "" || len(executor.executed) != 1 || executor.executed[0].Kind != "file.write" {
		t.Fatalf("result=%#v executed=%#v", result, executor.executed)
	}
	batch, err := store.GetBatch(context.Background(), result.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != BatchCompleted {
		t.Fatalf("batch status = %q", batch.Status)
	}
}

func TestAgentLeavesManualBatchFrozenUntilApproval(t *testing.T) {
	store := openTestStore(t)
	conversation := createAgentConversation(t, store, false, Permission{Execute: true})
	client := &scriptedClient{responses: []ModelResponse{
		{Message: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "a1", Name: "run_script", Arguments: json.RawMessage(`{"path":"a.sh"}`),
		}}}},
		{Message: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "s1", Name: SubmitBatchToolName, Arguments: json.RawMessage(`{"action_ids":["a1"]}`),
		}}}},
		{Message: ModelMessage{Role: "assistant", Text: "Waiting for approval."}},
		{Message: ModelMessage{Role: "assistant", Text: "Run script"}},
	}}
	registry := NewToolRegistry()
	if err := registry.RegisterAction(ToolDefinition{
		Name: "run_script", Description: "Run", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, RiskExecute, func(_ context.Context, arguments json.RawMessage, _ CallContext) (Action, error) {
		return Action{Kind: "run.start", Risk: RiskExecute, Summary: "run a.sh", Input: arguments}, nil
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	agent, err := NewCoordinator(store, registry, executor, func(ModelProfile) (ModelClient, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.Send(context.Background(), conversation.ID, "run it", nil)
	if err != nil {
		t.Fatal(err)
	}
	agent.Wait()
	if len(executor.executed) != 0 {
		t.Fatalf("manual batch executed before approval: %#v", executor.executed)
	}
	batch, err := store.GetBatch(context.Background(), result.BatchID)
	if err != nil || batch.Status != BatchPending {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if err := agent.ApproveBatch(context.Background(), result.BatchID); err != nil {
		t.Fatal(err)
	}
	if len(executor.executed) != 1 {
		t.Fatalf("executed=%#v", executor.executed)
	}
}
