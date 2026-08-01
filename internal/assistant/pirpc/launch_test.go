package pirpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPrepareLaunchUsesPrivateRuntimeAndConversationDirectories(t *testing.T) {
	stateRoot := t.TempDir()
	executable := filepath.Join(stateRoot, "assistant", "runtime", "versions", "0.83.0", runtimeExecutableName())
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}

	spec, err := PrepareLaunch(LaunchInput{
		StateRoot: stateRoot, Executable: executable, UserID: "user_1", ConversationID: "conversation_1",
		Provider: "openai-compatible", Model: "gpt-local", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "secret-value",
		SystemPrompt: "bounded assistant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Executable != executable {
		t.Fatalf("executable = %q", spec.Executable)
	}
	if slices.Contains(spec.Args, "--continue") {
		t.Fatalf("a new conversation unexpectedly resumes a Pi session: %#v", spec.Args)
	}
	for _, required := range []string{
		"--mode", "rpc", "--provider", privateProviderName, "--model", "gpt-local", "--session-dir", spec.SessionDir,
		"--no-tools", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve",
	} {
		if !slices.Contains(spec.Args, required) {
			t.Fatalf("arguments do not contain %q: %#v", required, spec.Args)
		}
	}
	if strings.Contains(strings.Join(spec.Args, " "), "secret-value") {
		t.Fatal("credential leaked into command arguments")
	}
	if !slices.Contains(spec.Env, "SCRIPTBOARD_PI_API_KEY=secret-value") || !slices.Contains(spec.Env, "PI_OFFLINE=1") || !slices.Contains(spec.Env, "PI_SKIP_VERSION_CHECK=1") || !slices.Contains(spec.Env, "PI_TELEMETRY=0") {
		t.Fatalf("environment = %#v", spec.Env)
	}
	if spec.SessionDir == spec.Workspace || !strings.HasPrefix(spec.SessionDir, filepath.Join(stateRoot, "assistant")) || !strings.HasPrefix(spec.Workspace, filepath.Join(stateRoot, "assistant")) {
		t.Fatalf("session = %q, workspace = %q", spec.SessionDir, spec.Workspace)
	}
	info, err := os.Stat(spec.ModelConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("models.json permissions = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(spec.ModelConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-value") {
		t.Fatal("credential leaked into models.json")
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("invalid models.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spec.SessionDir, "session-fixture.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed, err := PrepareLaunch(LaunchInput{
		StateRoot: stateRoot, Executable: executable, UserID: "user_1", ConversationID: "conversation_1",
		Provider: "openai-compatible", Model: "gpt-local", Endpoint: "http://127.0.0.1:11434/v1", APIKey: "secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resumed.Args, "--continue") {
		t.Fatalf("an existing conversation did not resume its isolated Pi session: %#v", resumed.Args)
	}
}

func TestPrepareLaunchIsolatesTwoConversations(t *testing.T) {
	stateRoot := t.TempDir()
	executable := filepath.Join(stateRoot, "assistant", "runtime", "versions", "test", runtimeExecutableName())
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	base := LaunchInput{StateRoot: stateRoot, Executable: executable, UserID: "same-user", Provider: "anthropic", Model: "claude-test", Endpoint: "https://api.anthropic.com", APIKey: "key"}
	base.ConversationID = "first"
	first, err := PrepareLaunch(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ConversationID = "second"
	second, err := PrepareLaunch(base)
	if err != nil {
		t.Fatal(err)
	}
	if first.PiHome == second.PiHome || first.SessionDir == second.SessionDir || first.Workspace == second.Workspace {
		t.Fatalf("conversation directories overlap: %#v %#v", first, second)
	}
}

func TestPrepareLaunchRejectsGlobalAndUntrustedExecutables(t *testing.T) {
	stateRoot := t.TempDir()
	for _, executable := range []string{"pi", filepath.Join(stateRoot, "outside", runtimeExecutableName())} {
		_, err := PrepareLaunch(LaunchInput{StateRoot: stateRoot, Executable: executable, UserID: "user", ConversationID: "conversation", Provider: "openai", Model: "gpt", Endpoint: "https://api.openai.com/v1", APIKey: "key"})
		if err == nil {
			t.Fatalf("expected %q to be rejected", executable)
		}
	}
}

func TestPrepareLaunchLoadsOnlyExplicitBrokerExtension(t *testing.T) {
	stateRoot := t.TempDir()
	runtimeDir := filepath.Join(stateRoot, "assistant", "runtime", "versions", "test")
	executable := filepath.Join(runtimeDir, runtimeExecutableName())
	extension := filepath.Join(runtimeDir, "scriptboard-extension.js")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extension, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := PrepareLaunch(LaunchInput{
		StateRoot: stateRoot, Executable: executable, Extension: extension, UserID: "user", ConversationID: "conversation",
		Provider: "openai", Model: "gpt", Endpoint: "https://api.openai.com/v1", APIKey: "key",
		BrokerEndpoint: `\\.\pipe\scriptboard-fixture`, BrokerCapability: "capability-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(spec.Args, "--no-tools") || !slices.Contains(spec.Args, "--no-builtin-tools") || !slices.Contains(spec.Args, extension) {
		t.Fatalf("arguments = %#v", spec.Args)
	}
	if !slices.Contains(spec.Env, `SCRIPTBOARD_BROKER_ENDPOINT=\\.\pipe\scriptboard-fixture`) || !slices.Contains(spec.Env, "SCRIPTBOARD_BROKER_CAPABILITY=capability-fixture") {
		t.Fatalf("broker environment = %#v", spec.Env)
	}
	if _, err := PrepareLaunch(LaunchInput{StateRoot: stateRoot, Executable: executable, Extension: extension, UserID: "user", ConversationID: "conversation", Provider: "openai", Model: "gpt", Endpoint: "https://api.openai.com/v1", APIKey: "key"}); err == nil {
		t.Fatal("extension launch without a process-bound broker capability was accepted")
	}
}
