package customdashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// flowBuiltinTestRunner 使用临时工作区与假路径保护（含 protected 的绝对路径拒绝）。
func flowBuiltinTestRunner(t *testing.T) (*BuiltinRunner, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	runner := NewBuiltinRunner(BuiltinRunnerOptions{
		WorkspaceRoot: workspaceRoot,
		ValidatePath: func(path string) error {
			if strings.Contains(filepath.ToSlash(path), "protected") {
				return errors.New("protected")
			}
			return nil
		},
	})
	return runner, workspaceRoot
}

func TestBuiltinCopyFiles(t *testing.T) {
	runner, workspaceRoot := flowBuiltinTestRunner(t)
	workspace := filepath.Join(workspaceRoot, "card-1")
	if err := os.MkdirAll(filepath.Join(workspace, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 目录递归复制。
	message, err := runner.Run(context.Background(), "card-1", "copy-files", map[string]string{"from": "src", "to": "dst"})
	if err != nil || message != "copied 2 files" {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "dst", "sub", "b.txt")); err != nil || string(content) != "b" {
		t.Fatalf("copied content=%q err=%v", content, err)
	}
	// 单文件覆盖同名文件。
	if err := os.WriteFile(filepath.Join(workspace, "dst", "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "copy-files", map[string]string{"from": "src/a.txt", "to": "dst/a.txt"}); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(workspace, "dst", "a.txt")); string(content) != "a" {
		t.Fatalf("overwrite content=%q", content)
	}
	// 源必须存在。
	if _, err := runner.Run(context.Background(), "card-1", "copy-files", map[string]string{"from": "ghost", "to": "dst"}); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestBuiltinWriteFileAndMakeDir(t *testing.T) {
	runner, workspaceRoot := flowBuiltinTestRunner(t)
	if _, err := runner.Run(context.Background(), "card-1", "make-dir", map[string]string{"path": "logs"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(workspaceRoot, "card-1", "logs")); err != nil || !info.IsDir() {
		t.Fatalf("dir err=%v", err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "write-file", map[string]string{"path": "logs/run.log", "content": "first\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "write-file", map[string]string{"path": "logs/run.log", "content": "second\n", "append": "true"}); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(workspaceRoot, "card-1", "logs", "run.log"))
	if string(content) != "first\nsecond\n" {
		t.Fatalf("content=%q", content)
	}
	// 非 append 覆盖。
	if _, err := runner.Run(context.Background(), "card-1", "write-file", map[string]string{"path": "logs/run.log", "content": "only\n"}); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(filepath.Join(workspaceRoot, "card-1", "logs", "run.log"))
	if string(content) != "only\n" {
		t.Fatalf("overwrite content=%q", content)
	}
}

func TestBuiltinRemoveFilesProtection(t *testing.T) {
	runner, workspaceRoot := flowBuiltinTestRunner(t)
	target := filepath.Join(workspaceRoot, "card-1", "junk.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "remove-files", map[string]string{"path": "junk.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
	// 逃逸工作区的相对路径拒绝。
	if _, err := runner.Run(context.Background(), "card-1", "remove-files", map[string]string{"path": "../other.txt"}); err == nil {
		t.Fatal("escape path accepted")
	}
	// 工作区根目录本身拒绝删除。
	if _, err := runner.Run(context.Background(), "card-1", "remove-files", map[string]string{"path": "."}); err == nil {
		t.Fatal("workspace root removal accepted")
	}
	// 绝对路径走注入的保护规则。
	protected := filepath.Join(workspaceRoot, "protected", "data.txt")
	if _, err := runner.Run(context.Background(), "card-1", "remove-files", map[string]string{"path": protected}); err == nil {
		t.Fatal("protected absolute path accepted")
	}
}

func TestBuiltinHTTPRequest(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotHeader = request.Header.Get("X-Token")
		if request.URL.Path == "/fail" {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	runner, _ := flowBuiltinTestRunner(t)
	message, err := runner.Run(context.Background(), "card-1", "http-request", map[string]string{"url": server.URL + "/ok", "header_X-Token": "abc"})
	if err != nil || message != "HTTP 200" {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if gotHeader != "abc" {
		t.Fatalf("header=%q", gotHeader)
	}
	if _, err := runner.Run(context.Background(), "card-1", "http-request", map[string]string{"url": server.URL + "/fail"}); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("500 err=%v", err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "http-request", map[string]string{"url": server.URL, "method": "TRACE"}); err == nil {
		t.Fatal("invalid method accepted")
	}
}

func TestBuiltinSleep(t *testing.T) {
	runner, _ := flowBuiltinTestRunner(t)
	started := time.Now()
	message, err := runner.Run(context.Background(), "card-1", "sleep", map[string]string{"seconds": "0"})
	if err != nil || message != "slept 0s" || time.Since(started) > time.Second {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "sleep", map[string]string{"seconds": "301"}); err == nil {
		t.Fatal("seconds > 300 accepted")
	}
}

func TestBuiltinGitSync(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	runner, workspaceRoot := flowBuiltinTestRunner(t)
	origin := filepath.Join(t.TempDir(), "origin")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	git(origin, "init")
	git(origin, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(origin, "add", ".")
	git(origin, "commit", "-m", "v1")
	// clone 到工作区。
	message, err := runner.Run(context.Background(), "card-1", "git-sync", map[string]string{"repo": origin, "dest": "repo", "branch": "main"})
	if err != nil || !strings.HasPrefix(message, "synced ") {
		t.Fatalf("clone message=%q err=%v", message, err)
	}
	// 更新 origin 后再次同步。
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(origin, "add", ".")
	git(origin, "commit", "-m", "v2")
	if _, err := runner.Run(context.Background(), "card-1", "git-sync", map[string]string{"repo": origin, "dest": "repo", "branch": "main"}); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(workspaceRoot, "card-1", "repo", "f.txt"))
	if string(content) != "v2" {
		t.Fatalf("content=%q", content)
	}
	// 已存在但非 git 仓库报错。
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "card-1", "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), "card-1", "git-sync", map[string]string{"repo": origin, "dest": "plain"}); err == nil || !strings.Contains(err.Error(), "不是 git 仓库") {
		t.Fatalf("non-repo err=%v", err)
	}
}
