package mcpcommand

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openLedgerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE mcp_idempotency (
		user_id TEXT NOT NULL, client_id TEXT NOT NULL, tool_name TEXT NOT NULL, request_id TEXT NOT NULL,
		result_json TEXT NOT NULL, created_at INTEGER NOT NULL,
		PRIMARY KEY(user_id, client_id, tool_name, request_id)
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExpiredRequestStartsOneNewIdempotencyWindow(t *testing.T) {
	db := openLedgerTestDB(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	key := Key{UserID: "u1", ClientID: "c1", Tool: "start", RequestID: "request-1"}
	if _, err := db.Exec(`INSERT INTO mcp_idempotency(user_id,client_id,tool_name,request_id,result_json,created_at) VALUES(?,?,?,?,?,?)`, key.UserID, key.ClientID, key.Tool, key.RequestID, `{"run_id":"old"}`, now.Add(-25*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	ledger := NewLedger(db, func() time.Time { return now })
	executions := 0
	operation := func() (any, error) {
		executions++
		return map[string]any{"run_id": "new"}, nil
	}
	first, err := ledger.Execute(context.Background(), key, operation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ledger.Execute(context.Background(), key, operation)
	if err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("executions=%d, want 1", executions)
	}
	for index, result := range []any{first, second} {
		body, ok := result.(map[string]any)
		if !ok || body["run_id"] != "new" {
			t.Fatalf("result[%d]=%#v, want new Run", index, result)
		}
	}
}

func TestCompletionFailureLeavesRequestNonExecutable(t *testing.T) {
	db := openLedgerTestDB(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_mcp_completion BEFORE UPDATE OF result_json ON mcp_idempotency
		WHEN NEW.result_json NOT LIKE '%"_scriptboard_state":"processing"%'
		BEGIN SELECT RAISE(FAIL, 'completion unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	ledger := NewLedger(db, time.Now)
	key := Key{UserID: "u1", ClientID: "c1", Tool: "start", RequestID: "request-2"}
	executions := 0
	operation := func() (any, error) {
		executions++
		return map[string]any{"run_id": "r1"}, nil
	}
	if _, err := ledger.Execute(context.Background(), key, operation); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("first error=%v, want ErrOutcomeUnknown", err)
	}
	if _, err := ledger.Execute(context.Background(), key, operation); !errors.Is(err, ErrRequestPending) {
		t.Fatalf("retry error=%v, want ErrRequestPending", err)
	}
	if executions != 1 {
		t.Fatalf("executions=%d, want 1", executions)
	}
}

func TestConcurrentRequestCannotExecuteWhileClaimIsActive(t *testing.T) {
	db := openLedgerTestDB(t)
	ledger := NewLedger(db, time.Now)
	key := Key{UserID: "u1", ClientID: "c1", Tool: "start", RequestID: "request-3"}
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	var executions atomic.Int32
	go func() {
		_, err := ledger.Execute(context.Background(), key, func() (any, error) {
			executions.Add(1)
			close(started)
			<-release
			return map[string]any{"run_id": "r1"}, nil
		})
		firstDone <- err
	}()
	<-started
	if _, err := ledger.Execute(context.Background(), key, func() (any, error) {
		executions.Add(1)
		return nil, nil
	}); !errors.Is(err, ErrRequestPending) {
		t.Fatalf("concurrent error=%v, want ErrRequestPending", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 1 {
		t.Fatalf("executions=%d, want 1", executions.Load())
	}
}
