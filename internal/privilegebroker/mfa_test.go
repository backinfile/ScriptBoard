package privilegebroker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/mfa"
)

func TestRemoteMFAUsesTypedBrokerOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureMFAService{verifyResult: true}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, MFA: service,
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
	remote := NewRemoteMFA(client)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mfa-domain-test"})
	status, err := remote.Status("administrator")
	if err != nil || !status.Enabled || status.RecoveryCodes != 4 {
		t.Fatalf("status=%+v error=%v", status, err)
	}
	if _, err := remote.Begin("administrator", "admin@example.test"); err == nil {
		t.Fatal("MFA enrollment without session authorization succeeded")
	}
	enrollment, err := remote.BeginContext(authorized, "administrator", "admin@example.test")
	if err != nil || enrollment.Secret != "ENROLLMENT" || enrollment.URI != "otpauth://fixture" {
		t.Fatalf("enrollment=%+v error=%v", enrollment, err)
	}
	codes, err := remote.ConfirmContext(authorized, "administrator", "123456")
	if err != nil || len(codes) != 2 || codes[0] != "recovery-one" {
		t.Fatalf("codes=%v error=%v", codes, err)
	}
	verified, err := remote.Verify("administrator", "654321")
	if err != nil || !verified {
		t.Fatalf("verified=%v error=%v", verified, err)
	}
	if err := remote.ResetContext(authorized, "administrator"); err != nil {
		t.Fatal(err)
	}
	if service.statusUser != "administrator" || service.beginAccount != "admin@example.test" || service.confirmCode != "123456" || service.verifyCode != "654321" || service.resetUser != "administrator" {
		t.Fatalf("Broker did not preserve typed MFA fields: %+v", service)
	}
}

func TestRemoteMFAEmptyVerificationIsAnAuthenticationMissWithoutIPC(t *testing.T) {
	dialed := false
	remote := NewRemoteMFA(NewClient(ClientOptions{Dial: func(context.Context) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected Broker dial")
	}}))
	for _, candidate := range []string{"", " \t\r\n"} {
		verified, err := remote.Verify("administrator", candidate)
		if err != nil || verified {
			t.Fatalf("candidate=%q verified=%v error=%v", candidate, verified, err)
		}
	}
	if dialed {
		t.Fatal("empty MFA verification reached the Broker protocol")
	}
}

func TestBrokerRateLimitsMFAFailuresBeforeSecretStoreVerification(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fixtureMFAService{}
	now := time.Now().UTC()
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, MFA: service, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	remote := NewRemoteMFA(NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}}))
	for attempt := 0; attempt < maxMFAVerifyFailures+2; attempt++ {
		verified, err := remote.Verify("administrator", "000000")
		if err != nil || verified {
			t.Fatalf("attempt=%d verified=%v error=%v", attempt, verified, err)
		}
	}
	if service.verifyCalls != maxMFAVerifyFailures {
		t.Fatalf("secret store verify calls=%d, want %d", service.verifyCalls, maxMFAVerifyFailures)
	}
}

func TestRemoteMFAPreservesDomainErrors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{actor: Actor{UserID: "administrator"}}, Executor: &fixtureExecutor{}, MFA: &fixtureMFAService{beginErr: mfa.ErrAlreadyEnabled, verifyErr: mfa.ErrInvalidCode},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Close()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	remote := NewRemoteMFA(client)
	authorized := WithAuthorization(context.Background(), Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mfa-error-test"})
	if _, err := remote.BeginContext(authorized, "administrator", "admin"); !errors.Is(err, mfa.ErrAlreadyEnabled) {
		t.Fatalf("begin error=%v", err)
	}
	if _, err := remote.Verify("administrator", "bad"); !errors.Is(err, mfa.ErrInvalidCode) {
		t.Fatalf("verify error=%v", err)
	}
}

func TestMFAProtocolRejectsGenericSecretOrActionFields(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationMFAStatus, RequestID: "mfa-test", MFAUserID: "administrator"}
	requests := []wireRequest{
		func() wireRequest { value := valid; value.Action = ActionUFWEnable; return value }(),
		func() wireRequest {
			value := valid
			value.Parameters = []byte(`{"ciphertext":"not-allowed"}`)
			return value
		}(),
		func() wireRequest { value := valid; value.MFACode = "not-allowed"; return value }(),
		{Version: ProtocolVersion, Operation: operationMFAVerify, RequestID: "mfa-test", MFAUserID: "administrator"},
		{Version: ProtocolVersion, Operation: operationMFAReset, RequestID: "mfa-test", MFAUserID: "administrator"},
	}
	for _, request := range requests {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("MFA request accepted unrelated or incomplete fields: %+v", request)
		}
	}
}

type fixtureMFAService struct {
	statusUser, beginAccount, confirmCode, verifyCode, resetUser string
	beginErr, verifyErr                                          error
	verifyResult                                                 bool
	verifyCalls                                                  int
}

func (service *fixtureMFAService) Status(userID string) (mfa.Status, error) {
	service.statusUser = userID
	return mfa.Status{Enabled: true, RecoveryCodes: 4}, nil
}

func (service *fixtureMFAService) Begin(_ string, account string) (mfa.Enrollment, error) {
	service.beginAccount = account
	return mfa.Enrollment{Secret: "ENROLLMENT", URI: "otpauth://fixture"}, service.beginErr
}

func (service *fixtureMFAService) Confirm(_ string, code string) ([]string, error) {
	service.confirmCode = code
	return []string{"recovery-one", "recovery-two"}, nil
}

func (service *fixtureMFAService) Verify(_ string, code string) (bool, error) {
	service.verifyCode = code
	service.verifyCalls++
	return service.verifyResult, service.verifyErr
}

func (service *fixtureMFAService) Reset(userID string) error {
	service.resetUser = userID
	return nil
}
