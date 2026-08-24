package runnerhost

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/runmanager"
)

func TestIsolatedRunnerHostTracerBullet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a deterministic Runner fixture")
	}
	root := t.TempDir()
	scriptPath := filepath.Join(root, "job.sh")
	script := []byte("#!/bin/sh\nprintf 'runner stdout\\n'\nprintf 'runner stderr\\n' >&2\n")
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(root, "job.cmd")
		script = []byte("@echo runner stdout\r\n@echo runner stderr 1>&2\r\n")
	}
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(script)
	endpoint := filepath.Join(t.TempDir(), "runner.sock")
	if runtime.GOOS == "windows" {
		var err error
		endpoint, err = DefaultEndpoint(root)
		if err != nil {
			t.Fatal(err)
		}
	}
	transport, err := Listen(TransportOptions{StateRoot: root, Endpoint: endpoint, DevelopmentCurrentUser: true})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{Listener: transport.Listener, VerifyPeer: transport.VerifyPeer, Maximum: 1})
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
	launcher := NewClientLauncher(Dial(transport.Endpoint))
	process, gotExecutor, err := launcher.Launch(context.Background(), runmanager.LaunchRequest{RunID: "run-1", ScriptPath: scriptPath, ScriptDigest: fmt.Sprintf("%x", digest[:]), WorkingDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	stdoutDone := make(chan string, 1)
	stderrDone := make(chan string, 1)
	go func() { data, _ := io.ReadAll(process.Stdout()); stdoutDone <- string(data) }()
	go func() { data, _ := io.ReadAll(process.Stderr()); stderrDone <- string(data) }()
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := <-stdoutDone, <-stderrDone
	if !strings.Contains(stdout, "runner stdout") || !strings.Contains(stderr, "runner stderr") {
		t.Fatalf("Runner Host streams stdout=%q stderr=%q", stdout, stderr)
	}
	if !filepath.IsAbs(gotExecutor) {
		t.Fatalf("executor is not absolute: %q", gotExecutor)
	}
}

func TestRunnerHostRechecksScriptDigest(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "job.fixture")
	if err := os.WriteFile(script, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := runmanager.NewLocalProcessLauncher(map[string][]string{".fixture": {filepath.Join(root, "executor")}})
	if _, _, err := launcher.Launch(context.Background(), runmanager.LaunchRequest{RunID: "run-1", ScriptPath: script, ScriptDigest: strings.Repeat("0", 64), WorkingDirectory: root}); err == nil {
		t.Fatal("changed script digest was accepted")
	}
}
