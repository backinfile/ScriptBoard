package assistant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	maxConversationWindowMessages     = 200
	maxConversationWindowMessageBytes = 2 << 20
	maxConversationWindowToolCalls    = 128
	maxConversationWindowToolBytes    = 4 << 20
)

type ConversationHistoryWindow struct {
	Messages  []Message
	ToolCalls []ToolCall
	Truncated bool
}

// ConversationWindow returns the newest bounded transcript data in display
// order. Persistent conversation history remains complete in SQLite.
func (s *Service) ConversationWindow(ctx context.Context, actor Actor, conversationID string) (ConversationHistoryWindow, error) {
	if err := s.ensureConversationOwner(ctx, actor, conversationID); err != nil {
		return ConversationHistoryWindow{}, err
	}
	messages, messageTruncated, err := s.conversationMessageWindow(ctx, conversationID)
	if err != nil {
		return ConversationHistoryWindow{}, err
	}
	toolCalls, toolTruncated, err := s.conversationToolWindow(ctx, conversationID, messages)
	if err != nil {
		return ConversationHistoryWindow{}, err
	}
	return ConversationHistoryWindow{Messages: messages, ToolCalls: toolCalls, Truncated: messageTruncated || toolTruncated}, nil
}

func (s *Service) conversationMessageWindow(ctx context.Context, conversationID string) ([]Message, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, sequence, role, body, status, created_at, finished_at
		FROM assistant_messages WHERE conversation_id = ? ORDER BY sequence DESC LIMIT ?`, conversationID, maxConversationWindowMessages+1)
	if err != nil {
		return nil, false, fmt.Errorf("read assistant conversation window: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0, maxConversationWindowMessages)
	bytesUsed := 0
	truncated := false
	for rows.Next() {
		var message Message
		var createdAt int64
		var finishedAt sql.NullInt64
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Sequence, &message.Role, &message.Body, &message.Status, &createdAt, &finishedAt); err != nil {
			return nil, false, fmt.Errorf("scan assistant conversation window: %w", err)
		}
		if len(messages) >= maxConversationWindowMessages || bytesUsed+len(message.Body) > maxConversationWindowMessageBytes {
			truncated = true
			break
		}
		message.CreatedAt = time.Unix(0, createdAt).UTC()
		if finishedAt.Valid {
			value := time.Unix(0, finishedAt.Int64).UTC()
			message.FinishedAt = &value
		}
		bytesUsed += len(message.Body)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate assistant conversation window: %w", err)
	}
	reverseMessages(messages)
	return messages, truncated, nil
}

func (s *Service) conversationToolWindow(ctx context.Context, conversationID string, messages []Message) ([]ToolCall, bool, error) {
	messageIDs := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		messageIDs[message.ID] = struct{}{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, COALESCE(message_id, ''), body_offset, tool_name, target_summary, parameter_summary, request_json, response_json,
		status, error_code, result_summary, started_at, finished_at FROM assistant_tool_calls WHERE conversation_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, conversationID, maxConversationWindowToolCalls+1)
	if err != nil {
		return nil, false, fmt.Errorf("read assistant tool window: %w", err)
	}
	defer rows.Close()
	calls := make([]ToolCall, 0, maxConversationWindowToolCalls)
	bytesUsed := 0
	truncated := false
	for rows.Next() {
		call, scanErr := scanToolCall(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		if _, visible := messageIDs[call.MessageID]; !visible {
			continue
		}
		size := len(call.RequestJSON) + len(call.ResponseJSON) + len(call.ResultSummary) + len(call.ParameterSummary) + len(call.TargetSummary)
		if len(calls) >= maxConversationWindowToolCalls || bytesUsed+size > maxConversationWindowToolBytes {
			truncated = true
			break
		}
		bytesUsed += size
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	reverseToolCalls(calls)
	return calls, truncated, nil
}

func reverseMessages(values []Message) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseToolCalls(values []ToolCall) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
