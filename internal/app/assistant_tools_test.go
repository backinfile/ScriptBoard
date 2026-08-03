package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/toolbroker"
	"scriptboard/internal/buildinfo"
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
		Request: toolbroker.Request{Version: 1, Capability: "must-not-be-persisted", ToolCallID: "pi-call-1", Tool: "start_quick_run", Parameters: json.RawMessage(`{"id":"quick-fixture"}`)},
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
	replayInvocation := invocation
	replayInvocation.Request.ApprovalID = ""
	replayInvocation.Request.Decision = ""
	replayResult := application.assistantTools.Invoke(context.Background(), replayInvocation)
	if replayResult.ErrorCode != "tool_replay" {
		t.Fatalf("replayed response = %#v, want tool_replay", replayResult)
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
		if !json.Valid([]byte(call.RequestJSON)) || !json.Valid([]byte(call.ResponseJSON)) {
			t.Fatalf("tool JSON is invalid: request=%q response=%q", call.RequestJSON, call.ResponseJSON)
		}
		if strings.Contains(call.RequestJSON, "must-not-be-persisted") || !strings.Contains(call.RequestJSON, `"tool": "start_quick_run"`) || !strings.Contains(call.ResponseJSON, `"errorCode"`) {
			t.Fatalf("tool JSON omitted safe fields or retained capability: request=%s response=%s", call.RequestJSON, call.ResponseJSON)
		}
		if call.Status == "error" && !strings.Contains(call.ResponseJSON, `"errorCode": "tool_target_changed"`) {
			t.Fatalf("tool replay replaced the original response JSON: %s", call.ResponseJSON)
		}
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

func TestAssistantUIActionCatalogAndExecutorReuseWebValidation(t *testing.T) {
	root := t.TempDir()
	application, err := Open(Config{StateRoot: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	specs := application.assistantUIActions()
	if len(specs) != 84 {
		t.Fatalf("UI action catalog has %d entries, want one for each of the 84 POST routes", len(specs))
	}
	keys, paths := make(map[string]bool, len(specs)), make(map[string]bool, len(specs))
	for _, spec := range specs {
		if keys[spec.Key] || paths[spec.Path] {
			t.Fatalf("duplicate UI action contract: key=%q path=%q", spec.Key, spec.Path)
		}
		keys[spec.Key], paths[spec.Path] = true, true
	}
	for _, expected := range []string{"websites.create", "files.save_text", "quick_runs.create_from_source", "schedules.preview_existing", "ai.conversation.resolve_approval"} {
		if !keys[expected] {
			t.Fatalf("UI action catalog is missing %q", expected)
		}
	}
	actor := assistant.Actor{UserID: "administrator", Username: "admin"}
	model, err := application.assistant.SaveModel(context.Background(), actor, "", assistant.ModelInput{Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture", MakeDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.assistant.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	autoApproval := true
	conversation, err := application.assistant.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "manage it", AutoApproval: &autoApproval})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.assistant.BeginAssistantReply(context.Background(), actor, conversation.ID); err != nil {
		t.Fatal(err)
	}
	binding := toolbroker.SessionBinding{RuntimeID: "fixture", UserID: actor.UserID, ConversationID: conversation.ID, ExpiresAt: time.Now().Add(time.Minute)}
	listed := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{Version: 1, ToolCallID: "ui-list", Tool: "list_ui_actions", Parameters: json.RawMessage(`{"domain":"quick_runs"}`)}})
	if listed.Status != toolbroker.StatusSuccess {
		t.Fatalf("list UI actions = %#v", listed)
	}
	payload, _ := json.Marshal(listed.Content)
	for _, expected := range []string{"quick_run_groups.create", "quick_runs.start", "requiresApproval"} {
		if !strings.Contains(string(payload), expected) {
			t.Fatalf("action catalog is missing %q: %s", expected, payload)
		}
	}
	performed := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{Version: 1, ToolCallID: "ui-perform", Tool: "perform_ui_action", Parameters: json.RawMessage(`{"action":"quick_run_groups.create","form":{"name":"Created by Pi"}}`)}})
	if performed.Status != toolbroker.StatusSuccess {
		t.Fatalf("perform UI action = %#v", performed)
	}
	var count int
	if err := application.db.QueryRow(`SELECT COUNT(*) FROM quick_run_groups WHERE name = 'Created by Pi'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("created group count=%d err=%v", count, err)
	}
}

func TestAssistantUIActionResultsAreComposableAndFailuresAreActionable(t *testing.T) {
	root := t.TempDir()
	application, err := Open(Config{StateRoot: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	actor := assistant.Actor{UserID: "administrator", Username: "admin"}
	model, err := application.assistant.SaveModel(context.Background(), actor, "", assistant.ModelInput{
		Name: "Fixture", Provider: assistant.ProviderOpenAICompatible, Model: "fixture",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture", MakeDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := application.assistant.UpdateSettings(context.Background(), actor, assistant.SettingsInput{Enabled: true, MaxActiveConversations: 1}); err != nil {
		t.Fatal(err)
	}
	autoApproval := true
	conversation, err := application.assistant.CreateConversation(context.Background(), actor, assistant.ConversationInput{ModelID: model.ID, InitialMessage: "manage it", AutoApproval: &autoApproval})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.assistant.BeginAssistantReply(context.Background(), actor, conversation.ID); err != nil {
		t.Fatal(err)
	}
	binding := toolbroker.SessionBinding{RuntimeID: "fixture", UserID: actor.UserID, ConversationID: conversation.ID, ExpiresAt: time.Now().Add(time.Minute)}

	missingSourceLog := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{
		Version: 1, ToolCallID: "source-log-missing", Tool: "search_source_log",
		Parameters: json.RawMessage(`{"id":"missing-application","query":"error","limit":5}`),
	}})
	if missingSourceLog.Status != toolbroker.StatusError || missingSourceLog.ErrorCode != "tool_target_not_found" {
		t.Fatalf("missing source log action = %#v", missingSourceLog)
	}

	created := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{
		Version: 1, ToolCallID: "ui-create-group-result", Tool: "perform_ui_action",
		Parameters: json.RawMessage(`{"action":"quick_run_groups.create","form":{"name":"Composable group"}}`),
	}})
	if created.Status != toolbroker.StatusSuccess {
		t.Fatalf("create group action = %#v", created)
	}
	createdJSON, _ := json.Marshal(created.Content)
	if !strings.Contains(string(createdJSON), `"resourceId"`) {
		t.Fatalf("create group action did not return its stable ID: %s", createdJSON)
	}

	failed := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{
		Version: 1, ToolCallID: "ui-delete-missing-result", Tool: "perform_ui_action",
		Parameters: json.RawMessage(`{"action":"files.delete","form":{"path":"Z:\\\\missing\\\\ai-e2e","confirm_references":"yes"}}`),
	}})
	if failed.Status != toolbroker.StatusError {
		t.Fatalf("delete missing action = %#v", failed)
	}
	if failed.ErrorCode == "tool_failed" || !strings.Contains(failed.Summary, "files.delete") {
		t.Fatalf("delete missing action did not preserve actionable failure details: %#v", failed)
	}

	invokeAction := func(callID string, parameters map[string]any) toolbroker.Response {
		t.Helper()
		encoded, err := json.Marshal(parameters)
		if err != nil {
			t.Fatal(err)
		}
		return application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: binding, Request: toolbroker.Request{
			Version: 1, ToolCallID: callID, Tool: "perform_ui_action", Parameters: encoded,
		}})
	}
	installFakeAssistantRuntime(t, application.stateRoot)
	providerTest := invokeAction("ui-test-llm", map[string]any{
		"action": "ai.test_llm", "pathParameters": map[string]string{"id": model.ID},
	})
	if providerTest.Status != toolbroker.StatusSuccess {
		t.Fatalf("test LLM action = %#v", providerTest)
	}

	workingDirectory := filepath.Join(root, "assistant-actions")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	language := platformScriptLanguages()[0]
	source := "printf 'assistant action ok\\n'\n"
	if language.ID == "powershell" {
		source = "Write-Output 'assistant action ok'\n"
	} else if language.ID == "batch" {
		source = "@echo assistant action ok\r\n"
	} else if language.ID == "python" {
		source = "print('assistant action ok')\n"
	}
	quickCreated := invokeAction("ui-create-source", map[string]any{
		"action": "quick_runs.create_from_source",
		"form": map[string]any{
			"language": language.ID, "source": source, "working_directory": workingDirectory,
			"file_name": "assistant-created", "name": "Assistant created", "timeout_seconds": 30,
		},
	})
	if quickCreated.Status != toolbroker.StatusSuccess {
		t.Fatalf("create Quick Run from source = %#v", quickCreated)
	}
	quickPayload, _ := json.Marshal(quickCreated.Content)
	if !strings.Contains(string(quickPayload), `"resourceId"`) {
		t.Fatalf("create source result = %s", quickPayload)
	}

	oneTime := invokeAction("ui-one-time-source", map[string]any{
		"action": "quick_runs.one_time",
		"form": map[string]any{
			"language": language.ID, "source": source, "working_directory": workingDirectory, "timeout_seconds": 30,
		},
	})
	if oneTime.Status != toolbroker.StatusSuccess {
		t.Fatalf("one-time source action = %#v", oneTime)
	}
	oneTimePayload, _ := json.Marshal(oneTime.Content)
	if !strings.Contains(string(oneTimePayload), `"resourceId"`) {
		t.Fatalf("one-time source result = %s", oneTimePayload)
	}

	var quickID string
	if err := application.db.QueryRow(`SELECT id FROM quick_runs WHERE name = 'Assistant created'`).Scan(&quickID); err != nil {
		t.Fatal(err)
	}
	locked := invokeAction("ui-lock-true", map[string]any{"action": "quick_runs.lock", "pathParameters": map[string]string{"id": quickID}, "form": map[string]any{"locked": true}})
	if locked.Status != toolbroker.StatusSuccess {
		t.Fatalf("lock true action = %#v", locked)
	}
	var lockValue int
	if err := application.db.QueryRow(`SELECT locked FROM quick_runs WHERE id = ?`, quickID).Scan(&lockValue); err != nil || lockValue != 1 {
		t.Fatalf("locked=%d err=%v", lockValue, err)
	}
	unlocked := invokeAction("ui-lock-false", map[string]any{"action": "quick_runs.lock", "pathParameters": map[string]string{"id": quickID}, "form": map[string]any{"locked": false}})
	if unlocked.Status != toolbroker.StatusSuccess {
		t.Fatalf("lock false action = %#v", unlocked)
	}
	if err := application.db.QueryRow(`SELECT locked FROM quick_runs WHERE id = ?`, quickID).Scan(&lockValue); err != nil || lockValue != 0 {
		t.Fatalf("unlocked=%d err=%v", lockValue, err)
	}

	scriptPath := filepath.Join(workingDirectory, "assistant-created"+language.Extension)
	scheduleCreated := invokeAction("ui-create-schedule-result", map[string]any{
		"action": "schedules.create",
		"form": map[string]any{
			"name": "Assistant schedule", "script": scriptPath, "expression": "0 2 * * *",
			"timeout_seconds": 30, "disallow_overlap": false,
		},
	})
	if scheduleCreated.Status != toolbroker.StatusSuccess {
		t.Fatalf("create schedule action = %#v", scheduleCreated)
	}
	var scheduleID string
	if err := application.db.QueryRow(`SELECT id FROM schedules WHERE name = 'Assistant schedule'`).Scan(&scheduleID); err != nil {
		t.Fatal(err)
	}
	for index, enabled := range []bool{false, true} {
		response := invokeAction("ui-schedule-toggle-"+strconv.Itoa(index), map[string]any{
			"action": "schedules.toggle", "pathParameters": map[string]string{"id": scheduleID}, "form": map[string]any{"enabled": enabled},
		})
		if response.Status != toolbroker.StatusSuccess {
			t.Fatalf("toggle schedule %t = %#v", enabled, response)
		}
		var actual int
		if err := application.db.QueryRow(`SELECT enabled FROM schedules WHERE id = ?`, scheduleID).Scan(&actual); err != nil || actual != map[bool]int{false: 0, true: 1}[enabled] {
			t.Fatalf("schedule enabled=%d after %t, err=%v", actual, enabled, err)
		}
	}

	settingsSaved := invokeAction("ui-save-ai-defaults", map[string]any{
		"action": "ai.save_defaults", "form": map[string]any{"enabled": true, "max_active_conversations": 1, "default_auto_approval": true},
	})
	if settingsSaved.Status != toolbroker.StatusSuccess {
		t.Fatalf("save AI defaults action = %#v", settingsSaved)
	}

	nginxPath := filepath.Join(root, "assistant-nginx.conf")
	if err := os.WriteFile(nginxPath, []byte("http { server { listen 8080; server_name assistant-import.local; } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanned := invokeAction("ui-nginx-scan-result", map[string]any{
		"action": "websites.nginx_scan", "form": map[string]any{"config_path": nginxPath},
	})
	if scanned.Status != toolbroker.StatusSuccess {
		t.Fatalf("Nginx scan action = %#v", scanned)
	}
	scanJSON, _ := json.Marshal(scanned.Content)
	var scanPayload struct {
		Candidates []struct {
			Digest string `json:"digest"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(scanJSON, &scanPayload); err != nil || len(scanPayload.Candidates) != 1 || scanPayload.Candidates[0].Digest == "" {
		t.Fatalf("Nginx scan did not return a composable digest: %s, %v", scanJSON, err)
	}
	imported := invokeAction("ui-nginx-import-result", map[string]any{
		"action": "websites.nginx_import", "form": map[string]any{
			"config_path": nginxPath, "digest": []any{scanPayload.Candidates[0].Digest},
		},
	})
	if imported.Status != toolbroker.StatusSuccess {
		t.Fatalf("Nginx import action = %#v", imported)
	}
	importJSON, _ := json.Marshal(imported.Content)
	if !strings.Contains(string(importJSON), `"importedCount":1`) {
		t.Fatalf("Nginx import result = %s", importJSON)
	}

	fileWorkingDirectory := filepath.Join(root, "file-actions")
	if err := os.MkdirAll(fileWorkingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	textPath := filepath.Join(fileWorkingDirectory, "managed.txt")
	if err := os.WriteFile(textPath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := invokeAction("ui-save-no-digest", map[string]any{
		"action": "files.save_text", "pathParameters": map[string]string{"path": textPath}, "form": map[string]any{"content": "after\n"},
	})
	if saved.Status != toolbroker.StatusSuccess {
		t.Fatalf("save text without digest = %#v", saved)
	}
	if content, err := os.ReadFile(textPath); err != nil || string(content) != "after\n" {
		t.Fatalf("saved content=%q err=%v", content, err)
	}

	deletePath := filepath.Join(fileWorkingDirectory, "delete-me")
	if err := os.MkdirAll(deletePath, 0o700); err != nil {
		t.Fatal(err)
	}
	deleted := invokeAction("ui-delete-existing", map[string]any{
		"action": "files.delete", "form": map[string]any{"path": deletePath, "confirm_references": true},
	})
	if deleted.Status != toolbroker.StatusSuccess {
		t.Fatalf("delete existing directory = %#v", deleted)
	}
	deletedJSON, _ := json.Marshal(deleted.Content)
	if !strings.Contains(string(deletedJSON), `"resourceId"`) {
		t.Fatalf("delete action did not return its trash entry ID: %s", deletedJSON)
	}
	var deletedResult struct {
		ResourceID string `json:"resourceId"`
	}
	if err := json.Unmarshal(deletedJSON, &deletedResult); err != nil || deletedResult.ResourceID == "" {
		t.Fatalf("decode delete result: %s, %v", deletedJSON, err)
	}
	if _, err := os.Stat(deletePath); !os.IsNotExist(err) {
		t.Fatalf("deleted directory still exists: %v", err)
	}
	restored := invokeAction("ui-restore-trash-result", map[string]any{
		"action": "files.restore_trash", "form": map[string]any{"id": deletedResult.ResourceID},
	})
	if restored.Status != toolbroker.StatusSuccess {
		t.Fatalf("restore trash action = %#v", restored)
	}
	if _, err := os.Stat(deletePath); err != nil {
		t.Fatalf("restored directory is unavailable: %v", err)
	}
	deletedAgain := invokeAction("ui-delete-for-purge", map[string]any{
		"action": "files.delete", "form": map[string]any{"path": deletePath, "confirm_references": true},
	})
	deletedAgainJSON, _ := json.Marshal(deletedAgain.Content)
	if deletedAgain.Status != toolbroker.StatusSuccess || json.Unmarshal(deletedAgainJSON, &deletedResult) != nil || deletedResult.ResourceID == "" {
		t.Fatalf("second delete action = %#v", deletedAgain)
	}
	purged := invokeAction("ui-purge-trash-result", map[string]any{
		"action": "files.purge_trash", "form": map[string]any{"id": deletedResult.ResourceID, "confirm": true},
	})
	if purged.Status != toolbroker.StatusSuccess {
		t.Fatalf("purge trash action = %#v", purged)
	}

	unavailable := invokeAction("ui-update-check-development", map[string]any{"action": "updates.check"})
	if !buildinfo.Current().ValidRelease() && unavailable.ErrorCode != "tool_unavailable" {
		t.Fatalf("development update action = %#v, want tool_unavailable", unavailable)
	}

	strictConversation, err := application.assistant.CreateConversation(context.Background(), actor, assistant.ConversationInput{
		ModelID: model.ID, InitialMessage: "失败不重试、不替代。", AutoApproval: &autoApproval,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.assistant.BeginAssistantReply(context.Background(), actor, strictConversation.ID); err != nil {
		t.Fatal(err)
	}
	strictBinding := toolbroker.SessionBinding{RuntimeID: "fixture", UserID: actor.UserID, ConversationID: strictConversation.ID, ExpiresAt: time.Now().Add(time.Minute)}
	strictFailure := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: strictBinding, Request: toolbroker.Request{
		Version: 1, ToolCallID: "strict-failure", Tool: "search_source_log",
		Parameters: json.RawMessage(`{"id":"missing-application","query":"error","limit":5}`),
	}})
	if strictFailure.Status != toolbroker.StatusError {
		t.Fatalf("strict first failure = %#v", strictFailure)
	}
	blockedRecovery := application.assistantTools.Invoke(context.Background(), toolbroker.Invocation{Binding: strictBinding, Request: toolbroker.Request{
		Version: 1, ToolCallID: "strict-substitute", Tool: "get_host_status", Parameters: json.RawMessage(`{}`),
	}})
	if blockedRecovery.ErrorCode != "tool_recovery_blocked" {
		t.Fatalf("strict substitute = %#v, want tool_recovery_blocked", blockedRecovery)
	}
}

func TestAssistantUIActionBooleanModesMatchBrowserContracts(t *testing.T) {
	for _, test := range []struct {
		mode    assistantUIActionValueMode
		input   string
		value   string
		include bool
	}{
		{assistantUIActionBinary, "true", "1", true},
		{assistantUIActionBinary, "false", "0", true},
		{assistantUIActionPresence, "true", "1", true},
		{assistantUIActionPresence, "false", "1", false},
		{assistantUIActionConfirmation, "true", "yes", true},
		{assistantUIActionConfirmation, "false", "no", true},
	} {
		value, include, err := normalizeAssistantUIActionValue(test.mode, test.input)
		if err != nil || value != test.value || include != test.include {
			t.Fatalf("normalize(%q, %q) = %q, %t, %v", test.mode, test.input, value, include, err)
		}
	}
}

func TestAssistantPromptForbidsToolRecovery(t *testing.T) {
	for _, prompt := range []string{
		"失败不重试、不替代。",
		"失败后不要重试。",
		"Do not retry or substitute after a failure.",
	} {
		if !assistantPromptForbidsToolRecovery(prompt) {
			t.Fatalf("prompt was not recognized: %q", prompt)
		}
	}
	if assistantPromptForbidsToolRecovery("失败后请先诊断，再决定是否重试。") {
		t.Fatal("ordinary recovery prompt must not enable the hard stop")
	}
}

func TestAssistantMessageTimelineKeepsToolCallsWithTheirReply(t *testing.T) {
	messages := []assistant.Message{{ID: "user-1", Role: "user", Body: "question"}, {ID: "reply-1", Role: "assistant", Body: "Before after"}, {ID: "reply-2", Role: "assistant", Body: "Done"}}
	calls := []assistant.ToolCall{{ID: "call-1", MessageID: "reply-1", Name: "first", Status: "complete", BodyOffset: 7}, {ID: "call-2", MessageID: "reply-1", Name: "second", Status: "error", BodyOffset: 7}, {ID: "call-3", MessageID: "reply-2", Name: "third", Status: "complete", BodyOffset: 0}}
	timeline := assistantMessageTimeline(messages, calls, localeEnglishUS)
	if len(timeline) != 3 || len(timeline[1].ToolCalls) != 2 || timeline[1].LatestToolCall == nil || timeline[1].LatestToolCall.Name != "second" || len(timeline[2].ToolCalls) != 1 {
		t.Fatalf("timeline = %#v", timeline)
	}
	parts := timeline[1].Parts
	if len(parts) != 3 || parts[0].Kind != "text" || parts[0].Body != "Before " || parts[1].Kind != "tool" || len(parts[1].ToolCalls) != 2 || parts[2].Kind != "text" || parts[2].Body != "after" {
		t.Fatalf("interleaved parts = %#v", parts)
	}
	if parts[1].Title != "Called 2 tools" || parts[1].AggregateStatus != "error" || parts[1].ResultSummary != "1 succeeded · 1 failed" {
		t.Fatalf("tool group title=%q status=%q summary=%q", parts[1].Title, parts[1].AggregateStatus, parts[1].ResultSummary)
	}
	if single := timeline[2].Parts[0]; single.Title != "Called 1 tool" || single.ResultSummary != "1 succeeded · 0 failed" {
		t.Fatalf("single tool title=%q summary=%q", single.Title, single.ResultSummary)
	}
}

func TestAssistantOperationViewsKeepOnlyExecutedStateChanges(t *testing.T) {
	started := time.Date(2026, time.August, 3, 14, 36, 0, 0, time.Local)
	calls := []assistant.ToolCall{
		{ID: "read", Name: "inspect_host", Status: "complete", TargetSummary: "Host", StartedAt: started.Add(-5 * time.Minute)},
		{ID: "waiting", Name: "stop_run", Status: "waiting_approval", TargetSummary: "Run 1", StartedAt: started.Add(-4 * time.Minute)},
		{ID: "rejected", Name: "run_schedule_now", Status: "rejected", ErrorCode: "approval_rejected", TargetSummary: "Nightly", StartedAt: started.Add(-3 * time.Minute)},
		{ID: "expired", Name: "check_website_now", Status: "error", ErrorCode: "approval_expired", TargetSummary: "Home", StartedAt: started.Add(-2 * time.Minute)},
		{ID: "complete", Name: "stop_run", Status: "complete", TargetSummary: "Run 1842", StartedAt: started.Add(-time.Minute)},
		{ID: "failed", Name: "perform_ui_action", Status: "error", ErrorCode: "action_failed", TargetSummary: "Save defaults", StartedAt: started},
	}

	views := assistantOperationViews(calls, localeSimplifiedChinese)
	if len(views) != 2 {
		t.Fatalf("operation views = %#v, want two executed state changes", views)
	}
	if views[0].ID != "failed" || views[0].State != "failed" || !strings.Contains(views[0].Label, "执行界面操作") {
		t.Fatalf("newest failed operation = %#v", views[0])
	}
	if views[1].ID != "complete" || views[1].State != "success" || views[1].TimeLabel != "14:35" {
		t.Fatalf("completed operation = %#v", views[1])
	}
}

func TestAssistantConversationUsageFormatsProviderTelemetry(t *testing.T) {
	percent := 64.2
	view := assistantConversationUsage(assistant.Conversation{Telemetry: assistant.SessionTelemetry{
		InputTokens: 126420, OutputTokens: 18630, ContextTokens: 81920, ContextWindow: 128000,
		ContextPercent: &percent, UpdatedAt: time.Now(),
	}})
	if !view.Available || !view.ContextAvailable || view.ContextPercent != percent || view.ContextTokens != "81,920" || view.ContextWindow != "128,000" || view.InputTokens != "126,420" || view.OutputTokens != "18,630" {
		t.Fatalf("usage view = %#v", view)
	}
}
