package toolbroker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestApprovalResponseUsesExtensionContractJSON(t *testing.T) {
	var buffer bytes.Buffer
	writeResponse(&buffer, Response{
		Status: StatusApproval,
		Approval: &Approval{
			ID: "approval-1", Title: "Check website now", Message: "Run the bounded check.",
		},
	})
	var payload struct {
		Approval struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Message string `json:"message"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Approval.ID != "approval-1" || payload.Approval.Title != "Check website now" || payload.Approval.Message != "Run the bounded check." {
		t.Fatalf("approval payload = %#v", payload.Approval)
	}
	if bytes.Contains(buffer.Bytes(), []byte(`"ID"`)) || bytes.Contains(buffer.Bytes(), []byte(`"Title"`)) || bytes.Contains(buffer.Bytes(), []byte(`"Message"`)) {
		t.Fatalf("approval response leaked Go field casing: %s", buffer.Bytes())
	}
}

func TestDecodeRequestRejectsDuplicateKeysAtEveryDepth(t *testing.T) {
	for _, record := range []string{
		`{"version":1,"version":1,"capability":"x","toolCallId":"t","tool":"get_host_status","parameters":{}}` + "\n",
		`{"version":1,"capability":"x","toolCallId":"t","tool":"get_host_status","parameters":{"limit":1,"limit":2}}` + "\n",
	} {
		if _, err := decodeRequest(strings.NewReader(record)); err == nil {
			t.Fatalf("decodeRequest(%q) accepted duplicate keys", record)
		}
	}
}

type fixtureExecutor struct{ calls chan Invocation }

func (executor *fixtureExecutor) Invoke(_ context.Context, invocation Invocation) Response {
	executor.calls <- invocation
	return Response{Status: StatusSuccess, Content: map[string]any{"ok": true}}
}

func TestSessionAuthenticatesBoundedVersionedToolCallsAndRevokesOnClose(t *testing.T) {
	executor := &fixtureExecutor{calls: make(chan Invocation, 1)}
	broker, err := New(t.TempDir(), executor)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	session, err := broker.Start(SessionBinding{RuntimeID: "runtime-1", UserID: "user-1", ConversationID: "conversation-1", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	response := callFixtureBroker(t, session.Endpoint, Request{
		Version: ProtocolVersion, Capability: session.Capability, ToolCallID: "tool-1", Tool: "get_host_status", Parameters: json.RawMessage(`{}`),
	})
	if response.Status != StatusSuccess {
		t.Fatalf("response = %#v", response)
	}
	select {
	case invocation := <-executor.calls:
		if invocation.Binding.UserID != "user-1" || invocation.Request.ToolCallID != "tool-1" {
			t.Fatalf("invocation = %#v", invocation)
		}
	case <-time.After(time.Second):
		t.Fatal("executor was not called")
	}

	wrong := callFixtureBroker(t, session.Endpoint, Request{Version: ProtocolVersion, Capability: "wrong", ToolCallID: "tool-2", Tool: "get_host_status", Parameters: json.RawMessage(`{}`)})
	if wrong.Status != StatusForbidden {
		t.Fatalf("wrong capability response = %#v", wrong)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout(session.network, session.Endpoint, 200*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("revoked broker endpoint still accepts connections")
	}
}

func TestSessionCloseInterruptsStalledLocalConnections(t *testing.T) {
	broker, err := New(t.TempDir(), &fixtureExecutor{calls: make(chan Invocation, 1)})
	if err != nil {
		t.Fatal(err)
	}
	session, err := broker.Start(SessionBinding{RuntimeID: "runtime-1", UserID: "user-1", ConversationID: "conversation-1", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := dialEndpoint(session.network, session.Endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	deadline := time.Now().Add(time.Second)
	for {
		session.connectionMu.Lock()
		accepted := len(session.activeConnections) == 1
		session.connectionMu.Unlock()
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("broker did not accept the stalled connection")
		}
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("closing a stalled connection took %s", elapsed)
	}
	_ = broker.Close()
}

func callFixtureBroker(t *testing.T, endpoint string, request Request) Response {
	t.Helper()
	network := "unix"
	if len(endpoint) >= 2 && endpoint[:2] == `\\` {
		network = "pipe"
	}
	connection, err := dialEndpoint(network, endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}
