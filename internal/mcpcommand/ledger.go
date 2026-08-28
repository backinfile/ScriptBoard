// Package mcpcommand owns recoverable idempotency for machine-facing commands.
package mcpcommand

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const idempotencyWindow = 24 * time.Hour

var (
	ErrRequestPending = errors.New("MCP request is already processing")
	ErrOutcomeUnknown = errors.New("MCP request executed but its result could not be persisted")
)

type Key struct {
	UserID, ClientID, Tool, RequestID string
}

type Ledger struct {
	db  *sql.DB
	now func() time.Time
}

type storedResult struct {
	State  string          `json:"_scriptboard_state"`
	Result json.RawMessage `json:"result,omitempty"`
}

type uncachedOutcome struct{ value any }

// Uncached returns an operation outcome without consuming its idempotency key.
// Use it when no command was executed and the caller may retry after confirming
// or resolving a transient precondition.
func Uncached(value any) any { return uncachedOutcome{value: value} }

var processingResult = mustStoredResult(storedResult{State: "processing"})

func NewLedger(db *sql.DB, now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{db: db, now: now}
}

func (ledger *Ledger) Execute(ctx context.Context, key Key, operation func() (any, error)) (any, error) {
	if ledger == nil || ledger.db == nil || operation == nil || key.UserID == "" || key.ClientID == "" || key.Tool == "" || key.RequestID == "" {
		return nil, errors.New("complete MCP idempotency input is required")
	}
	claimedAt, cached, claimed, err := ledger.claim(ctx, key)
	if err != nil || !claimed {
		return cached, err
	}
	result, err := operation()
	if err != nil {
		ledger.releaseFailedOperation(ctx, key, claimedAt)
		return nil, err
	}
	if outcome, ok := result.(uncachedOutcome); ok {
		if err := ledger.releaseClaim(ctx, key, claimedAt); err != nil {
			return nil, fmt.Errorf("release unexecuted MCP request: %w", err)
		}
		return outcome.value, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode MCP command result: %w", err)
	}
	completed := mustStoredResult(storedResult{State: "completed", Result: encoded})
	update, err := ledger.db.ExecContext(ctx, `UPDATE mcp_idempotency SET result_json=?
		WHERE user_id=? AND client_id=? AND tool_name=? AND request_id=? AND created_at=? AND result_json=?`,
		completed, key.UserID, key.ClientID, key.Tool, key.RequestID, claimedAt, processingResult)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	changed, err := update.RowsAffected()
	if err != nil || changed != 1 {
		return nil, fmt.Errorf("%w: idempotency claim was lost", ErrOutcomeUnknown)
	}
	return result, nil
}

func (ledger *Ledger) claim(ctx context.Context, key Key) (int64, any, bool, error) {
	now := ledger.now().UTC()
	claimedAt := now.UnixNano()
	insert, err := ledger.db.ExecContext(ctx, `INSERT OR IGNORE INTO mcp_idempotency
		(user_id,client_id,tool_name,request_id,result_json,created_at) VALUES(?,?,?,?,?,?)`,
		key.UserID, key.ClientID, key.Tool, key.RequestID, processingResult, claimedAt)
	if err != nil {
		return 0, nil, false, fmt.Errorf("claim MCP request: %w", err)
	}
	inserted, err := insert.RowsAffected()
	if err != nil {
		return 0, nil, false, fmt.Errorf("inspect MCP request claim: %w", err)
	}
	if inserted == 1 {
		return claimedAt, nil, true, nil
	}

	var body string
	var createdAt int64
	if err := ledger.db.QueryRowContext(ctx, `SELECT result_json,created_at FROM mcp_idempotency
		WHERE user_id=? AND client_id=? AND tool_name=? AND request_id=?`,
		key.UserID, key.ClientID, key.Tool, key.RequestID).Scan(&body, &createdAt); err != nil {
		return 0, nil, false, fmt.Errorf("load MCP request claim: %w", err)
	}
	if createdAt >= now.Add(-idempotencyWindow).UnixNano() {
		cached, err := decodeStoredResult(body)
		return 0, cached, false, err
	}

	// 修复过期记录仍占用主键导致后续重试重复执行：用旧时间戳作条件，原子开启新的幂等窗口。
	update, err := ledger.db.ExecContext(ctx, `UPDATE mcp_idempotency SET result_json=?,created_at=?
		WHERE user_id=? AND client_id=? AND tool_name=? AND request_id=? AND created_at=?`,
		processingResult, claimedAt, key.UserID, key.ClientID, key.Tool, key.RequestID, createdAt)
	if err != nil {
		return 0, nil, false, fmt.Errorf("renew MCP request claim: %w", err)
	}
	changed, err := update.RowsAffected()
	if err != nil {
		return 0, nil, false, fmt.Errorf("inspect renewed MCP request claim: %w", err)
	}
	if changed != 1 {
		return 0, nil, false, ErrRequestPending
	}
	return claimedAt, nil, true, nil
}

func decodeStoredResult(body string) (any, error) {
	var stored storedResult
	if json.Unmarshal([]byte(body), &stored) == nil && stored.State != "" {
		switch stored.State {
		case "processing":
			return nil, ErrRequestPending
		case "completed":
			var result any
			if len(stored.Result) == 0 || json.Unmarshal(stored.Result, &result) != nil {
				return nil, ErrOutcomeUnknown
			}
			return result, nil
		default:
			return nil, ErrOutcomeUnknown
		}
	}
	var legacy any
	if json.Unmarshal([]byte(body), &legacy) != nil {
		return nil, ErrOutcomeUnknown
	}
	return legacy, nil
}

func (ledger *Ledger) releaseFailedOperation(ctx context.Context, key Key, claimedAt int64) {
	_ = ledger.releaseClaim(ctx, key, claimedAt)
}

func (ledger *Ledger) releaseClaim(ctx context.Context, key Key, claimedAt int64) error {
	_, err := ledger.db.ExecContext(ctx, `DELETE FROM mcp_idempotency
		WHERE user_id=? AND client_id=? AND tool_name=? AND request_id=? AND created_at=? AND result_json=?`,
		key.UserID, key.ClientID, key.Tool, key.RequestID, claimedAt, processingResult)
	return err
}

func mustStoredResult(value storedResult) string {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(body)
}
