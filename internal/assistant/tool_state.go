package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrApprovalInvalid  = errors.New("assistant approval is invalid")
	ErrApprovalExpired  = errors.New("assistant approval has expired")
	ErrApprovalRejected = errors.New("assistant approval was rejected")
	ErrApprovalPending  = errors.New("assistant conversation already has a pending approval")
	ErrToolReplay       = errors.New("assistant tool call was already recorded")
	toolNamePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	toolCallPattern     = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,160}$`)
)

const (
	approvalLifetime         = 2 * time.Minute
	maxToolRequestJSONBytes  = 256 << 10
	maxToolResponseJSONBytes = 1 << 20
)

type ToolCallInput struct {
	Name, TargetSummary, ParameterSummary string
	RequestJSON                           string
}

type ToolCall struct {
	ID, ConversationID, MessageID, Name string
	TargetSummary, ParameterSummary     string
	Status, ErrorCode, ResultSummary    string
	RequestJSON, ResponseJSON           string
	BodyOffset                          int
	StartedAt                           time.Time
	FinishedAt                          *time.Time
}

type Approval struct {
	ID, ConversationID, ToolCallID, ParameterDigest, Status string
	RequestedAt, ExpiresAt                                  time.Time
	DecidedAt                                               *time.Time
	DecidedByUserID                                         string
}

func (s *Service) StartToolCall(ctx context.Context, actor Actor, conversationID, messageID, externalID string, input ToolCallInput) (ToolCall, error) {
	requestJSON, validRequestJSON := normalizedToolJSON(input.RequestJSON, "{}", maxToolRequestJSONBytes)
	if strings.TrimSpace(actor.UserID) == "" || !toolCallPattern.MatchString(externalID) || !toolNamePattern.MatchString(input.Name) ||
		!validToolSummary(input.TargetSummary) || !validToolSummary(input.ParameterSummary) || !validRequestJSON {
		return ToolCall{}, fmt.Errorf("%w: invalid tool call", ErrInvalidInput)
	}
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return ToolCall{}, err
	}
	id := toolCallRecordID(conversationID, externalID)
	now := s.now().UTC()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolCall{}, fmt.Errorf("begin assistant tool call: %w", err)
	}
	defer transaction.Rollback()
	bodyOffset := 0
	if messageID != "" {
		err = transaction.QueryRowContext(ctx, `SELECT LENGTH(m.body) FROM assistant_messages m
			JOIN assistant_conversations c ON c.id = m.conversation_id
			WHERE m.id = ? AND m.conversation_id = ? AND m.role = 'assistant' AND c.owner_user_id = ?`,
			messageID, conversationID, actor.UserID).Scan(&bodyOffset)
		if errors.Is(err, sql.ErrNoRows) {
			return ToolCall{}, ErrNotFound
		}
		if err != nil {
			return ToolCall{}, fmt.Errorf("read assistant tool call position: %w", err)
		}
	}
	result, err := transaction.ExecContext(ctx, `INSERT INTO assistant_tool_calls
		(id, conversation_id, message_id, body_offset, tool_name, target_summary, parameter_summary, request_json, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'running', ?)`, id, conversationID, nullableString(messageID), bodyOffset, input.Name,
		strings.TrimSpace(input.TargetSummary), strings.TrimSpace(input.ParameterSummary), requestJSON, now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
			return ToolCall{}, ErrToolReplay
		}
		return ToolCall{}, fmt.Errorf("record assistant tool call: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ToolCall{}, ErrToolReplay
	}
	if err := transaction.Commit(); err != nil {
		return ToolCall{}, fmt.Errorf("commit assistant tool call: %w", err)
	}
	return ToolCall{
		ID: id, ConversationID: conversationID, MessageID: messageID, Name: input.Name,
		TargetSummary: strings.TrimSpace(input.TargetSummary), ParameterSummary: strings.TrimSpace(input.ParameterSummary),
		Status: "running", RequestJSON: requestJSON, ResponseJSON: "null", BodyOffset: bodyOffset, StartedAt: now,
	}, nil
}

func (s *Service) RequestApproval(ctx context.Context, actor Actor, conversationID, externalToolCallID, parameterDigest string) (Approval, error) {
	if !validDigest(parameterDigest) {
		return Approval{}, fmt.Errorf("%w: invalid approval digest", ErrInvalidInput)
	}
	toolCallID := toolCallRecordID(conversationID, externalToolCallID)
	approvalID, err := randomID()
	if err != nil {
		return Approval{}, err
	}
	now := s.now().UTC()
	expires := now.Add(approvalLifetime)
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Approval{}, err
	}
	defer transaction.Rollback()
	if err := ensureConversationOwnerTx(ctx, transaction, actor, conversationID); err != nil {
		return Approval{}, err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'expired', decided_at = ?
		WHERE conversation_id = ? AND status = 'pending' AND expires_at <= ?`, now.Unix(), conversationID, now.Unix()); err != nil {
		return Approval{}, err
	}
	var pending int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_approvals WHERE conversation_id = ? AND status = 'pending'`, conversationID).Scan(&pending); err != nil {
		return Approval{}, err
	}
	if pending != 0 {
		return Approval{}, ErrApprovalPending
	}
	result, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'waiting_approval'
		WHERE id = ? AND conversation_id = ? AND status = 'running'`, toolCallID, conversationID)
	if err != nil {
		return Approval{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Approval{}, ErrApprovalInvalid
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO assistant_approvals
		(id, conversation_id, tool_call_id, parameter_digest, status, requested_at, expires_at)
		VALUES (?, ?, ?, ?, 'pending', ?, ?)`, approvalID, conversationID, toolCallID, parameterDigest, now.Unix(), expires.Unix()); err != nil {
		return Approval{}, err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'waiting_approval', revision = revision + 1, updated_at = ? WHERE id = ?`, now.Unix(), conversationID); err != nil {
		return Approval{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Approval{}, err
	}
	return Approval{ID: approvalID, ConversationID: conversationID, ToolCallID: toolCallID, ParameterDigest: parameterDigest, Status: "pending", RequestedAt: now, ExpiresAt: expires}, nil
}

func (s *Service) DecideApproval(ctx context.Context, actor Actor, conversationID, approvalID string, approve bool) (Approval, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Approval{}, err
	}
	defer transaction.Rollback()
	if err := ensureConversationOwnerTx(ctx, transaction, actor, conversationID); err != nil {
		return Approval{}, err
	}
	approval, err := approvalByIDTx(ctx, transaction, conversationID, approvalID)
	if err != nil {
		return Approval{}, err
	}
	now := s.now().UTC()
	if approval.Status != "pending" {
		return Approval{}, ErrApprovalInvalid
	}
	if !now.Before(approval.ExpiresAt) {
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'expired', decided_at = ? WHERE id = ? AND status = 'pending'`, now.Unix(), approvalID); err != nil {
			return Approval{}, err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'rejected', error_code = 'approval_expired', finished_at = ? WHERE id = ? AND status = 'waiting_approval'`, now.Unix(), approval.ToolCallID); err != nil {
			return Approval{}, err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ?`, now.Unix(), conversationID); err != nil {
			return Approval{}, err
		}
		if err := transaction.Commit(); err != nil {
			return Approval{}, err
		}
		return Approval{}, ErrApprovalExpired
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	result, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = ?, decided_at = ?, decided_by_user_id = ? WHERE id = ? AND status = 'pending'`, status, now.Unix(), actor.UserID, approvalID)
	if err != nil {
		return Approval{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Approval{}, ErrApprovalInvalid
	}
	if !approve {
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'rejected', error_code = 'approval_rejected', finished_at = ? WHERE id = ? AND status = 'waiting_approval'`, now.Unix(), approval.ToolCallID); err != nil {
			return Approval{}, err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ?`, now.Unix(), conversationID); err != nil {
			return Approval{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return Approval{}, err
	}
	approval.Status, approval.DecidedAt, approval.DecidedByUserID = status, &now, actor.UserID
	return approval, nil
}

func (s *Service) ConsumeApproval(ctx context.Context, actor Actor, conversationID, externalToolCallID, approvalID, digest string) error {
	if !validDigest(digest) {
		return ErrApprovalInvalid
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := ensureConversationOwnerTx(ctx, transaction, actor, conversationID); err != nil {
		return err
	}
	approval, err := approvalByIDTx(ctx, transaction, conversationID, approvalID)
	if err != nil {
		return err
	}
	if approval.ToolCallID != toolCallRecordID(conversationID, externalToolCallID) || subtleDigestCompare(approval.ParameterDigest, digest) == false {
		return ErrApprovalInvalid
	}
	if approval.Status == "rejected" {
		return ErrApprovalRejected
	}
	if approval.Status != "approved" {
		return ErrApprovalInvalid
	}
	now := s.now().UTC()
	if !now.Before(approval.ExpiresAt) {
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'expired', decided_at = ? WHERE id = ? AND status = 'approved'`, now.Unix(), approvalID); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'rejected', error_code = 'approval_expired', finished_at = ? WHERE id = ? AND status = 'waiting_approval'`, now.Unix(), approval.ToolCallID); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ?`, now.Unix(), conversationID); err != nil {
			return err
		}
		if err := transaction.Commit(); err != nil {
			return err
		}
		return ErrApprovalExpired
	}
	result, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'running' WHERE id = ? AND conversation_id = ? AND status = 'waiting_approval'`, approval.ToolCallID, conversationID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrApprovalInvalid
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ?`, s.now().UTC().Unix(), conversationID); err != nil {
		return err
	}
	return transaction.Commit()
}

func (s *Service) FinishToolCall(ctx context.Context, actor Actor, conversationID, externalToolCallID, status, errorCode, resultSummary string) error {
	if (status != "complete" && status != "error" && status != "rejected" && status != "cancelled" && status != "interrupted") || !validToolSummary(errorCode) || !validToolSummary(resultSummary) {
		return fmt.Errorf("%w: invalid tool result", ErrInvalidInput)
	}
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = ?, error_code = ?, result_summary = ?, finished_at = ?
		WHERE id = ? AND conversation_id = ? AND status IN ('running', 'waiting_approval')`, status, strings.TrimSpace(errorCode), strings.TrimSpace(resultSummary), s.now().UTC().Unix(), toolCallRecordID(conversationID, externalToolCallID), conversationID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrApprovalInvalid
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ? AND status = 'waiting_approval'`, s.now().UTC().Unix(), conversationID); err != nil {
		return err
	}
	return transaction.Commit()
}

func (s *Service) RecordToolCallResponse(ctx context.Context, actor Actor, conversationID, externalToolCallID, response string) (ToolCall, error) {
	responseJSON, validResponseJSON := normalizedToolJSON(response, "null", maxToolResponseJSONBytes)
	if strings.TrimSpace(actor.UserID) == "" || !toolCallPattern.MatchString(externalToolCallID) || !validResponseJSON {
		return ToolCall{}, fmt.Errorf("%w: invalid tool response", ErrInvalidInput)
	}
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return ToolCall{}, err
	}
	toolCallID := toolCallRecordID(conversationID, externalToolCallID)
	result, err := s.db.ExecContext(ctx, `UPDATE assistant_tool_calls SET response_json = ? WHERE id = ? AND conversation_id = ?`,
		responseJSON, toolCallID, conversationID)
	if err != nil {
		return ToolCall{}, fmt.Errorf("record assistant tool response: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ToolCall{}, ErrNotFound
	}
	return s.ToolCallByID(ctx, actor, conversationID, toolCallID)
}

func (s *Service) ToolCalls(ctx context.Context, actor Actor, conversationID string) ([]ToolCall, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, COALESCE(message_id, ''), body_offset, tool_name, target_summary, parameter_summary, request_json, response_json,
		status, error_code, result_summary, started_at, finished_at FROM assistant_tool_calls WHERE conversation_id = ? ORDER BY started_at, id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []ToolCall
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

func (s *Service) PendingApproval(ctx context.Context, actor Actor, conversationID string) (Approval, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return Approval{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT a.id, a.conversation_id, a.tool_call_id, a.parameter_digest, a.status, a.requested_at, a.expires_at, a.decided_at, a.decided_by_user_id
		FROM assistant_approvals a JOIN assistant_tool_calls t ON t.id = a.tool_call_id
		WHERE a.conversation_id = ? AND a.status IN ('pending', 'approved') AND t.status = 'waiting_approval'
		ORDER BY a.requested_at DESC LIMIT 1`, conversationID)
	return scanApproval(row)
}

func (s *Service) Approval(ctx context.Context, actor Actor, conversationID, approvalID string) (Approval, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return Approval{}, err
	}
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT id, conversation_id, tool_call_id, parameter_digest, status, requested_at, expires_at, decided_at, decided_by_user_id
		FROM assistant_approvals WHERE id = ? AND conversation_id = ?`, approvalID, conversationID))
}

func (s *Service) ToolCallByID(ctx context.Context, actor Actor, conversationID, id string) (ToolCall, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return ToolCall{}, err
	}
	return scanToolCall(s.db.QueryRowContext(ctx, `SELECT id, conversation_id, COALESCE(message_id, ''), body_offset, tool_name, target_summary, parameter_summary, request_json, response_json,
		status, error_code, result_summary, started_at, finished_at FROM assistant_tool_calls WHERE id = ? AND conversation_id = ?`, id, conversationID))
}

func (s *Service) CancelPendingApprovals(ctx context.Context, actor Actor, conversationID string) error {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return err
	}
	now := s.now().UTC().Unix()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'cancelled', error_code = 'approval_cancelled', finished_at = ?
		WHERE conversation_id = ? AND status IN ('running', 'waiting_approval') AND id IN
		(SELECT tool_call_id FROM assistant_approvals WHERE conversation_id = ? AND status IN ('pending', 'approved'))`, now, conversationID, conversationID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'cancelled', decided_at = ?
		WHERE conversation_id = ? AND status IN ('pending', 'approved') AND tool_call_id IN
		(SELECT id FROM assistant_tool_calls WHERE conversation_id = ? AND status = 'cancelled' AND error_code = 'approval_cancelled')`, now, conversationID, conversationID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ? AND status = 'waiting_approval'`, now, conversationID); err != nil {
		return err
	}
	return transaction.Commit()
}

// InvalidateApproval terminates one frozen action without executing it. It is
// used when authorization, parameters, or target state changes between the
// user's decision and the actual domain call.
func (s *Service) InvalidateApproval(ctx context.Context, actor Actor, conversationID, approvalID, errorCode string) error {
	if !validToolSummary(errorCode) || strings.TrimSpace(errorCode) == "" {
		return fmt.Errorf("%w: invalid approval error code", ErrInvalidInput)
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := ensureConversationOwnerTx(ctx, transaction, actor, conversationID); err != nil {
		return err
	}
	approval, err := approvalByIDTx(ctx, transaction, conversationID, approvalID)
	if err != nil {
		return err
	}
	if approval.Status != "pending" && approval.Status != "approved" {
		return ErrApprovalInvalid
	}
	now := s.now().UTC().Unix()
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_approvals SET status = 'cancelled', decided_at = ? WHERE id = ? AND status IN ('pending', 'approved')`, now, approvalID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_tool_calls SET status = 'error', error_code = ?, finished_at = ? WHERE id = ? AND status IN ('waiting_approval', 'running')`, strings.TrimSpace(errorCode), now, approval.ToolCallID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE assistant_conversations SET status = 'running', revision = revision + 1, updated_at = ? WHERE id = ? AND status = 'waiting_approval'`, now, conversationID); err != nil {
		return err
	}
	return transaction.Commit()
}

func ensureConversationOwnerTx(ctx context.Context, transaction *sql.Tx, actor Actor, conversationID string) error {
	var exists int
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM assistant_conversations WHERE id = ? AND owner_user_id = ?)`, conversationID, actor.UserID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return nil
}

func approvalByIDTx(ctx context.Context, transaction *sql.Tx, conversationID, approvalID string) (Approval, error) {
	return scanApproval(transaction.QueryRowContext(ctx, `SELECT id, conversation_id, tool_call_id, parameter_digest, status, requested_at, expires_at, decided_at, decided_by_user_id
		FROM assistant_approvals WHERE id = ? AND conversation_id = ?`, approvalID, conversationID))
}

type rowScanner interface{ Scan(...any) error }

func scanApproval(row rowScanner) (Approval, error) {
	var approval Approval
	var requested, expires int64
	var decided sql.NullInt64
	if err := row.Scan(&approval.ID, &approval.ConversationID, &approval.ToolCallID, &approval.ParameterDigest, &approval.Status, &requested, &expires, &decided, &approval.DecidedByUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Approval{}, ErrNotFound
		}
		return Approval{}, err
	}
	approval.RequestedAt, approval.ExpiresAt = time.Unix(requested, 0).UTC(), time.Unix(expires, 0).UTC()
	if decided.Valid {
		value := time.Unix(decided.Int64, 0).UTC()
		approval.DecidedAt = &value
	}
	return approval, nil
}

func scanToolCall(row rowScanner) (ToolCall, error) {
	var call ToolCall
	var started int64
	var finished sql.NullInt64
	if err := row.Scan(&call.ID, &call.ConversationID, &call.MessageID, &call.BodyOffset, &call.Name, &call.TargetSummary, &call.ParameterSummary, &call.RequestJSON, &call.ResponseJSON, &call.Status, &call.ErrorCode, &call.ResultSummary, &started, &finished); err != nil {
		return ToolCall{}, err
	}
	call.StartedAt = time.Unix(started, 0).UTC()
	if finished.Valid {
		value := time.Unix(finished.Int64, 0).UTC()
		call.FinishedAt = &value
	}
	return call, nil
}

func toolCallRecordID(conversationID, externalID string) string {
	digest := sha256.Sum256([]byte(conversationID + "\x00" + externalID))
	return "tool_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}

func validToolSummary(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= 512
}

func normalizedToolJSON(value, fallback string, maximumBytes int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return value, utf8.ValidString(value) && !strings.ContainsRune(value, 0) && len(value) <= maximumBytes && json.Valid([]byte(value))
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func subtleDigestCompare(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	if leftErr != nil || rightErr != nil || len(leftBytes) != len(rightBytes) {
		return false
	}
	var difference byte
	for index := range leftBytes {
		difference |= leftBytes[index] ^ rightBytes[index]
	}
	return difference == 0
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
