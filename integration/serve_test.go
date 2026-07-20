package integration_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServeCommandStartsHTTPApplication(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "scriptboard")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	build := exec.Command("go", "build", "-o", binary, "./cmd/scriptboard")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scriptboard: %v\n%s", err, output)
	}

	command := exec.Command(
		binary,
		"serve",
		"--managed-root", filepath.Join(root, "managed"),
		"--state-root", filepath.Join(root, "state"),
		"--listen", "127.0.0.1:0",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("open stdout: %v", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatalf("open stderr: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start scriptboard: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
			return
		}
		message, _ := io.ReadAll(stderr)
		line <- "process exited before startup: " + string(message)
	}()

	var startup string
	select {
	case startup = <-line:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for startup")
	}
	const prefix = "ScriptBoard 已启动：http://"
	if !strings.HasPrefix(startup, prefix) {
		t.Fatalf("unexpected startup line: %q", startup)
	}
	address := strings.TrimPrefix(startup, "ScriptBoard 已启动：")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/login", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
