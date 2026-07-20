package app_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAdminCanEnableVersionProtectionWithBaseline(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "protected.txt"), []byte("baseline"), 0o644); err != nil {
		t.Fatalf("write managed file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/settings/version-protection")
	if err != nil {
		t.Fatalf("get version protection: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/settings/version-protection/enable", url.Values{
		"confirm":    {"yes"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("enable version protection: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("enable response: status=%d body=%s", response.StatusCode, body)
	}
	branch := gitOutput(t, managedRoot, "branch", "--show-current")
	if strings.TrimSpace(branch) != "scriptboard-managed" {
		t.Fatalf("managed branch = %q", branch)
	}
	message := gitOutput(t, managedRoot, "log", "-1", "--pretty=%s")
	if !strings.Contains(message, "baseline") {
		t.Fatalf("baseline commit message = %q", message)
	}
	status := gitOutput(t, managedRoot, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("repository is dirty after baseline: %s", status)
	}
}

func TestVersionProtectionCheckpointsAroundRunBatch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	scriptName := "change.sh"
	scriptContent := "printf 'changed\\n' > result.txt\n"
	if runtime.GOOS == "windows" {
		scriptName = "change.cmd"
		scriptContent = "@echo off\r\necho changed>result.txt\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/settings/version-protection")
	if err != nil {
		t.Fatalf("get version protection: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/settings/version-protection/enable", url.Values{"confirm": {"yes"}, "csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatalf("enable version protection: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/runs/start", url.Values{"script": {scriptName}, "csrf_token": {formToken(t, filesPage)}})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	runURL := serverURL + response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, _ = client.Get(runURL)
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(body), "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %s", body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	for {
		log := gitOutput(t, managedRoot, "log", "--pretty=%s", "-3")
		if strings.Contains(log, "pre-run") && strings.Contains(log, "post-run") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run checkpoints missing: %s", log)
		}
		time.Sleep(25 * time.Millisecond)
	}
	tracked := gitOutput(t, managedRoot, "show", "HEAD:result.txt")
	if !strings.Contains(tracked, "changed") {
		t.Fatalf("post-run content missing: %q", tracked)
	}
}

func TestAdminCanCheckpointAndRestoreSingleFile(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	filePath := filepath.Join(managedRoot, "restore.txt")
	if err := os.WriteFile(filePath, []byte("version one"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/settings/version-protection")
	if err != nil {
		t.Fatalf("get version protection: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/settings/version-protection/enable", url.Values{"confirm": {"yes"}, "csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatalf("enable version protection: %v", err)
	}
	_ = response.Body.Close()
	baseline := strings.TrimSpace(gitOutput(t, managedRoot, "rev-parse", "HEAD"))
	if err := os.WriteFile(filePath, []byte("version two"), 0o644); err != nil {
		t.Fatalf("write second version: %v", err)
	}
	response, err = client.Get(serverURL + "/settings/version-protection")
	if err != nil {
		t.Fatalf("get version protection after edit: %v", err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/settings/version-protection/checkpoint", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/settings/version-protection/restore", url.Values{
		"path":       {"restore.txt"},
		"commit":     {baseline},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("restore file: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("restore response: status=%d body=%s", response.StatusCode, body)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "version one" {
		t.Fatalf("restored content = %q", content)
	}
	message := gitOutput(t, managedRoot, "log", "-1", "--pretty=%s")
	if !strings.Contains(message, "restore") {
		t.Fatalf("restore commit message = %q", message)
	}
}

func gitOutput(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
