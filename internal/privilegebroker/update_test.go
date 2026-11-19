package privilegebroker

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"

	updatepkg "scriptboard/internal/update"
)

func TestUpdateHandoffRejectsExecutablePathsAndUnprivilegedSessions(t *testing.T) {
	request := wireRequest{
		Version: ProtocolVersion, Operation: operationUpdateHandoff, RequestID: "update-test",
		SessionToken: "fixture-session-token", Resource: "C:/ProgramData/updater.exe",
	}
	if err := validateWireRequest(request); err == nil {
		t.Fatal("update handoff accepted a caller-supplied executable path")
	}
	request.Resource = strings.Repeat("a", 24)
	if err := validateWireRequest(request); err != nil {
		t.Fatal(err)
	}
	authorizer := &fixtureAuthorizer{actor: Actor{Role: "operator"}}
	server := &Server{
		updates:    updatepkg.NewManager(updatepkg.ManagerConfig{StateRoot: t.TempDir()}),
		authorizer: authorizer, errorLogger: log.New(io.Discard, "", 0),
	}
	response := server.updateOperation(context.Background(), request)
	if response.ErrorCode != "authorization_failed" || authorizer.calls != 1 {
		t.Fatalf("operator handoff got error %q, auth calls=%d", response.ErrorCode, authorizer.calls)
	}
}
