package privilegebroker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"scriptboard/internal/passkey"
)

func TestRemotePasskeyUsesTypedBrokerOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixturePasskeyService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, Passkeys: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	remote := NewRemotePasskey(client)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "passkey-domain-test"})
	user, err := remote.User("administrator", "admin")
	if err != nil || user.ID != "administrator" || user.Name != "admin" || len(user.Credentials) != 1 {
		t.Fatalf("user=%+v error=%v", user, err)
	}
	views, err := remote.List("administrator")
	if err != nil || len(views) != 1 || views[0].Name != "Security key" {
		t.Fatalf("views=%+v error=%v", views, err)
	}
	credential := webauthn.Credential{ID: []byte{4, 5, 6}, PublicKey: []byte{7, 8, 9}}
	if err := remote.Add("administrator", "Laptop", credential); err == nil {
		t.Fatal("passkey enrollment without session authorization succeeded")
	}
	if err := remote.AddContext(authorized, "administrator", "Laptop", credential); err != nil {
		t.Fatal(err)
	}
	if err := remote.Update("administrator", credential); err != nil {
		t.Fatal(err)
	}
	if err := remote.DeleteContext(authorized, "administrator", "040506"); err != nil {
		t.Fatal(err)
	}
	if err := remote.ResetContext(authorized, "administrator"); err != nil {
		t.Fatal(err)
	}
	if service.userName != "admin" || service.addName != "Laptop" || string(service.addCredential.ID) != string(credential.ID) ||
		string(service.updateCredential.ID) != string(credential.ID) || service.deleteID != "040506" || service.resetUser != "administrator" {
		t.Fatalf("Broker did not preserve typed passkey fields: %+v", service)
	}
}

func TestRemotePasskeyPreservesDomainErrors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{},
		Passkeys: &fixturePasskeyService{addErr: passkey.ErrDuplicateCredential, deleteErr: passkey.ErrCredentialNotFound},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	remote := NewRemotePasskey(client)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "passkey-error-test"})
	credential := webauthn.Credential{ID: []byte{1}}
	if err := remote.AddContext(authorized, "administrator", "duplicate", credential); !errors.Is(err, passkey.ErrDuplicateCredential) {
		t.Fatalf("add error=%v", err)
	}
	if err := remote.DeleteContext(authorized, "administrator", "01"); !errors.Is(err, passkey.ErrCredentialNotFound) {
		t.Fatalf("delete error=%v", err)
	}
}

func TestPasskeyMutationRequiresAuthorizedSessionBeforeStateChange(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixturePasskeyService{}
	authorizer := &fixtureAuthorizer{err: errors.New("step-up expired")}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: authorizer, Executor: &fixtureExecutor{}, Passkeys: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	remote := NewRemotePasskey(client)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "passkey-denied-test"})
	if err := remote.AddContext(authorized, "administrator", "Laptop", webauthn.Credential{ID: []byte{1}}); err == nil {
		t.Fatal("expired session authorized passkey mutation")
	}
	if authorizer.calls != 1 || service.addName != "" {
		t.Fatalf("authorizer calls=%d passkey mutation reached service=%q", authorizer.calls, service.addName)
	}
}

func TestPasskeyMutationRejectsCrossUserSessionBinding(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixturePasskeyService{}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "other-user"}}, Executor: &fixtureExecutor{}, Passkeys: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	remote := NewRemotePasskey(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "passkey-cross-user-test"})
	if err := remote.AddContext(authorized, "administrator", "Laptop", webauthn.Credential{ID: []byte{1}}); err == nil {
		t.Fatal("cross-user session authorized passkey mutation")
	}
	if service.addName != "" {
		t.Fatalf("cross-user mutation reached passkey service: %q", service.addName)
	}
}

func TestPasskeyProtocolRejectsGenericSecretAndUnrelatedFields(t *testing.T) {
	credential := webauthn.Credential{ID: []byte{1}}
	valid := wireRequest{Version: ProtocolVersion, Operation: operationPasskeyAdd, RequestID: "passkey-test", SessionToken: strings.Repeat("s", 32), PasskeyUserID: "administrator", PasskeyName: "key", PasskeyCredential: &credential}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.Action = ActionUFWEnable; return value }(),
		func() wireRequest {
			value := valid
			value.Parameters = []byte(`{"ciphertext":"not-allowed"}`)
			return value
		}(),
		func() wireRequest { value := valid; value.MFACode = "not-allowed"; return value }(),
		{Version: ProtocolVersion, Operation: operationPasskeyUser, RequestID: "passkey-test", PasskeyUserID: "administrator"},
		{Version: ProtocolVersion, Operation: operationPasskeyDelete, RequestID: "passkey-test", PasskeyUserID: "administrator", PasskeyCredentialID: "not-hex"},
		{Version: ProtocolVersion, Operation: operationPasskeyReset, RequestID: "passkey-test", PasskeyUserID: "administrator"},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("passkey request accepted unrelated or incomplete fields: %+v", request)
		}
	}
}

type fixturePasskeyService struct {
	userName, addName, deleteID, resetUser string
	addCredential, updateCredential        webauthn.Credential
	addErr, deleteErr                      error
}

func (service *fixturePasskeyService) User(userID, username string) (passkey.User, error) {
	service.userName = username
	return passkey.User{ID: userID, Name: username, Credentials: []webauthn.Credential{{ID: []byte{1, 2, 3}}}}, nil
}

func (*fixturePasskeyService) List(string) ([]passkey.CredentialView, error) {
	return []passkey.CredentialView{{ID: "010203", Name: "Security key"}}, nil
}

func (service *fixturePasskeyService) Add(_ string, name string, credential webauthn.Credential) error {
	service.addName, service.addCredential = name, credential
	return service.addErr
}

func (service *fixturePasskeyService) Update(_ string, credential webauthn.Credential) error {
	service.updateCredential = credential
	return nil
}

func (service *fixturePasskeyService) Delete(_ string, credentialID string) error {
	service.deleteID = credentialID
	return service.deleteErr
}

func (service *fixturePasskeyService) Reset(userID string) error {
	service.resetUser = userID
	return nil
}
