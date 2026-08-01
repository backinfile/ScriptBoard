package pirpc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveActiveRuntimeUsesOnlyThePrivateVersionDirectory(t *testing.T) {
	stateRoot := t.TempDir()
	versionRoot := filepath.Join(stateRoot, "assistant", "runtime", "versions", "0.83.0")
	executable := filepath.Join(versionRoot, runtimeExecutableName())
	if err := os.MkdirAll(versionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	activeRoot := filepath.Join(stateRoot, "assistant", "runtime")
	if err := os.WriteFile(filepath.Join(activeRoot, "active.json"), []byte(`{"version":"0.83.0","rpcContract":1,"executable":"`+runtimeExecutableName()+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := ResolveActiveRuntime(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Version != "0.83.0" || runtime.Executable != executable || runtime.RPCContract != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
}

func TestResolveActiveRuntimeRejectsMissingAndTraversalPointers(t *testing.T) {
	stateRoot := t.TempDir()
	if _, err := ResolveActiveRuntime(stateRoot); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("missing error = %v", err)
	}
	runtimeRoot := filepath.Join(stateRoot, "assistant", "runtime")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "active.json"), []byte(`{"version":"../outside","rpcContract":1,"executable":"pi"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveActiveRuntime(stateRoot); !errors.Is(err, ErrRuntimeInvalid) {
		t.Fatalf("traversal error = %v", err)
	}
}
