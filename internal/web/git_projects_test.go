package web_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGitProjectAndQuickRunPreflight(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is required")
	}
	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	checkout := filepath.Join(hostRoot, "existing")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand := func(args ...string) {
		t.Helper()
		command := exec.Command(git, append([]string{"-C", checkout, "-c", "user.name=ScriptBoard Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, output)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitCommand("init", "--initial-branch=main")
	tracked := filepath.Join(checkout, "tracked.txt")
	write(tracked, "original\n")
	gitCommand("add", "tracked.txt")
	gitCommand("commit", "-m", "initial")
	gitCommand("remote", "add", "origin", "https://git.example.invalid/repo.git")
	write(tracked, "local edit\n")
	client, server := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	request := func(path string, fields url.Values, status int) ([]byte, string) {
		t.Helper()
		var response *http.Response
		var err error
		if fields == nil {
			response, err = client.Get(server + path)
		} else {
			response, err = client.PostForm(server+path, fields)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s status=%d want=%d err=%v body=%s", path, response.StatusCode, status, err, body)
		}
		return body, response.Header.Get("Location")
	}
	add, _ := request("/resources/git-projects/task?task=add", nil, http.StatusOK)
	if !strings.Contains(string(add), "Use existing checkout") {
		t.Fatalf("register existing option missing: %s", add)
	}
	token := formToken(t, add)
	request("/resources/git-projects/new", url.Values{
		"csrf_token": {token}, "name": {"Existing repo"}, "directory": {checkout}, "mode": {"existing"},
	}, http.StatusSeeOther)
	projectPage, _ := request("/resources/git-projects", nil, http.StatusOK)
	match := regexp.MustCompile("name=\"project_id\" value=\"([A-Za-z0-9_-]+)\" data-git-select").FindSubmatch(projectPage)
	if len(match) != 2 || !strings.Contains(string(projectPage), ">Local changes<") || !strings.Contains(string(projectPage), "Open directory") {
		t.Fatalf("registered working tree details missing: %s", projectPage)
	}
	projectID := string(match[1])
	groupID, _ := createQuickRunGroup(t, client, server, "Deployment")
	groupURL := "/config/quick-runs/groups/" + groupID + "/update"
	request(groupURL, url.Values{"csrf_token": {token}, "name": {"Deployment"}, "git_settings": {"1"}, "git_project": {projectID}}, http.StatusSeeOther)

	name, original, modified := "quick.sh", "echo original\n", "echo current-content\n"
	if runtime.GOOS == "windows" {
		name, original, modified = "quick.cmd", "@echo off\r\necho original\r\n", "@echo off\r\necho current-content\r\n"
	}
	scriptPath := filepath.Join(hostRoot, name)
	write(scriptPath, original)
	createQuickRunFromFile(t, client, server, scriptPath, "Run with preflight", groupID)
	page := getQuickRunsPage(t, client, server)
	quickID := quickRunIDForName(t, page, "Run with preflight")
	quickURL := "/config/quick-runs/" + quickID
	edit, _ := request(quickURL+"/edit", nil, http.StatusOK)
	if !strings.Contains(string(edit), "name=\"follow_file\" value=\"1\" checked") || !regexp.MustCompile("value=\""+projectID+"\"\\s+checked disabled").Match(edit) {
		t.Fatalf("follow and inherited settings missing: %s", edit)
	}
	write(scriptPath, modified)
	start := url.Values{"csrf_token": {token}}
	blocked, _ := request(quickURL+"/start", start, http.StatusBadRequest)
	if !strings.Contains(string(blocked), "Git preflight") {
		t.Fatalf("failed update should prevent the script starting: %s", blocked)
	}
	history, _ := request("/resources/git-projects?tab=history", nil, http.StatusOK)
	if !strings.Contains(string(history), ">Failed<") || !strings.Contains(string(history), ">Quick Runs<") {
		t.Fatalf("failed Git preflight missing from history: %s", history)
	}

	request(groupURL, url.Values{"csrf_token": {token}, "name": {"Deployment"}, "git_settings": {"1"}}, http.StatusSeeOther)
	_, runURL := request(quickURL+"/start", start, http.StatusSeeOther)
	for deadline := time.Now().Add(6 * time.Second); ; {
		log, _ := request(runURL+"/download", nil, http.StatusOK)
		if strings.Contains(string(log), "current-content") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("updated script output missing: %s", log)
		}
		time.Sleep(30 * time.Millisecond)
	}

	request(quickURL+"/update", url.Values{"csrf_token": {token}, "name": {"Run with preflight"}, "git_settings": {"1"}}, http.StatusSeeOther)
	write(scriptPath, original)
	request(quickURL+"/start", start, http.StatusConflict)
}
