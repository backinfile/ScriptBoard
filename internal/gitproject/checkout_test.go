package gitproject

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

type gitFixture struct {
	git, root, seed, bare string
}

func gitTestCommand(t *testing.T, git, directory string, args ...string) string {
	t.Helper()
	prefix := []string{"-c", "user.name=ScriptBoard Test", "-c", "user.email=test@example.invalid", "-c", "core.autocrlf=false"}
	command := exec.Command(git, append(prefix, args...)...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", args[0], err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeGitTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func newGitFixture(t *testing.T) gitFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is required")
	}
	root := t.TempDir()
	seed, bare := filepath.Join(root, "seed"), filepath.Join(root, "project.git")
	gitTestCommand(t, git, root, "init", "--initial-branch=main", seed)
	gitTestCommand(t, git, root, "init", "--bare", "--initial-branch=main", bare)
	writeGitTestFile(t, filepath.Join(seed, ".gitignore"), "guard.env\n")
	writeGitTestFile(t, filepath.Join(seed, "main.txt"), "initial\n")
	gitTestCommand(t, git, seed, "add", ".")
	gitTestCommand(t, git, seed, "commit", "-m", "initial")
	gitTestCommand(t, git, seed, "branch", "develop")
	gitTestCommand(t, git, seed, "push", bare, "main", "develop")
	return gitFixture{git: git, root: root, seed: seed, bare: bare}
}

func (fixture gitFixture) endpoint() http.Handler {
	return &cgi.Handler{Path: fixture.git, Args: []string{"http-backend"}, Dir: fixture.root,
		Env:        []string{"GIT_PROJECT_ROOT=" + filepath.ToSlash(fixture.root), "GIT_HTTP_EXPORT_ALL=1"},
		InheritEnv: []string{"PATH", "SystemRoot", "WINDIR", "TMP", "TEMP"}}
}

func (fixture gitFixture) remoteUpdate(t *testing.T, changes map[string]string) {
	t.Helper()
	for name, content := range changes {
		writeGitTestFile(t, filepath.Join(fixture.seed, name), content)
		gitTestCommand(t, fixture.git, fixture.seed, "add", "-f", name)
	}
	gitTestCommand(t, fixture.git, fixture.seed, "commit", "-m", "update")
	gitTestCommand(t, fixture.git, fixture.seed, "push", fixture.bare, "main")
}

func gitTestCheckout(t *testing.T) (*Checkout, string) {
	t.Helper()
	state := t.TempDir()
	fs, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{state}})
	if err != nil {
		t.Fatal(err)
	}
	return NewCheckout(fs, state), filepath.Join(t.TempDir(), "checkout")
}

func gitTestProject(target, remote string) Project {
	return Project{ID: "project", Name: "project", Directory: target, PathKey: hostfiles.ComparisonKey(target), Remote: remote}
}

func TestCheckoutLifecycle(t *testing.T) {
	fixture := newGitFixture(t)
	server := httptest.NewServer(fixture.endpoint())
	defer server.Close()
	checkout, target := gitTestCheckout(t)
	project := gitTestProject(target, server.URL+"/project.git")
	run := func(action Action, branch, revision string) (Result, error) {
		return checkout.Apply(t.Context(), project, Request{ProjectID: project.ID, Action: action, Branch: branch, Revision: revision})
	}
	initial, err := run(Force, "", UnclonedRevision)
	if err != nil || initial.Before.Cloned || !initial.After.Cloned || initial.After.Branch != "main" {
		t.Fatalf("clone = %+v, %v", initial, err)
	}
	if _, err := run(Force, "", UnclonedRevision); !errors.Is(err, ErrChanged) {
		t.Fatalf("force clone on an existing checkout = %v", err)
	}
	switched, err := run(Switch, "develop", "")
	if err != nil || switched.After.Branch != "develop" || switched.After.Upstream != "origin/develop" {
		t.Fatalf("switch = %+v, %v", switched, err)
	}
	if _, err := run(Switch, "main", ""); err != nil {
		t.Fatal(err)
	}

	tracked := filepath.Join(target, "main.txt")
	writeGitTestFile(t, tracked, "local change\n")
	untracked := filepath.Join(target, "local.env")
	writeGitTestFile(t, untracked, "preserve\n")
	if _, err := run(Sync, "", ""); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("ordinary sync with local changes = %v", err)
	}
	snapshot, err := run(Inspect, "", "")
	if err != nil || snapshot.After.Tracked != 1 {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	writeGitTestFile(t, tracked, "new local change on same path\n")
	if _, err := run(Discard, "", snapshot.After.Revision); !errors.Is(err, ErrChanged) {
		t.Fatalf("discard after a change = %v", err)
	}
	snapshot, err = run(Inspect, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(Discard, "", snapshot.After.Revision); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(tracked); err != nil || string(content) != "initial\n" {
		t.Fatalf("tracked file = %q, %v", content, err)
	}
	fixture.remoteUpdate(t, map[string]string{"main.txt": "remote v2\n"})
	updated, err := run(Sync, "", "")
	if err != nil || updated.Before.Commit == updated.After.Commit {
		t.Fatalf("ordinary fast-forward = %+v, %v", updated, err)
	}

	ignored := filepath.Join(target, "guard.env")
	writeGitTestFile(t, ignored, "local guard\n")
	fixture.remoteUpdate(t, map[string]string{"guard.env": "remote guard\n"})
	snapshot, err = run(Inspect, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(Force, "", snapshot.After.Revision); err == nil || !strings.Contains(err.Error(), "would overwrite") {
		t.Fatalf("force sync with ignored collision = %v", err)
	}
	if content, err := os.ReadFile(ignored); err != nil || string(content) != "local guard\n" {
		t.Fatalf("ignored file = %q, %v", content, err)
	}
	if err := os.Remove(ignored); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, tracked, "discard during force\n")
	snapshot, err = run(Inspect, "", "")
	if err != nil {
		t.Fatal(err)
	}
	forced, err := run(Force, "", snapshot.After.Revision)
	if err != nil || forced.After.Tracked != 0 || forced.After.Commit == forced.Before.Commit {
		t.Fatalf("force sync = %+v, %v", forced, err)
	}
	if content, err := os.ReadFile(untracked); err != nil || string(content) != "preserve\n" {
		t.Fatalf("untracked file = %q, %v", content, err)
	}
}

func TestExistingCheckoutPreservesGitDirectory(t *testing.T) {
	fixture := newGitFixture(t)
	server := httptest.NewServer(fixture.endpoint())
	defer server.Close()
	checkout, target := gitTestCheckout(t)
	gitTestCommand(t, fixture.git, filepath.Dir(target), "clone", server.URL+"/project.git", target)
	writeGitTestFile(t, filepath.Join(target, "main.txt"), "changed locally\n")
	project := gitTestProject(target, "")
	project.Existing = true
	result, err := checkout.Apply(t.Context(), project, Request{ProjectID: project.ID, Action: Inspect})
	if err != nil || result.After.Tracked != 1 || result.After.Remote != server.URL+"/project.git" {
		t.Fatalf("existing checkout = %+v, %v", result, err)
	}
	if metadata, err := os.Stat(filepath.Join(target, ".git")); err != nil || !metadata.IsDir() {
		t.Fatalf("existing metadata was relocated: %v", err)
	}
	gitTestCommand(t, fixture.git, target, "config", "include.path", "some-config")
	request := Request{ProjectID: project.ID, Action: Sync}
	if _, err := checkout.Apply(context.Background(), project, request); err == nil || !strings.Contains(err.Error(), "configuration") {
		t.Fatalf("an unsafe Git configuration was accepted: %v", err)
	}
}

func TestHTTPSCheckoutVerificationModes(t *testing.T) {
	fixture := newGitFixture(t)
	server := httptest.NewTLSServer(fixture.endpoint())
	defer server.Close()
	remote := server.URL + "/project.git"
	ctx := t.Context()
	checkout, target := gitTestCheckout(t)
	project := gitTestProject(target, remote)
	request := Request{ProjectID: project.ID, Action: Sync}
	if _, err := checkout.Apply(ctx, project, request); err == nil {
		t.Fatal("a Git connection accepted the untrusted certificate")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed clone left a checkout: %v", err)
	}
	project.SkipTLS = true
	if result, err := checkout.Apply(ctx, project, request); err != nil || !result.After.Cloned {
		t.Fatalf("explicit skip mode = %+v, %v", result, err)
	}

	trustFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(trustFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", trustFile)
	checkout, target = gitTestCheckout(t)
	project = gitTestProject(target, remote)
	if result, err := checkout.Apply(ctx, project, request); err != nil || !result.After.Cloned {
		t.Fatalf("verified HTTPS mode = %+v, %v", result, err)
	}
}
