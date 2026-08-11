package pirpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateRuntimeTracerBullet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the deterministic fake Pi executable")
	}
	stateRoot := t.TempDir()
	versionRoot := filepath.Join(stateRoot, "assistant", "runtime", "versions", "fixture")
	if err := os.MkdirAll(versionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(versionRoot, runtimeExecutableName())
	build := exec.Command("go", "build", "-o", executable, "./testdata/fakepi")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Pi: %v\n%s", err, output)
	}
	activeRoot := filepath.Join(stateRoot, "assistant", "runtime")
	if err := os.WriteFile(filepath.Join(activeRoot, "active.json"), []byte(`{"version":"fixture","rpcContract":1,"executable":"`+runtimeExecutableName()+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	managedRuntime, err := ResolveActiveRuntime(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := PrepareLaunch(LaunchInput{
		StateRoot: stateRoot, Executable: managedRuntime.Executable,
		UserID: "user", ConversationID: "conversation", Provider: "openai-compatible", Model: "fixture-model",
		Endpoint: "http://127.0.0.1:11434/v1", APIKey: "fixture-key", SystemPrompt: "bounded",
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor(1)
	t.Cleanup(func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = supervisor.Close(closeContext)
	})
	session, err := supervisor.Start("conversation", spec)
	if err != nil {
		t.Fatal(err)
	}
	// The full repository test suite starts several subprocess fixtures in
	// parallel on Windows. Start the protocol budget only after the fake Pi
	// process exists so host process-launch delays do not consume it.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := session.Client().Prompt(ctx, "prompt-1", "hello")
	if err != nil || response.Success == nil || !*response.Success {
		t.Fatalf("prompt response = %#v, error = %v, stderr = %s", response, err, session.StderrTail())
	}
	var text string
	settled := false
	for !settled {
		select {
		case event := <-session.Client().Events():
			if delta, ok := event.TextDelta(); ok {
				text += delta
			}
			settled = event.Settled()
		case <-ctx.Done():
			t.Fatalf("wait for fake Pi events: %v, stderr = %s", ctx.Err(), session.StderrTail())
		}
	}
	if text != "fixture response" {
		t.Fatalf("streamed text = %q", text)
	}
	if err := supervisor.Stop(ctx, "conversation"); err != nil {
		t.Fatal(err)
	}
}
