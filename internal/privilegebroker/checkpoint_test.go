package privilegebroker

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestRemoteCheckpointUsesTypedBrokerOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureCheckpointService{verifyEventID: 7, writeEventID: 9}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{}, Executor: &fixtureExecutor{}, Checkpoint: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", listener.Addr().String())
	}})
	checkpoint := NewRemoteCheckpoint(client)
	if err := checkpoint.VerifyOrBootstrap(context.Background(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := checkpoint.CheckpointEventID(); got != 7 {
		t.Fatalf("verified checkpoint event=%d", got)
	}
	if err := checkpoint.Write(context.Background(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := checkpoint.CheckpointEventID(); got != 9 {
		t.Fatalf("written checkpoint event=%d", got)
	}
	if service.verifyCalls != 1 || service.writeCalls != 1 {
		t.Fatalf("verify calls=%d write calls=%d", service.verifyCalls, service.writeCalls)
	}
}

func TestCheckpointProtocolRejectsGenericSecretOrActionFields(t *testing.T) {
	for _, request := range []wireRequest{
		{Version: ProtocolVersion, Operation: operationCheckpointWrite, RequestID: "checkpoint-test", SessionToken: "not-allowed"},
		{Version: ProtocolVersion, Operation: operationCheckpointWrite, RequestID: "checkpoint-test", Action: ActionUFWEnable},
		{Version: ProtocolVersion, Operation: operationCheckpointWrite, RequestID: "checkpoint-test", Parameters: []byte(`{"secret":"not-allowed"}`)},
	} {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("checkpoint request accepted unrelated fields: %+v", request)
		}
	}
}

type fixtureCheckpointService struct {
	verifyEventID, writeEventID int64
	verifyCalls, writeCalls     int
}

func (service *fixtureCheckpointService) Verify(context.Context) (int64, error) {
	service.verifyCalls++
	return service.verifyEventID, nil
}

func (service *fixtureCheckpointService) Write(context.Context) (int64, error) {
	service.writeCalls++
	return service.writeEventID, nil
}
