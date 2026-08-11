package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"scriptboard/internal/appstatus"
	"scriptboard/internal/assistant"
	"scriptboard/internal/assistant/toolbroker"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/logstream"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/websitemonitor"
)

const (
	assistantToolCallsPerTurn    = 24
	assistantToolResultBytes     = 96 << 10
	assistantToolTurnResultBytes = 256 << 10
	assistantToolTextBytes       = 64 << 10
)

type assistantToolExecutor struct {
	app *App
	mu  sync.Mutex
	// Only byte counts are retained. Tool bodies remain process-local and are
	// discarded as soon as the next message in a conversation starts.
	resultBytes map[string]int
	cursorKey   [32]byte
}

type assistantToolAuthorization struct {
	Actor       assistant.Actor
	Role        userRole
	AuthVersion int64
}

type assistantToolPlan struct {
	stateful                        bool
	targetSummary, parameterSummary string
	deepLink                        string
	normalized, targetState         any
	approvalTitle, approvalMessage  string
	execute                         func(context.Context) (any, string, bool, error)
}

type assistantToolIDParameters struct {
	ID string `json:"id"`
}

type assistantToolListParameters struct {
	Limit int `json:"limit,omitempty"`
}

type assistantToolRunListParameters struct {
	Limit  int    `json:"limit,omitempty"`
	Status string `json:"status,omitempty"`
}

type assistantToolLogParameters struct {
	ID       string `json:"id"`
	MaxLines int    `json:"maxLines,omitempty"`
}

type assistantToolTextParameters struct {
	Reference string `json:"reference"`
	MaxLines  int    `json:"maxLines,omitempty"`
}

type assistantToolEvidenceSearchParameters struct {
	ID, Query, Cursor string
	Limit             int
}

type assistantToolEvidenceWindowParameters struct {
	ID, Cursor string
	Sequence   int64
	Limit      int
}

type assistantToolCompareRunsParameters struct {
	IDs []string `json:"ids"`
}

type assistantToolScheduleHistoryParameters struct {
	ID, Cursor string
	Limit      int
}

type assistantToolAuditParameters struct {
	Query, Cursor string
	Limit         int
}

func newAssistantToolExecutor(application *App) *assistantToolExecutor {
	executor := &assistantToolExecutor{app: application, resultBytes: make(map[string]int)}
	if _, err := rand.Read(executor.cursorKey[:]); err != nil {
		panic("initialize Assistant evidence cursor key: " + err.Error())
	}
	return executor
}

func (executor *assistantToolExecutor) releaseTurn(conversationID, messageID string) {
	executor.mu.Lock()
	delete(executor.resultBytes, conversationID+"\x00"+messageID)
	executor.mu.Unlock()
}

func (executor *assistantToolExecutor) Invoke(ctx context.Context, invocation toolbroker.Invocation) (response toolbroker.Response) {
	var responseActor assistant.Actor
	persistResponse := false
	defer func() {
		if !persistResponse || responseActor.UserID == "" || invocation.Binding.ConversationID == "" || invocation.Request.ToolCallID == "" {
			return
		}
		recordContext := context.WithoutCancel(ctx)
		if call, err := executor.app.assistant.RecordToolCallResponse(recordContext, responseActor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, assistantToolResponseJSON(response)); err == nil {
			executor.publishCall(recordContext, responseActor, invocation.Binding.ConversationID, call.ID, "tool_updated", nil)
		}
	}()
	if err := ctx.Err(); err != nil {
		return assistantToolError("tool_cancelled", "Tool call was cancelled.")
	}
	authorization, ok := executor.authorize(ctx, invocation.Binding)
	responseActor = authorization.Actor
	if !ok {
		if authorization.Actor.UserID != "" && invocation.Request.ApprovalID != "" {
			persistResponse = true
			_ = executor.app.assistant.InvalidateApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ApprovalID, "tool_forbidden")
		}
		executor.auditDenied(invocation, "authorization_changed")
		return toolbroker.Response{Status: toolbroker.StatusForbidden, ErrorCode: "tool_forbidden", Summary: "Current ScriptBoard authorization does not allow this tool call."}
	}
	required, stateful := assistantToolPermission(invocation.Request.Tool)
	if !roleAllows(authorization.Role, required) {
		if invocation.Request.ApprovalID != "" {
			persistResponse = true
			_ = executor.app.assistant.InvalidateApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ApprovalID, "tool_forbidden")
		}
		persistResponse = executor.recordDeniedCall(ctx, authorization, invocation, "tool_forbidden") || persistResponse
		return toolbroker.Response{Status: toolbroker.StatusForbidden, ErrorCode: "tool_forbidden", Summary: "Current ScriptBoard role does not allow this tool."}
	}
	hasApprovalResponse := invocation.Request.ApprovalID != "" || invocation.Request.Decision != ""
	if hasApprovalResponse {
		if !stateful || invocation.Request.ApprovalID == "" || (invocation.Request.Decision != "approve" && invocation.Request.Decision != "reject") {
			return assistantToolError("approval_invalid", "The approval response is invalid.")
		}
		persistResponse = true
		if invocation.Request.Decision == "reject" {
			return executor.rejectApprovedCall(ctx, authorization, invocation)
		}
	}
	plan, err := executor.plan(ctx, authorization, invocation)
	if err != nil {
		if errors.Is(err, errAssistantToolForbidden) {
			executor.auditDenied(invocation, "tool_forbidden")
			return toolbroker.Response{Status: toolbroker.StatusForbidden, ErrorCode: "tool_forbidden", Summary: "Current ScriptBoard role or domain ownership does not allow this tool."}
		}
		return assistantToolPlanError(err)
	}
	plan.stateful = stateful
	messageID, err := executor.currentMessageID(ctx, invocation.Binding.ConversationID)
	if err != nil {
		return assistantToolError("conversation_interrupted", "The active Agent Turn is no longer available.")
	}
	if hasApprovalResponse {
		return executor.resumeApprovedCall(ctx, authorization, invocation, plan, messageID)
	}
	if executor.turnRecoveryBlocked(ctx, invocation.Binding.ConversationID, messageID) {
		return assistantToolError("tool_recovery_blocked", "The user required this turn to stop Tool Calls after the first failure; no retry or substitute action was performed.")
	}
	if executor.toolCallCount(ctx, invocation.Binding.ConversationID, messageID) >= assistantToolCallsPerTurn {
		return assistantToolError("tool_limit_reached", "This Agent Turn reached its Tool Call limit.")
	}
	call, err := executor.app.assistant.StartToolCall(ctx, authorization.Actor, invocation.Binding.ConversationID, messageID, invocation.Request.ToolCallID, assistant.ToolCallInput{
		Name: invocation.Request.Tool, TargetSummary: plan.targetSummary, ParameterSummary: plan.parameterSummary,
		RequestJSON: assistantToolRequestJSON(invocation.Request),
	})
	if err != nil {
		if errors.Is(err, assistant.ErrToolReplay) {
			return assistantToolError("tool_replay", "A Tool Call ID cannot be reused.")
		}
		return assistantToolError("tool_failed", "ScriptBoard could not record the Tool Call.")
	}
	persistResponse = true
	executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_started", nil)
	if !stateful {
		return executor.executeCall(ctx, authorization, invocation, plan, call, "")
	}
	digest, err := assistantToolApprovalDigest(invocation, authorization, plan)
	if err != nil {
		return executor.failRecordedCall(ctx, authorization, invocation, call, "tool_failed", "Tool parameters could not be frozen.", "")
	}
	approval, err := executor.app.assistant.RequestApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, digest)
	if err != nil {
		return executor.failRecordedCall(ctx, authorization, invocation, call, "approval_invalid", "ScriptBoard could not create a bounded approval.", "")
	}
	executor.auditStateChange(authorization, invocation, call, approval.ID, "assistant_tool_requested", "requested")
	conversation, err := executor.app.assistant.Conversation(ctx, authorization.Actor, invocation.Binding.ConversationID)
	if err != nil {
		return executor.failRecordedCall(ctx, authorization, invocation, call, "conversation_interrupted", "The conversation is no longer available.", approval.ID)
	}
	if conversation.AutoApproval {
		approval, err = executor.app.assistant.DecideApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, approval.ID, true)
		if err != nil {
			return executor.failRecordedCall(ctx, authorization, invocation, call, "approval_invalid", "Automatic approval could not be recorded.", approval.ID)
		}
		executor.auditStateChange(authorization, invocation, call, approval.ID, "assistant_tool_approved", "auto_approved")
		if err := executor.app.assistant.ConsumeApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, approval.ID, digest); err != nil {
			return executor.failRecordedCall(ctx, authorization, invocation, call, "approval_invalid", "Automatic approval became invalid.", approval.ID)
		}
		executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "approval_resolved", &approval)
		return executor.executeCall(ctx, authorization, invocation, plan, call, approval.ID)
	}
	executor.app.assistantRuntime.RegisterApproval(authorization.Actor, approval)
	executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "approval_requested", &approval)
	return toolbroker.Response{
		Status: toolbroker.StatusApproval, Summary: "This state change requires one-time approval.",
		Approval: &toolbroker.Approval{ID: approval.ID, Title: plan.approvalTitle, Message: plan.approvalMessage},
	}
}

func (executor *assistantToolExecutor) authorize(ctx context.Context, binding toolbroker.SessionBinding) (assistantToolAuthorization, bool) {
	var username, role string
	var enabled int
	var authVersion int64
	if err := executor.app.db.QueryRowContext(ctx, `SELECT username, role, enabled, auth_version FROM users WHERE id = ?`, binding.UserID).Scan(&username, &role, &enabled, &authVersion); err != nil {
		return assistantToolAuthorization{}, false
	}
	authorization := assistantToolAuthorization{Actor: assistant.Actor{UserID: binding.UserID, Username: username}, Role: userRole(role), AuthVersion: authVersion}
	if enabled != 1 {
		return authorization, false
	}
	if _, err := executor.app.assistant.Conversation(ctx, authorization.Actor, binding.ConversationID); err != nil {
		return assistantToolAuthorization{}, false
	}
	return authorization, true
}

func assistantToolPermission(name string) (permission, bool) {
	switch name {
	case "read_managed_text":
		return permissionReadFiles, false
	case "list_audit_events":
		return permissionReadAudit, false
	case "start_quick_run", "stop_run":
		return permissionExecute, true
	case "run_schedule_now":
		return permissionManageExecution, true
	case "check_website_now":
		return permissionManageOperations, true
	case "perform_ui_action":
		return permissionObserve, true
	default:
		return permissionObserve, false
	}
}

func (executor *assistantToolExecutor) currentMessageID(ctx context.Context, conversationID string) (string, error) {
	var id string
	err := executor.app.db.QueryRowContext(ctx, `SELECT id FROM assistant_messages WHERE conversation_id = ? AND role = 'assistant' AND status = 'streaming' ORDER BY sequence DESC LIMIT 1`, conversationID).Scan(&id)
	return id, err
}

func (executor *assistantToolExecutor) toolCallCount(ctx context.Context, conversationID, messageID string) int {
	var count int
	_ = executor.app.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_tool_calls WHERE conversation_id = ? AND message_id = ?`, conversationID, messageID).Scan(&count)
	return count
}

func (executor *assistantToolExecutor) turnRecoveryBlocked(ctx context.Context, conversationID, messageID string) bool {
	var prompt string
	if err := executor.app.db.QueryRowContext(ctx, `SELECT body FROM assistant_messages
		WHERE conversation_id = ? AND role = 'user'
		AND sequence < (SELECT sequence FROM assistant_messages WHERE id = ? AND conversation_id = ?)
		ORDER BY sequence DESC LIMIT 1`, conversationID, messageID, conversationID).Scan(&prompt); err != nil || !assistantPromptForbidsToolRecovery(prompt) {
		return false
	}
	var failed int
	_ = executor.app.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM assistant_tool_calls
		WHERE conversation_id = ? AND message_id = ? AND status IN ('error', 'rejected'))`, conversationID, messageID).Scan(&failed)
	return failed == 1
}

func assistantPromptForbidsToolRecovery(prompt string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(prompt), " "))
	markers := []string{
		"失败不重试", "失败后不重试", "失败后不要重试", "不要重试", "不可重试",
		"不替代", "不要替代", "不可替代", "no retry", "do not retry", "don't retry",
		"no substitute", "do not substitute", "don't substitute",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func (executor *assistantToolExecutor) resumeApprovedCall(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation, plan assistantToolPlan, messageID string) toolbroker.Response {
	approval, err := executor.app.assistant.Approval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ApprovalID)
	if err != nil {
		return assistantToolError("approval_invalid", "The one-time approval is unavailable.")
	}
	call, err := executor.app.assistant.ToolCallByID(ctx, authorization.Actor, invocation.Binding.ConversationID, approval.ToolCallID)
	if err != nil || call.Name != invocation.Request.Tool || call.MessageID != messageID {
		return assistantToolError("approval_invalid", "The approval is not bound to this Tool Call.")
	}
	digest, err := assistantToolApprovalDigest(invocation, authorization, plan)
	if err != nil || subtle.ConstantTimeCompare([]byte(digest), []byte(approval.ParameterDigest)) != 1 {
		_ = executor.app.assistant.InvalidateApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, approval.ID, "tool_target_changed")
		executor.auditStateChange(authorization, invocation, call, approval.ID, "assistant_tool_finished", "target_changed")
		executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", &approval)
		executor.app.assistantRuntime.CompleteApproval(invocation.Binding.ConversationID, approval.ID)
		return assistantToolError("tool_target_changed", "The target, parameters, or authorization changed after approval.")
	}
	if approval.Status != "approved" {
		return assistantToolError("approval_invalid", "The approval was not granted by ScriptBoard.")
	}
	if err := executor.app.assistant.ConsumeApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, approval.ID, digest); err != nil {
		code := "approval_invalid"
		if errors.Is(err, assistant.ErrApprovalExpired) {
			code = "approval_expired"
		}
		executor.auditStateChange(authorization, invocation, call, approval.ID, "assistant_tool_finished", code)
		executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", &approval)
		executor.app.assistantRuntime.CompleteApproval(invocation.Binding.ConversationID, approval.ID)
		return assistantToolError(code, "The one-time approval expired or was already consumed.")
	}
	executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "approval_resolved", &approval)
	return executor.executeCall(ctx, authorization, invocation, plan, call, approval.ID)
}

func (executor *assistantToolExecutor) rejectApprovedCall(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation) toolbroker.Response {
	approval, err := executor.app.assistant.Approval(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ApprovalID)
	if err != nil {
		return assistantToolError("approval_invalid", "The one-time approval is unavailable.")
	}
	call, err := executor.app.assistant.ToolCallByID(ctx, authorization.Actor, invocation.Binding.ConversationID, approval.ToolCallID)
	if err != nil || call.Name != invocation.Request.Tool {
		return assistantToolError("approval_invalid", "The approval is not bound to this Tool Call.")
	}
	if approval.Status != "rejected" && approval.Status != "expired" && approval.Status != "cancelled" {
		return assistantToolError("approval_invalid", "The rejection was not recorded by ScriptBoard.")
	}
	executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", &approval)
	executor.app.assistantRuntime.CompleteApproval(invocation.Binding.ConversationID, approval.ID)
	code := "approval_rejected"
	if approval.Status == "expired" {
		code = "approval_expired"
	} else if approval.Status == "cancelled" {
		code = "approval_cancelled"
	}
	return toolbroker.Response{Status: toolbroker.StatusRejected, ErrorCode: code, Summary: "The state change was not approved."}
}

func (executor *assistantToolExecutor) executeCall(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation, plan assistantToolPlan, call assistant.ToolCall, approvalID string) toolbroker.Response {
	content, summary, truncated, err := plan.execute(ctx)
	if err != nil {
		var actionErr assistantUIActionHTTPError
		if errors.As(err, &actionErr) {
			code, message := actionErr.toolResponse()
			return executor.failRecordedCall(ctx, authorization, invocation, call, code, message, approvalID)
		}
		code, message := assistantToolExecutionError(err)
		return executor.failRecordedCall(ctx, authorization, invocation, call, code, message, approvalID)
	}
	if truncated {
		summary = strings.TrimSpace(summary + " Result truncated at the ScriptBoard bound.")
	}
	response := toolbroker.Response{Status: toolbroker.StatusSuccess, Content: content, Summary: summary, Truncated: truncated, DeepLink: plan.deepLink}
	if !executor.reserveResult(invocation.Binding.ConversationID, call.MessageID, response) {
		return executor.failRecordedCall(ctx, authorization, invocation, call, "tool_result_too_large", "The bounded result budget for this Agent Turn was reached.", approvalID)
	}
	if err := executor.app.assistant.FinishToolCall(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, "complete", "", summary); err != nil {
		return assistantToolError("tool_failed", "ScriptBoard could not finalize the Tool Call.")
	}
	if plan.stateful {
		executor.auditStateChange(authorization, invocation, call, approvalID, "assistant_tool_finished", "succeeded")
	}
	executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", nil)
	if approvalID != "" {
		executor.app.assistantRuntime.CompleteApproval(invocation.Binding.ConversationID, approvalID)
	}
	return response
}

func (executor *assistantToolExecutor) failRecordedCall(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation, call assistant.ToolCall, code, message, approvalID string) toolbroker.Response {
	if approvalID != "" {
		_ = executor.app.assistant.InvalidateApproval(ctx, authorization.Actor, invocation.Binding.ConversationID, approvalID, code)
	} else {
		_ = executor.app.assistant.FinishToolCall(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, "error", code, message)
	}
	if call.ID != "" {
		executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", nil)
	}
	if planStatefulTool(invocation.Request.Tool) {
		executor.auditStateChange(authorization, invocation, call, approvalID, "assistant_tool_finished", code)
	}
	if approvalID != "" {
		executor.app.assistantRuntime.CompleteApproval(invocation.Binding.ConversationID, approvalID)
	}
	return assistantToolError(code, message)
}

func planStatefulTool(name string) bool {
	_, stateful := assistantToolPermission(name)
	return stateful
}

func (executor *assistantToolExecutor) reserveResult(conversationID, messageID string, response toolbroker.Response) bool {
	payload, err := json.Marshal(response)
	if err != nil || len(payload) > assistantToolResultBytes {
		return false
	}
	key := conversationID + "\x00" + messageID
	executor.mu.Lock()
	defer executor.mu.Unlock()
	for candidate := range executor.resultBytes {
		if strings.HasPrefix(candidate, conversationID+"\x00") && candidate != key {
			delete(executor.resultBytes, candidate)
		}
	}
	if executor.resultBytes[key]+len(payload) > assistantToolTurnResultBytes {
		return false
	}
	executor.resultBytes[key] += len(payload)
	return true
}

func (executor *assistantToolExecutor) publishCall(ctx context.Context, actor assistant.Actor, conversationID, callID, eventType string, approval *assistant.Approval) {
	call, err := executor.app.assistant.ToolCallByID(ctx, actor, conversationID, callID)
	if err != nil || executor.app.assistantRuntime == nil {
		return
	}
	executor.app.assistantRuntime.publish(conversationID, assistantBrowserEvent{Type: eventType, ToolCall: &call, Approval: approval})
}

func (executor *assistantToolExecutor) recordDeniedCall(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation, code string) bool {
	messageID, err := executor.currentMessageID(ctx, invocation.Binding.ConversationID)
	if err == nil {
		call, startErr := executor.app.assistant.StartToolCall(ctx, authorization.Actor, invocation.Binding.ConversationID, messageID, invocation.Request.ToolCallID, assistant.ToolCallInput{
			Name: invocation.Request.Tool, TargetSummary: "restricted", ParameterSummary: "permission denied",
			RequestJSON: assistantToolRequestJSON(invocation.Request),
		})
		if startErr == nil {
			_ = executor.app.assistant.FinishToolCall(ctx, authorization.Actor, invocation.Binding.ConversationID, invocation.Request.ToolCallID, "error", code, "Current role does not allow this tool.")
			executor.publishCall(ctx, authorization.Actor, invocation.Binding.ConversationID, call.ID, "tool_finished", nil)
			executor.auditDenied(invocation, code)
			return true
		}
	}
	executor.auditDenied(invocation, code)
	return false
}

func assistantToolRequestJSON(request toolbroker.Request) string {
	parameters := request.Parameters
	if !json.Valid(parameters) {
		parameters = json.RawMessage("null")
	}
	payload := struct {
		ToolCallID string          `json:"toolCallId"`
		Tool       string          `json:"tool"`
		Parameters json.RawMessage `json:"parameters"`
	}{ToolCallID: request.ToolCallID, Tool: request.Tool, Parameters: parameters}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func assistantToolResponseJSON(response toolbroker.Response) string {
	encoded, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return `{
  "status": "error",
  "errorCode": "response_json_unavailable"
}`
	}
	return string(encoded)
}

func (executor *assistantToolExecutor) auditDenied(invocation toolbroker.Invocation, result string) {
	executor.app.recordAudit("assistant_tool_denied", assistantToolAuditTarget(invocation.Binding.ConversationID, invocation.Request.Tool, invocation.Request.ToolCallID, "", "restricted"), result, "assistant")
}

func (executor *assistantToolExecutor) auditStateChange(authorization assistantToolAuthorization, invocation toolbroker.Invocation, call assistant.ToolCall, approvalID, action, result string) {
	executor.app.recordAuditWithActor(action,
		assistantToolAuditTarget(invocation.Binding.ConversationID, invocation.Request.Tool, call.ID, approvalID, planAuditTarget(call.TargetSummary)),
		result, "assistant", authorization.Actor.UserID, authorization.Actor.Username, authorization.Role)
}

func planAuditTarget(summary string) string {
	if index := strings.Index(summary, " "); index > 0 {
		return summary[:index]
	}
	return summary
}

func assistantToolAuditTarget(conversationID, tool, callID, approvalID, targetID string) string {
	return fmt.Sprintf("conversation=%s tool=%s call=%s approval=%s target=%s", conversationID, tool, callID, approvalID, targetID)
}

func assistantToolApprovalDigest(invocation toolbroker.Invocation, authorization assistantToolAuthorization, plan assistantToolPlan) (string, error) {
	payload, err := json.Marshal(struct {
		Version, ConversationID, UserID, Role string
		AuthVersion                           int64
		Tool, ToolCallID                      string
		Parameters, TargetState               any
	}{
		Version: "scriptboard-action-approval-v1", ConversationID: invocation.Binding.ConversationID,
		UserID: authorization.Actor.UserID, Role: string(authorization.Role), AuthVersion: authorization.AuthVersion,
		Tool: invocation.Request.Tool, ToolCallID: invocation.Request.ToolCallID,
		Parameters: plan.normalized, TargetState: plan.targetState,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func assistantToolError(code, summary string) toolbroker.Response {
	return toolbroker.Response{Status: toolbroker.StatusError, ErrorCode: code, Summary: summary}
}

var (
	errAssistantToolParameters  = errors.New("assistant tool parameters are invalid")
	errAssistantToolNotFound    = errors.New("assistant tool target was not found")
	errAssistantToolForbidden   = errors.New("assistant tool target is forbidden")
	errAssistantToolUnavailable = errors.New("assistant tool is unavailable")
)

func assistantToolPlanError(err error) toolbroker.Response {
	switch {
	case errors.Is(err, errAssistantToolParameters):
		return assistantToolError("tool_parameters_invalid", "Tool parameters are invalid.")
	case errors.Is(err, errAssistantToolNotFound), errors.Is(err, sql.ErrNoRows):
		return assistantToolError("tool_target_not_found", "The selected ScriptBoard target no longer exists.")
	case errors.Is(err, errAssistantToolUnavailable):
		return assistantToolError("tool_unavailable", strings.TrimSpace(strings.TrimPrefix(err.Error(), errAssistantToolUnavailable.Error()+":")))
	default:
		return assistantToolError("tool_failed", "ScriptBoard could not prepare this Tool Call.")
	}
}

func assistantToolExecutionError(err error) (string, string) {
	switch {
	case errors.Is(err, errAssistantToolNotFound), errors.Is(err, sql.ErrNoRows), errors.Is(err, os.ErrNotExist), errors.Is(err, appstatus.ErrApplicationNotFound):
		return "tool_target_not_found", "The selected ScriptBoard target no longer exists."
	case errors.Is(err, errAssistantToolUnavailable), errors.Is(err, appstatus.ErrApplicationLogsUnsupported), errors.Is(err, appstatus.ErrApplicationLogProbeMissing):
		return "tool_unavailable", "The requested capability is unavailable for this ScriptBoard target."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "tool_cancelled", "The Tool Call was cancelled before it completed."
	default:
		return "tool_failed", "ScriptBoard could not complete the Tool Call."
	}
}

func decodeAssistantToolParameters(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errAssistantToolParameters
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errAssistantToolParameters
	}
	return nil
}

func validAssistantToolID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n/\\")
}

func normalizeAssistantToolLimit(value, fallback int) (int, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 1 || value > 50 {
		return 0, errAssistantToolParameters
	}
	return value, nil
}

func normalizeAssistantEvidenceQuery(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errAssistantToolParameters
	}
	return value, nil
}

func (executor *assistantToolExecutor) evidenceCursor(invocation toolbroker.Invocation, tool, target, query, encoded string) (assistantEvidenceCursor, error) {
	expected := assistantEvidenceCursor{
		UserID: invocation.Binding.UserID, ConversationID: invocation.Binding.ConversationID,
		Tool: tool, Target: target, QueryDigest: assistantEvidenceQueryDigest(query),
	}
	if strings.TrimSpace(encoded) == "" {
		return expected, nil
	}
	return decodeAssistantEvidenceCursor(executor.cursorKey, encoded, expected, time.Now().UTC())
}

func (executor *assistantToolExecutor) nextEvidenceCursor(cursor assistantEvidenceCursor, page string, offset int) string {
	cursor.Page = page
	cursor.Offset = offset
	cursor.ExpiresAt = time.Now().UTC().Add(5 * time.Minute).Unix()
	encoded, _ := encodeAssistantEvidenceCursor(executor.cursorKey, cursor)
	return encoded
}

func normalizeAssistantToolLines(value int) (int, error) {
	if value == 0 {
		return 100, nil
	}
	if value < 1 || value > 400 {
		return 0, errAssistantToolParameters
	}
	return value, nil
}

func assistantToolTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func assistantToolPointerTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return assistantToolTime(*value)
}

func assistantToolBaseName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return "managed script"
	}
	return name
}

func assistantToolBoundText(value string, maximumLines int) (string, bool) {
	lines := strings.Split(value, "\n")
	truncated := false
	if len(lines) > maximumLines {
		lines = lines[len(lines)-maximumLines:]
		truncated = true
	}
	value = strings.Join(lines, "\n")
	if len(value) > assistantToolTextBytes {
		value = strings.ToValidUTF8(value[len(value)-assistantToolTextBytes:], "\uFFFD")
		truncated = true
	}
	return value, truncated
}

func assistantToolRunActive(status string) bool {
	switch status {
	case "starting", "running", "stopping", "timing_out":
		return true
	default:
		return false
	}
}

func (executor *assistantToolExecutor) plan(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	switch invocation.Request.Tool {
	case "get_host_status":
		var parameters struct{}
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
			return assistantToolPlan{}, err
		}
		return assistantToolPlan{targetSummary: "host current", parameterSummary: "latest snapshot", normalized: parameters, deepLink: "/overview", execute: func(ctx context.Context) (any, string, bool, error) {
			overview, err := executor.app.hostStatus.Overview(ctx, "15m")
			if err != nil {
				return nil, "", false, err
			}
			errorCodes := make([]string, 0, len(overview.Errors))
			for code := range overview.Errors {
				errorCodes = append(errorCodes, code)
			}
			content := map[string]any{
				"source": "ScriptBoard host monitor", "untrustedData": false, "collectedAt": assistantToolTime(overview.CollectedAt), "stale": overview.Stale,
				"host": map[string]any{"hostname": overview.Facts.Hostname, "os": overview.Facts.OS, "platform": overview.Facts.Platform, "architecture": overview.Facts.Architecture, "logicalCores": overview.Facts.LogicalCores, "totalMemoryBytes": overview.Facts.TotalMemoryBytes},
				"cpu":  overview.Current.CPU, "memory": overview.Current.Memory, "disk": overview.Current.Disk, "network": overview.Current.Network, "serviceProcess": overview.Current.Process, "errorCodes": errorCodes,
			}
			return content, "Read current host status.", false, nil
		}}, nil
	case "list_applications":
		var parameters assistantToolListParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
			return assistantToolPlan{}, err
		}
		limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
		if err != nil {
			return assistantToolPlan{}, err
		}
		parameters.Limit = limit
		return assistantToolPlan{targetSummary: "applications current", parameterSummary: fmt.Sprintf("limit %d", limit), normalized: parameters, deepLink: "/monitor/applications", execute: func(ctx context.Context) (any, string, bool, error) {
			view, err := executor.app.applicationStatus.View(ctx, appstatus.Query{Limit: limit})
			if err != nil {
				return nil, "", false, err
			}
			items := make([]any, 0, len(view.Applications))
			for _, application := range view.Applications {
				items = append(items, compactAssistantApplication(application))
			}
			return map[string]any{"source": "ScriptBoard application monitor", "untrustedData": true, "collectedAt": assistantToolTime(view.CollectedAt), "applications": items, "total": view.Total}, fmt.Sprintf("Read %d applications.", len(items)), view.Truncated, nil
		}}, nil
	case "get_application":
		var parameters assistantToolIDParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		return assistantToolPlan{targetSummary: parameters.ID + " application", parameterSummary: "stable ID", normalized: parameters, deepLink: "/monitor/applications/" + url.PathEscape(parameters.ID), execute: func(ctx context.Context) (any, string, bool, error) {
			view, err := executor.app.applicationStatus.View(ctx, appstatus.Query{Limit: 256})
			if err != nil {
				return nil, "", false, err
			}
			for _, application := range append(view.Pinned, view.Applications...) {
				if application.ID == parameters.ID {
					return map[string]any{"source": "ScriptBoard application monitor", "untrustedData": true, "collectedAt": assistantToolTime(view.CollectedAt), "application": compactAssistantApplication(application)}, "Read application status.", false, nil
				}
			}
			return nil, "", false, errAssistantToolNotFound
		}}, nil
	case "read_source_log":
		var parameters assistantToolLogParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		lines, err := normalizeAssistantToolLines(parameters.MaxLines)
		if err != nil {
			return assistantToolPlan{}, err
		}
		parameters.MaxLines = lines
		return assistantToolPlan{targetSummary: parameters.ID + " source-log", parameterSummary: fmt.Sprintf("last %d lines", lines), normalized: parameters, deepLink: "/monitor/applications/" + url.PathEscape(parameters.ID) + "/logs", execute: func(ctx context.Context) (any, string, bool, error) {
			source, err := executor.app.applicationStatus.LogSource(ctx, parameters.ID)
			if err != nil {
				return nil, "", false, err
			}
			page, err := source.History(ctx, "")
			if err != nil {
				return nil, "", false, err
			}
			entries, truncated := compactAssistantLogEntries(page.Entries, lines)
			return map[string]any{"source": "ScriptBoard application source log", "untrustedData": true, "entries": entries}, fmt.Sprintf("Read %d source log entries.", len(entries)), truncated || page.HasMore, nil
		}}, nil
	case "list_website_monitors":
		var parameters assistantToolListParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
			return assistantToolPlan{}, err
		}
		limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
		if err != nil {
			return assistantToolPlan{}, err
		}
		parameters.Limit = limit
		return assistantToolPlan{targetSummary: "websites current", parameterSummary: fmt.Sprintf("limit %d", limit), normalized: parameters, deepLink: "/monitor/websites", execute: func(ctx context.Context) (any, string, bool, error) {
			monitors, err := executor.app.websiteMonitor.List(ctx, websitemonitor.Filter{})
			if err != nil {
				return nil, "", false, err
			}
			truncated := len(monitors) > limit
			if truncated {
				monitors = monitors[:limit]
			}
			items := make([]any, 0, len(monitors))
			for _, monitor := range monitors {
				items = append(items, compactAssistantWebsite(monitor))
			}
			return map[string]any{"source": "ScriptBoard website monitor", "untrustedData": true, "monitors": items}, fmt.Sprintf("Read %d website monitors.", len(items)), truncated, nil
		}}, nil
	case "get_website_incident":
		var parameters assistantToolIDParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		return assistantToolPlan{targetSummary: parameters.ID + " website", parameterSummary: "recent incident evidence", normalized: parameters, deepLink: "/monitor/websites/" + url.PathEscape(parameters.ID), execute: func(ctx context.Context) (any, string, bool, error) {
			detail, err := executor.app.websiteMonitor.DetailSnapshot(ctx, parameters.ID)
			if err != nil {
				return nil, "", false, err
			}
			checks := detail.RecentChecks
			truncated := len(checks) > 20
			if truncated {
				checks = checks[:20]
			}
			incidents := detail.Incidents
			if len(incidents) > 20 {
				incidents = incidents[:20]
				truncated = true
			}
			return map[string]any{"source": "ScriptBoard website monitor", "untrustedData": true, "monitor": compactAssistantWebsite(detail.Monitor), "currentIncident": detail.CurrentIncident, "recentChecks": checks, "incidents": incidents, "availabilityPercent": detail.AvailabilityPercent}, "Read recent website incident evidence.", truncated, nil
		}}, nil
	case "list_runs":
		var parameters assistantToolRunListParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
			return assistantToolPlan{}, err
		}
		limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
		if err != nil || !validAssistantRunStatus(parameters.Status) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		parameters.Limit, parameters.Status = limit, strings.TrimSpace(parameters.Status)
		return assistantToolPlan{targetSummary: "runs history", parameterSummary: fmt.Sprintf("limit %d status %s", limit, parameters.Status), normalized: parameters, deepLink: "/history/runs", execute: func(context.Context) (any, string, bool, error) {
			runs, err := executor.app.runs.List(100)
			if err != nil {
				return nil, "", false, err
			}
			items := make([]any, 0, limit)
			matched := 0
			for _, run := range runs {
				if parameters.Status != "" && run.Status != parameters.Status {
					continue
				}
				matched++
				if len(items) < limit {
					items = append(items, compactAssistantRun(run))
				}
			}
			return map[string]any{"source": "ScriptBoard Run Manager", "untrustedData": true, "runs": items}, fmt.Sprintf("Read %d Runs.", len(items)), matched > len(items) || len(runs) == 100, nil
		}}, nil
	case "get_run":
		var parameters assistantToolIDParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		return assistantToolPlan{targetSummary: parameters.ID + " run", parameterSummary: "bounded metadata", normalized: parameters, deepLink: "/history/runs/" + url.PathEscape(parameters.ID), execute: func(context.Context) (any, string, bool, error) {
			run, err := executor.app.runs.GetMetadata(parameters.ID)
			if err != nil {
				return nil, "", false, err
			}
			return map[string]any{"source": "ScriptBoard Run Manager", "untrustedData": true, "run": compactAssistantRun(run)}, "Read Run metadata.", false, nil
		}}, nil
	case "read_run_log":
		var parameters assistantToolLogParameters
		if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		lines, err := normalizeAssistantToolLines(parameters.MaxLines)
		if err != nil {
			return assistantToolPlan{}, err
		}
		parameters.MaxLines = lines
		return assistantToolPlan{targetSummary: parameters.ID + " run-log", parameterSummary: fmt.Sprintf("last %d lines", lines), normalized: parameters, deepLink: "/history/runs/" + url.PathEscape(parameters.ID), execute: func(context.Context) (any, string, bool, error) {
			run, err := executor.app.runs.Get(parameters.ID)
			if err != nil {
				return nil, "", false, err
			}
			entries, truncated := compactAssistantRunEvents(run.Events, lines)
			return map[string]any{"source": "ScriptBoard Run log", "untrustedData": true, "runId": run.ID, "status": run.Status, "entries": entries}, fmt.Sprintf("Read %d Run log entries.", len(entries)), truncated || run.LogTruncated || run.LogIncomplete, nil
		}}, nil
	case "search_run_log":
		return executor.planSearchRunLog(invocation)
	case "read_run_log_window":
		return executor.planReadRunLogWindow(invocation)
	case "compare_runs":
		return executor.planCompareRuns(invocation)
	case "search_source_log":
		return executor.planSearchSourceLog(invocation)
	case "get_schedule_history":
		return executor.planScheduleHistory(invocation)
	case "list_audit_events":
		return executor.planAuditEvents(authorization, invocation)
	case "list_quick_runs":
		return executor.planListQuickRuns(invocation)
	case "list_schedules":
		return executor.planListSchedules(invocation)
	case "read_managed_text":
		return executor.planReadManagedText(ctx, authorization, invocation)
	case "start_quick_run":
		return executor.planStartQuickRun(authorization, invocation)
	case "run_schedule_now":
		return executor.planRunSchedule(authorization, invocation)
	case "stop_run":
		return executor.planStopRun(authorization, invocation)
	case "check_website_now":
		return executor.planCheckWebsite(ctx, invocation)
	case "list_ui_actions":
		return executor.planListUIActions(authorization, invocation)
	case "perform_ui_action":
		return executor.planPerformUIAction(ctx, authorization, invocation)
	default:
		return assistantToolPlan{}, errAssistantToolParameters
	}
}

func compactAssistantApplication(application appstatus.Application) map[string]any {
	return map[string]any{
		"id": application.ID, "name": application.Name, "kind": application.Kind, "running": application.Running,
		"rateAvailable": application.RateAvailable, "cpuPercent": application.CPUPercent, "memoryPercent": application.MemoryPercent,
		"memoryBytes": application.MemoryBytes, "memoryLimitBytes": application.MemoryLimitBytes,
		"readBytesPerSecond": application.ReadBytesPerSecond, "writeBytesPerSecond": application.WriteBytesPerSecond,
		"processCount": application.ProcessCount, "threadCount": application.ThreadCount,
	}
}

func compactAssistantWebsite(monitor websitemonitor.Monitor) map[string]any {
	return map[string]any{
		"id": monitor.ID, "name": monitor.Config.Name, "kind": monitor.Config.Kind, "scope": monitor.Config.Scope,
		"url": monitor.Config.URL, "state": monitor.State, "failureCount": monitor.FailureCount,
		"latest":      map[string]any{"success": monitor.Latest.Success, "statusCode": monitor.Latest.StatusCode, "latencyMilliseconds": monitor.Latest.Latency.Milliseconds(), "checkedAt": assistantToolTime(monitor.Latest.CheckedAt), "errorCategory": monitor.Latest.ErrorCategory, "summary": monitor.Latest.Summary},
		"nextCheckAt": assistantToolTime(monitor.NextCheckAt),
	}
}

func compactAssistantRun(run runmanager.Run) map[string]any {
	return map[string]any{
		"id": run.ID, "script": assistantToolBaseName(run.ScriptPath), "sourceType": run.SourceType, "sourceName": run.SourceName,
		"status": run.Status, "createdAt": assistantToolTime(run.CreatedAt), "startedAt": assistantToolPointerTime(run.StartedAt),
		"finishedAt": assistantToolPointerTime(run.FinishedAt), "exitCode": run.ExitCode, "timeoutSeconds": run.TimeoutSeconds,
		"argumentCount": len(run.Arguments), "logExpired": run.LogExpired, "logIncomplete": run.LogIncomplete, "logTruncated": run.LogTruncated,
		"initiatedBy": run.InitiatorUsername,
	}
}

func compactAssistantLogEntries(entries []logstream.Entry, maximum int) ([]any, bool) {
	truncated := len(entries) > maximum
	if truncated {
		entries = entries[len(entries)-maximum:]
	}
	result := make([]any, 0, len(entries))
	bytesUsed := 0
	for _, entry := range entries {
		text, textTruncated := assistantToolBoundText(entry.Text, maximum)
		if bytesUsed+len(text) > assistantToolTextBytes {
			truncated = true
			break
		}
		bytesUsed += len(text)
		truncated = truncated || textTruncated
		result = append(result, map[string]any{"time": assistantToolPointerTime(entry.Time), "source": entry.Source, "severity": entry.Severity, "text": text, "continuation": entry.Continuation, "encodingError": entry.EncodingError})
	}
	return result, truncated
}

func compactAssistantRunEvents(events []runmanager.Event, maximum int) ([]any, bool) {
	truncated := len(events) > maximum
	if truncated {
		events = events[len(events)-maximum:]
	}
	result := make([]any, 0, len(events))
	bytesUsed := 0
	for _, event := range events {
		text, textTruncated := assistantToolBoundText(event.Data, maximum)
		if bytesUsed+len(text) > assistantToolTextBytes {
			truncated = true
			break
		}
		bytesUsed += len(text)
		truncated = truncated || textTruncated
		result = append(result, map[string]any{"sequence": event.Sequence, "time": assistantToolTime(event.Time), "source": event.Source, "text": text, "encodingError": event.EncodingError})
	}
	return result, truncated
}

func validAssistantRunStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "starting", "running", "stopping", "timing_out", "succeeded", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

func (executor *assistantToolExecutor) planListQuickRuns(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolListParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
		return assistantToolPlan{}, err
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	parameters.Limit = limit
	return assistantToolPlan{targetSummary: "quick-runs current", parameterSummary: fmt.Sprintf("limit %d", limit), normalized: parameters, deepLink: "/config/quick-runs", execute: func(context.Context) (any, string, bool, error) {
		rows, err := executor.app.db.Query(`SELECT id, name, script_path, timeout_seconds, locked FROM quick_runs ORDER BY sort_order, created_at LIMIT ?`, limit+1)
		if err != nil {
			return nil, "", false, err
		}
		defer rows.Close()
		items := make([]any, 0, limit)
		truncated := false
		for rows.Next() {
			var id, name, path string
			var timeout int
			var locked bool
			if err := rows.Scan(&id, &name, &path, &timeout, &locked); err != nil {
				return nil, "", false, err
			}
			if len(items) >= limit {
				truncated = true
				continue
			}
			valid := false
			if info, infoErr := executor.app.files.Info(path); infoErr == nil && info.Mode().IsRegular() {
				valid = true
			}
			items = append(items, map[string]any{"id": id, "name": name, "script": assistantToolBaseName(path), "timeoutSeconds": timeout, "locked": locked, "available": valid})
		}
		return map[string]any{"source": "ScriptBoard Quick Runs", "untrustedData": true, "quickRuns": items}, fmt.Sprintf("Read %d Quick Runs.", len(items)), truncated, rows.Err()
	}}, nil
}

func (executor *assistantToolExecutor) planListSchedules(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolListParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
		return assistantToolPlan{}, err
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	parameters.Limit = limit
	return assistantToolPlan{targetSummary: "schedules current", parameterSummary: fmt.Sprintf("limit %d", limit), normalized: parameters, deepLink: "/config/schedules", execute: func(context.Context) (any, string, bool, error) {
		schedules, err := executor.app.scheduler.ListPage(limit+1, 0)
		if err != nil {
			return nil, "", false, err
		}
		truncated := len(schedules) > limit
		if truncated {
			schedules = schedules[:limit]
		}
		items := make([]any, 0, len(schedules))
		for _, schedule := range schedules {
			items = append(items, map[string]any{
				"id": schedule.ID, "name": schedule.Name, "script": assistantToolBaseName(schedule.ScriptPath), "expression": schedule.Expression,
				"enabled": schedule.Enabled, "allowOverlap": schedule.AllowOverlap, "timeoutSeconds": schedule.TimeoutSeconds,
				"nextFireAt": assistantToolTime(schedule.NextFireAt), "lastResult": schedule.LastResult, "lastRunId": schedule.LastRunID,
			})
		}
		return map[string]any{"source": "ScriptBoard Schedules", "untrustedData": true, "schedules": items}, fmt.Sprintf("Read %d schedules.", len(items)), truncated, nil
	}}, nil
}

func (executor *assistantToolExecutor) planReadManagedText(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolTextParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.Reference) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	lines, err := normalizeAssistantToolLines(parameters.MaxLines)
	if err != nil {
		return assistantToolPlan{}, err
	}
	parameters.MaxLines = lines
	references, err := executor.app.assistant.ContextReferences(ctx, authorization.Actor, invocation.Binding.ConversationID)
	if err != nil {
		return assistantToolPlan{}, err
	}
	referenced := false
	for _, reference := range references {
		if reference.Kind == "file" && reference.StableID == parameters.Reference {
			referenced = true
			break
		}
	}
	if !referenced {
		return assistantToolPlan{}, errAssistantToolNotFound
	}
	return assistantToolPlan{targetSummary: parameters.Reference + " managed-text", parameterSummary: fmt.Sprintf("last %d lines", lines), normalized: parameters, deepLink: "/resources/files", execute: func(context.Context) (any, string, bool, error) {
		entry, err := executor.resolveManagedFile(parameters.Reference)
		if err != nil {
			return nil, "", false, err
		}
		if !isTextPreviewExtension(entry.Name) {
			return nil, "", false, errAssistantToolParameters
		}
		document, err := executor.app.files.ReadText(entry.Path, assistantToolTextBytes)
		if err != nil {
			return nil, "", false, err
		}
		content, truncated := assistantToolBoundText(document.Content, lines)
		return map[string]any{"source": "ScriptBoard managed text", "untrustedData": true, "name": entry.Name, "sha256": document.Digest, "content": content}, "Read bounded managed text.", truncated, nil
	}}, nil
}

func (executor *assistantToolExecutor) resolveManagedFile(reference string) (hostfiles.Entry, error) {
	if entry, found := executor.app.assistantHostEntryByStableID("file", reference); found {
		return entry, nil
	}
	return hostfiles.Entry{}, errAssistantToolNotFound
}

func (executor *assistantToolExecutor) planStartQuickRun(authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolIDParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	var name, path, arguments string
	var timeout int
	var locked bool
	var updatedAt int64
	var scriptSHA256 string
	var revision int64
	if err := executor.app.db.QueryRow(`SELECT name, script_path, arguments_template, timeout_seconds, locked, updated_at, script_sha256, revision FROM quick_runs WHERE id = ?`, parameters.ID).Scan(&name, &path, &arguments, &timeout, &locked, &updatedAt, &scriptSHA256, &revision); err != nil {
		return assistantToolPlan{}, err
	}
	if info, err := executor.app.files.Info(path); err != nil || !info.Mode().IsRegular() || scriptSHA256 == "" {
		return assistantToolPlan{}, errAssistantToolNotFound
	}
	state := struct {
		ID, Name, PathKey, Arguments, ScriptSHA256 string
		Timeout                                    int
		UpdatedAt, Revision                        int64
		Locked, Active                             bool
	}{parameters.ID, name, hostfiles.ComparisonKey(path), arguments, scriptSHA256, timeout, updatedAt, revision, locked, executor.app.runs.IsActiveScript(path)}
	return assistantToolPlan{
		targetSummary: parameters.ID + " quick-run", parameterSummary: "start saved Quick Run",
		normalized: parameters, targetState: state, deepLink: "/config/quick-runs", approvalTitle: "Start Quick Run", approvalMessage: fmt.Sprintf("Start the saved Quick Run %q now?", name),
		execute: func(context.Context) (any, string, bool, error) {
			variables, err := executor.app.loadVariables()
			if err != nil {
				return nil, "", false, err
			}
			quick, loadErr := executor.app.loadQuickRun(parameters.ID)
			if loadErr != nil || quick.Revision != revision || quick.ScriptSHA256 != scriptSHA256 {
				return nil, "Quick Run is no longer available.", false, errors.New("quick run is not published")
			}
			runID, err := executor.app.runs.Start(runmanager.StartRequest{ScriptPath: quick.ScriptPath, ExpectedDigest: quick.ScriptSHA256, ArgumentsTemplate: quick.ArgumentsTemplate, TimeoutSeconds: quick.TimeoutSeconds, SourceType: "assistant/quick-run", SourceName: quick.Name, SourceID: parameters.ID, Variables: variables, InitiatorUserID: authorization.Actor.UserID, InitiatorUsername: authorization.Actor.Username})
			if err != nil {
				return nil, "", false, err
			}
			return map[string]any{"accepted": true, "runId": runID}, "Started Quick Run.", false, nil
		},
	}, nil
}

func (executor *assistantToolExecutor) planRunSchedule(authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolIDParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	var name, path, arguments, expression string
	var timeout, enabled, allowOverlap, nextFireAt, updatedAt int64
	if err := executor.app.db.QueryRow(`SELECT name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, updated_at FROM schedules WHERE id = ? AND deleted = 0`, parameters.ID).Scan(&name, &path, &arguments, &expression, &timeout, &enabled, &allowOverlap, &nextFireAt, &updatedAt); err != nil {
		return assistantToolPlan{}, err
	}
	state := struct {
		ID, Name, PathKey, Arguments, Expression string
		Timeout, Enabled, AllowOverlap           int64
		NextFireAt, UpdatedAt                    int64
		Active                                   bool
	}{parameters.ID, name, hostfiles.ComparisonKey(path), arguments, expression, timeout, enabled, allowOverlap, nextFireAt, updatedAt, executor.app.runs.IsActiveScript(path)}
	return assistantToolPlan{
		targetSummary: parameters.ID + " schedule", parameterSummary: "run schedule immediately",
		normalized: parameters, targetState: state, deepLink: "/config/schedules", approvalTitle: "Run schedule now", approvalMessage: fmt.Sprintf("Trigger the schedule %q immediately?", name),
		execute: func(context.Context) (any, string, bool, error) {
			runID, err := executor.app.scheduler.RunNowAs(parameters.ID, authorization.Actor.UserID, authorization.Actor.Username)
			if err != nil {
				return nil, "", false, err
			}
			return map[string]any{"accepted": true, "runId": runID}, "Triggered schedule.", false, nil
		},
	}, nil
}

func (executor *assistantToolExecutor) planStopRun(authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolIDParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	run, err := executor.app.runs.GetMetadata(parameters.ID)
	if err != nil {
		return assistantToolPlan{}, err
	}
	if !assistantToolRunActive(run.Status) {
		return assistantToolPlan{}, errAssistantToolNotFound
	}
	if authorization.Role == roleOperator && run.InitiatorUserID != authorization.Actor.UserID {
		return assistantToolPlan{}, errAssistantToolForbidden
	}
	state := struct {
		ID, Status, InitiatorUserID string
		CreatedAt                   int64
	}{run.ID, run.Status, run.InitiatorUserID, run.CreatedAt.UnixNano()}
	return assistantToolPlan{
		targetSummary: parameters.ID + " run", parameterSummary: "stop active Run",
		normalized: parameters, targetState: state, deepLink: "/history/runs/" + url.PathEscape(parameters.ID), approvalTitle: "Stop Run", approvalMessage: fmt.Sprintf("Stop the active Run %s?", parameters.ID),
		execute: func(context.Context) (any, string, bool, error) {
			if err := executor.app.runs.Stop(parameters.ID); err != nil {
				return nil, "", false, err
			}
			return map[string]any{"accepted": true, "runId": parameters.ID}, "Requested Run stop.", false, nil
		},
	}, nil
}

func (executor *assistantToolExecutor) planCheckWebsite(ctx context.Context, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolIDParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	monitor, err := executor.app.websiteMonitor.Get(ctx, parameters.ID)
	if err != nil || monitor.DeletedAt != nil {
		return assistantToolPlan{}, errAssistantToolNotFound
	}
	state := struct {
		ID, State string
		UpdatedAt int64
		Failures  int
	}{monitor.ID, string(monitor.State), monitor.UpdatedAt.UnixNano(), monitor.FailureCount}
	return assistantToolPlan{
		targetSummary: parameters.ID + " website", parameterSummary: "check website immediately",
		normalized: parameters, targetState: state, deepLink: "/monitor/websites/" + url.PathEscape(parameters.ID), approvalTitle: "Check website now", approvalMessage: fmt.Sprintf("Run an immediate check for %q?", monitor.Config.Name),
		execute: func(ctx context.Context) (any, string, bool, error) {
			if err := executor.app.websiteMonitor.CheckNow(ctx, parameters.ID); err != nil {
				return nil, "", false, err
			}
			return map[string]any{"accepted": true, "monitorId": parameters.ID}, "Started website check.", false, nil
		},
	}, nil
}
