package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/toolbroker"
)

func TestAssistantStateToolApprovalFreezesTargetAndNeverReplaysChangedAction(t *testing.T) {
	root := t.TempDir()
	application, err := Open(Config{StateRoot: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })

	actor := assistant.Actor{UserID: "administrator", Username: "admin"}
	model, err := application.assistant.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-only", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.assistant.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	autoApproval := false
	conversation, err := application.assistant.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "start it", AutoApproval: &autoApproval})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.assistant.BeginAssistantReply(context.Background(), actor, conversation.ID); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(root, "fixture.ps1")
	if err := os.WriteFile(scriptPath, []byte("Write-Output 'fixture'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := application.db.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id, sort_order, created_at, group_id, locked, updated_at)
		VALUES ('quick-fixture', 'Fixture quick run', ?, ?, '', 30, NULL, 1, ?, NULL, 0, ?)`, scriptPath, scriptPath, now, now); err != nil {
		t.Fatal(err)
	}

	invocation := toolbroker.Invocation{
		Binding: toolbroker.SessionBinding{RuntimeID: "fixture", UserID: actor.UserID, ConversationID: conversation.ID, ExpiresAt: time.Now().Add(time.Minute)},
		Request: toolbroker.Request{Version: 1, ToolCallID: "pi-call-1", Tool: "start_quick_run", Parameters: json.RawMessage(`{"id":"quick-fixture"}`)},
	}
	requested := application.assistantTools.Invoke(context.Background(), invocation)
	if requested.Status != toolbroker.StatusApproval || requested.Approval == nil {
		t.Fatalf("initial tool response = %#v, want approval", requested)
	}
	if _, err := application.assistantRuntime.ResolveApproval(context.Background(), actor, conversation.ID, requested.Approval.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := application.db.Exec(`UPDATE quick_runs SET name = 'Changed after approval', updated_at = ? WHERE id = 'quick-fixture'`, now+1); err != nil {
		t.Fatal(err)
	}
	invocation.Request.ApprovalID = requested.Approval.ID
	invocation.Request.Decision = "approve"
	result := application.assistantTools.Invoke(context.Background(), invocation)
	if result.ErrorCode != "tool_target_changed" {
		t.Fatalf("changed target response = %#v, want tool_target_changed", result)
	}
	rejectedInvocation := toolbroker.Invocation{
		Binding: invocation.Binding,
		Request: toolbroker.Request{Version: 1, ToolCallID: "pi-call-2", Tool: "start_quick_run", Parameters: json.RawMessage(`{"id":"quick-fixture"}`)},
	}
	rejectedRequest := application.assistantTools.Invoke(context.Background(), rejectedInvocation)
	if rejectedRequest.Status != toolbroker.StatusApproval || rejectedRequest.Approval == nil {
		t.Fatalf("rejected-call request = %#v, want approval", rejectedRequest)
	}
	if _, err := application.assistantRuntime.ResolveApproval(context.Background(), actor, conversation.ID, rejectedRequest.Approval.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := application.db.Exec(`UPDATE quick_runs SET name = 'Changed after rejection', updated_at = ? WHERE id = 'quick-fixture'`, now+2); err != nil {
		t.Fatal(err)
	}
	rejectedInvocation.Request.ApprovalID = rejectedRequest.Approval.ID
	rejectedInvocation.Request.Decision = "reject"
	rejectedResult := application.assistantTools.Invoke(context.Background(), rejectedInvocation)
	if rejectedResult.Status != toolbroker.StatusRejected || rejectedResult.ErrorCode != "approval_rejected" {
		t.Fatalf("rejected target response = %#v, want approval_rejected", rejectedResult)
	}
	calls, err := application.assistant.ToolCalls(context.Background(), actor, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	callResults := make(map[string]string, len(calls))
	for _, call := range calls {
		callResults[call.Status] = call.ErrorCode
	}
	if len(calls) != 2 || callResults["error"] != "tool_target_changed" || callResults["rejected"] != "approval_rejected" {
		t.Fatalf("tool calls = %#v", calls)
	}
	var runs int
	if err := application.db.QueryRow("SELECT COUNT(*) FROM runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs=%d err=%v; changed action was executed", runs, err)
	}
	rows, err := application.db.Query(`SELECT action, target, result FROM audit_events WHERE action LIKE 'assistant_tool_%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var audit strings.Builder
	for rows.Next() {
		var action, target, result string
		if err := rows.Scan(&action, &target, &result); err != nil {
			t.Fatal(err)
		}
		audit.WriteString(action + " " + target + " " + result + "\n")
	}
	if !strings.Contains(audit.String(), "assistant_tool_requested") || !strings.Contains(audit.String(), "target_changed") {
		t.Fatalf("assistant audit = %s", audit.String())
	}
	if strings.Contains(audit.String(), scriptPath) || strings.Contains(audit.String(), "Write-Output") {
		t.Fatalf("assistant audit leaked a private path or body: %s", audit.String())
	}
}

func TestAssistantToolReauthorizesCurrentRole(t *testing.T) {
	root := t.TempDir()
	application, err := Open(Config{StateRoot: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	actor := assistant.Actor{UserID: "administrator", Username: "admin"}
	model, err := application.assistant.SaveModel(context.Background(), actor, "", assistant.ModelInput{Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture", MakeDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.assistant.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	conversation, err := application.assistant.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.assistant.BeginAssistantReply(context.Background(), actor, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.db.Exec(`UPDATE users SET role = 'viewer', auth_version = auth_version + 1 WHERE id = 'administrator'`); err != nil {
		t.Fatal(err)
	}
	response := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{
		Binding: toolbroker.SessionBinding{RuntimeID: "fixture", UserID: actor.UserID, ConversationID: conversation.ID, ExpiresAt: time.Now().Add(time.Minute)},
		Request: toolbroker.Request{Version: 1, ToolCallID: "pi-call-role", Tool: "start_quick_run", Parameters: json.RawMessage(`{"id":"missing"}`)},
	})
	if response.Status != toolbroker.StatusForbidden || response.ErrorCode != "tool_forbidden" {
		t.Fatalf("viewer state tool response = %#v", response)
	}
}
