package runtimehost

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"scriptboard/internal/assistant/pirpc"
)

func TestIsolatedRuntimeHostTracerBullet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	versionRoot := filepath.Join(stateRoot, "assistant", "runtime", "versions", "fixture")
	if err := os.MkdirAll(versionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executableName := "pi"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	executable := filepath.Join(versionRoot, executableName)
	build := exec.Command("go", "build", "-o", executable, "../pirpc/testdata/fakepi")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Pi: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "assistant", "runtime", "active.json"), []byte(`{"version":"fixture","rpcContract":1,"executable":"`+executableName+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(t.TempDir(), "runtime.sock")
	if runtime.GOOS == "windows" {
		var err error
		endpoint, err = DefaultEndpoint(stateRoot)
		if err != nil {
			t.Fatal(err)
		}
	}
	transport, err := Listen(TransportOptions{StateRoot: stateRoot, Endpoint: endpoint, DevelopmentCurrentUser: true})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{Listener: transport.Listener, VerifyPeer: transport.VerifyPeer, StateRoot: stateRoot, Maximum: 1})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Close(ctx)
		_ = transport.Close()
	})

	supervisor := pirpc.NewSupervisorWithLauncher(1, NewClientLauncher(Dial(transport.Endpoint)))
	request := pirpc.RuntimeLaunchRequest{
		UserID: "user", ConversationID: "conversation", Provider: "openai-compatible", Model: "fixture-model",
		ProviderProxyEndpoint: "http://127.0.0.1:11434/v1", ProviderCapability: "session-provider-capability-fixture-value",
		SystemPrompt: "bounded",
	}
	session, err := supervisor.StartRuntime("conversation", request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := session.Client().Prompt(ctx, "prompt-1", "hello")
	if err != nil || response.Success == nil || !*response.Success {
		t.Fatalf("prompt response = %#v, error = %v", response, err)
	}
	if err := supervisor.Stop(ctx, "conversation"); err != nil {
		t.Fatal(err)
	}
}

func TestClientLauncherRejectsGenericExecutableSpecifications(t *testing.T) {
	launcher := NewClientLauncher(func(context.Context) (net.Conn, error) {
		t.Fatal("generic launch must fail before dialing")
		return nil, nil
	})
	if _, err := launcher.LaunchSpec(context.Background(), pirpc.LaunchSpec{Executable: `C:\untrusted.exe`}); err == nil {
		t.Fatal("generic executable launch was accepted")
	}
}

func TestRuntimeHostRejectsMismatchedProcessKey(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	listener := &singleConnectionListener{connection: serverSide}
	server, err := NewServer(ServerOptions{Listener: listener, StateRoot: t.TempDir(), Maximum: 1})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(func() {
		_ = clientSide.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	if err := json.NewEncoder(clientSide).Encode(launchEnvelope{
		Key: "different", Request: pirpc.RuntimeLaunchRequest{ConversationID: "conversation"},
	}); err != nil {
		t.Fatal(err)
	}
	var response launchResponse
	if err := json.NewDecoder(clientSide).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == "" {
		t.Fatalf("mismatched process key response = %#v", response)
	}
}

type singleConnectionListener struct {
	connection net.Conn
	once       sync.Once
}

func (listener *singleConnectionListener) Accept() (net.Conn, error) {
	var connection net.Conn
	listener.once.Do(func() { connection = listener.connection })
	if connection == nil {
		return nil, net.ErrClosed
	}
	return connection, nil
}

func (listener *singleConnectionListener) Close() error   { return nil }
func (listener *singleConnectionListener) Addr() net.Addr { return listener.connection.LocalAddr() }
