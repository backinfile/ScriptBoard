package app_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAdminCanCreateQuickRunGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))

	response, err := client.Get(serverURL + "/config/quick-runs/groups/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(taskPage), `data-task-kind="quick-group-new"`) {
		t.Fatalf("new group task status=%d body=%s", response.StatusCode, taskPage)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/groups", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {"Deployment"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/quick-runs" {
		t.Fatalf("create group status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-quick-run-group`, `Deployment`, `0 items`} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("group page missing %q: %s", expected, page)
		}
	}
}

func TestAdminCanRenameQuickRunGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	groupID, token := createQuickRunGroup(t, client, serverURL, "Deployment")

	response, err := client.Get(serverURL + "/config/quick-runs/groups/" + groupID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-task-kind="quick-group-edit"`, `name="name"`, `value="Deployment"`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("edit group task missing %q: %s", expected, taskPage)
		}
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/groups/"+groupID+"/update", url.Values{
		"csrf_token": {token},
		"name":       {"Production"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(page), "Production") || strings.Contains(string(page), ">Deployment<") {
		t.Fatalf("renamed group not reflected: %s", page)
	}
}

func TestAdminCanReorderQuickRunGroups(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	_, _ = createQuickRunGroup(t, client, serverURL, "First")
	secondID, token := createQuickRunGroup(t, client, serverURL, "Second")

	response, err := client.PostForm(serverURL+"/config/quick-runs/groups/"+secondID+"/move", url.Values{
		"csrf_token": {token},
		"direction":  {"up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if first, second := strings.Index(string(page), ">First<"), strings.Index(string(page), ">Second<"); first < 0 || second < 0 || second > first {
		t.Fatalf("groups were not reordered: %s", page)
	}
}

func TestAdminCanCreateQuickRunInAGroupFromManagedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "grouped.sh", "printf 'grouped\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "grouped.cmd", "@echo off\r\necho grouped\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Deployment")

	response, err := client.Get(serverURL + "/resources/files/quick-run/" + scriptName)
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`name="group_id"`, `value="` + groupID + `"`, `Deployment`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("Quick Run form missing group option %q: %s", expected, taskPage)
		}
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {"Grouped deploy"},
		"script":     {scriptName},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create grouped Quick Run status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	groupPattern := regexp.MustCompile(`data-quick-run-group="` + regexp.QuoteMeta(groupID) + `"[\s\S]*?Grouped deploy`)
	if !groupPattern.Match(page) {
		t.Fatalf("grouped Quick Run is not rendered in its group: %s", page)
	}
}

func TestDeletingQuickRunGroupMovesItsItemsToUngrouped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "preserved.sh", "printf 'preserved\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "preserved.cmd", "@echo off\r\necho preserved\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	groupID, token := createQuickRunGroup(t, client, serverURL, "Temporary")
	createQuickRunFromFile(t, client, serverURL, scriptName, "Preserved item", groupID)

	response, err := client.PostForm(serverURL+"/config/quick-runs/groups/"+groupID+"/delete", url.Values{
		"csrf_token": {token},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(page), ">Temporary<") {
		t.Fatalf("deleted group remains: %s", page)
	}
	ungroupedPattern := regexp.MustCompile(`data-quick-run-group="ungrouped"[\s\S]*?Preserved item`)
	if !ungroupedPattern.Match(page) {
		t.Fatalf("group member was not preserved under Ungrouped: %s", page)
	}
}

func TestAdminCanMoveQuickRunBetweenGroups(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "movable.sh", "printf 'movable\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "movable.cmd", "@echo off\r\necho movable\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	sourceGroupID, _ := createQuickRunGroup(t, client, serverURL, "Source")
	targetGroupID, _ := createQuickRunGroup(t, client, serverURL, "Target")
	createQuickRunFromFile(t, client, serverURL, scriptName, "Movable item", sourceGroupID)
	page := getQuickRunsPage(t, client, serverURL)
	quickRunID := hiddenValue(t, page, "id")

	response, err := client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/move-group")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-task-kind="quick-move-group"`, `value="` + targetGroupID + `"`, `Target`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("move group task missing %q: %s", expected, taskPage)
		}
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/move-group", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"group_id":   {targetGroupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move Quick Run status=%d", response.StatusCode)
	}
	page = getQuickRunsPage(t, client, serverURL)
	targetPattern := regexp.MustCompile(`data-quick-run-group="` + regexp.QuoteMeta(targetGroupID) + `"[\s\S]*?Movable item`)
	if !targetPattern.Match(page) {
		t.Fatalf("Quick Run did not move to target group: %s", page)
	}
}

func TestQuickRunReorderingStaysWithinCurrentGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "ordered.sh", "printf 'ordered\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "ordered.cmd", "@echo off\r\necho ordered\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Ordered")
	otherGroupID, _ := createQuickRunGroup(t, client, serverURL, "Other")
	createQuickRunFromFile(t, client, serverURL, scriptName, "First item", groupID)
	createQuickRunFromFile(t, client, serverURL, scriptName, "Second item", groupID)
	createQuickRunFromFile(t, client, serverURL, scriptName, "Other item", otherGroupID)
	page := getQuickRunsPage(t, client, serverURL)
	secondID := quickRunIDForName(t, page, "Second item")

	response, err := client.PostForm(serverURL+"/config/quick-runs/"+secondID+"/move", url.Values{
		"csrf_token": {formToken(t, page)},
		"direction":  {"up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move Quick Run status=%d", response.StatusCode)
	}

	page = getQuickRunsPage(t, client, serverURL)
	first, second, other := strings.Index(string(page), "First item"), strings.Index(string(page), "Second item"), strings.Index(string(page), "Other item")
	if first < 0 || second < 0 || other < 0 || second > first || first > other {
		t.Fatalf("Quick Run order is not scoped to its group: %s", page)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+secondID+"/move-group", url.Values{
		"csrf_token": {formToken(t, page)},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move Quick Run to current group status=%d", response.StatusCode)
	}
	page = getQuickRunsPage(t, client, serverURL)
	first, second = strings.Index(string(page), "First item"), strings.Index(string(page), "Second item")
	if second < 0 || first < 0 || second > first {
		t.Fatalf("moving to the current group changed item order: %s", page)
	}
}

func TestAdminCanSaveHistoricalRunIntoAGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "historical.sh", "printf 'historical\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "historical.cmd", "@echo off\r\necho historical\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "History")
	filesPageResponse, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatal(err)
	}
	filesPage, _ := io.ReadAll(filesPageResponse.Body)
	_ = filesPageResponse.Body.Close()
	response, err := client.PostForm(serverURL+"/monitor/runs/start", url.Values{
		"csrf_token": {formToken(t, filesPage)},
		"script":     {scriptName},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = client.Get(serverURL + runPath)
		if err != nil {
			t.Fatal(err)
		}
		runPage, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(runPage), "succeeded") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("source Run did not finish: %s", runPage)
		}
		time.Sleep(20 * time.Millisecond)
	}

	response, err = client.Get(serverURL + runPath + "/save-quick-run")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`name="group_id"`, `value="` + groupID + `"`, `History`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("historical Quick Run form missing %q: %s", expected, taskPage)
		}
	}
	response, err = client.PostForm(serverURL+runPath+"/quick-run", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {"Historical item"},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	page := getQuickRunsPage(t, client, serverURL)
	groupPattern := regexp.MustCompile(`data-quick-run-group="` + regexp.QuoteMeta(groupID) + `"[\s\S]*?Historical item`)
	if !groupPattern.Match(page) {
		t.Fatalf("historical Quick Run is not rendered in its group: %s", page)
	}
}

func TestAdminCanEditQuickRunWithoutChangingItsScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "editable.sh", "printf 'editable\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "editable.cmd", "@echo off\r\necho editable\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	createQuickRunFromFile(t, client, serverURL, scriptName, "Original name", "")
	page := getQuickRunsPage(t, client, serverURL)
	quickRunID := quickRunIDForName(t, page, "Original name")

	response, err := client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{
		`data-task-kind="quick-edit"`,
		`name="name" autocomplete="off" value="Original name"`,
		`name="arguments"`,
		`name="timeout_seconds"`,
		`<code>` + scriptName + `</code>`,
	} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("edit Quick Run task missing %q: %s", expected, taskPage)
		}
	}
	if strings.Contains(string(taskPage), `name="script"`) {
		t.Fatalf("edit Quick Run task allows script changes: %s", taskPage)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/update", url.Values{
		"csrf_token":      {formToken(t, taskPage)},
		"name":            {"Updated name"},
		"arguments":       {"--mode safe"},
		"timeout_seconds": {"90"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update Quick Run status=%d", response.StatusCode)
	}

	page = getQuickRunsPage(t, client, serverURL)
	for _, expected := range []string{"Updated name", "--mode safe", "90s", scriptName} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("updated Quick Run missing %q: %s", expected, page)
		}
	}
	if strings.Contains(string(page), "Original name") {
		t.Fatalf("old Quick Run name remains: %s", page)
	}
}

func TestAdminCanCopyQuickRunNextToItsSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "copyable.sh", "printf 'copyable\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "copyable.cmd", "@echo off\r\necho copyable\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Copies")
	createQuickRunFromFile(t, client, serverURL, scriptName, "Original", groupID)
	page := getQuickRunsPage(t, client, serverURL)
	sourceID := quickRunIDForName(t, page, "Original")

	response, err := client.Get(serverURL + "/config/quick-runs/" + sourceID + "/copy")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{
		`data-task-kind="quick-copy"`,
		`name="name" autocomplete="off" value="Original copy"`,
		`<code>` + scriptName + `</code>`,
		`value="` + groupID + `" selected`,
	} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("copy Quick Run task missing %q: %s", expected, taskPage)
		}
	}
	if strings.Contains(string(taskPage), `name="script"`) {
		t.Fatalf("copy Quick Run task exposes a script field: %s", taskPage)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+sourceID+"/copy", url.Values{
		"csrf_token":      {formToken(t, taskPage)},
		"name":            {"Replica"},
		"arguments":       {"--copy"},
		"timeout_seconds": {"12"},
		"group_id":        {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("copy Quick Run status=%d", response.StatusCode)
	}

	page = getQuickRunsPage(t, client, serverURL)
	original, replica := strings.Index(string(page), ">Original<"), strings.Index(string(page), ">Replica<")
	if original < 0 || replica < 0 || replica < original {
		t.Fatalf("copy is not adjacent after its source: %s", page)
	}
	for _, expected := range []string{"--copy", "12s"} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("copy missing %q: %s", expected, page)
		}
	}
	if replicaID := quickRunIDForName(t, page, "Replica"); replicaID == sourceID {
		t.Fatalf("copy reused source id %q", sourceID)
	}
}

func TestQuickRunSoftLockBlocksEditingAndDeletionUntilUnlocked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "locked.sh", "printf 'locked\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "locked.cmd", "@echo off\r\necho locked\r\n"
	}
	if err := os.WriteFile(filepath.Join(managedRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	createQuickRunFromFile(t, client, serverURL, scriptName, "Protected", "")
	page := getQuickRunsPage(t, client, serverURL)
	quickRunID := quickRunIDForName(t, page, "Protected")
	token := formToken(t, page)

	response, err := client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/lock", url.Values{
		"csrf_token": {token},
		"locked":     {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("lock Quick Run status=%d", response.StatusCode)
	}

	page = getQuickRunsPage(t, client, serverURL)
	if !strings.Contains(string(page), `data-quick-run-id="`+quickRunID+`" data-locked="true"`) || !strings.Contains(string(page), "Locked") {
		t.Fatalf("locked state is not visible: %s", page)
	}
	for _, expected := range []string{
		`data-group-toggle`,
		`href="/config/quick-runs/` + quickRunID + `/copy"`,
		`href="/config/quick-runs/` + quickRunID + `/move-group"`,
		`action="/config/quick-runs/` + quickRunID + `/lock"`,
		`name="locked" value="0"`,
		`data-locked-action="edit" aria-disabled="true"`,
		`data-locked-action="delete"`,
	} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("locked Quick Run UI missing %q: %s", expected, page)
		}
	}
	response, err = client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("locked edit task status=%d, want 409", response.StatusCode)
	}
	for path, values := range map[string]url.Values{
		"/update": {
			"csrf_token":      {formToken(t, page)},
			"name":            {"Tampered"},
			"arguments":       {"--unsafe"},
			"timeout_seconds": {"1"},
		},
		"/delete": {
			"csrf_token": {formToken(t, page)},
			"confirm":    {"yes"},
		},
	} {
		response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+path, values)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("locked mutation %s status=%d, want 409", path, response.StatusCode)
		}
	}
	if page = getQuickRunsPage(t, client, serverURL); !strings.Contains(string(page), "Protected") || strings.Contains(string(page), "Tampered") {
		t.Fatalf("locked Quick Run changed or disappeared: %s", page)
	}

	targetGroupID, _ := createQuickRunGroup(t, client, serverURL, "Protected group")
	page = getQuickRunsPage(t, client, serverURL)
	for path, values := range map[string]url.Values{
		"/move-group": {
			"csrf_token": {formToken(t, page)},
			"group_id":   {targetGroupID},
		},
		"/move": {
			"csrf_token": {formToken(t, page)},
			"direction":  {"up"},
		},
		"/copy": {
			"csrf_token":      {formToken(t, page)},
			"name":            {"Protected copy"},
			"arguments":       {""},
			"timeout_seconds": {"0"},
			"group_id":        {targetGroupID},
		},
		"/start": {
			"csrf_token": {formToken(t, page)},
		},
	} {
		response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+path, values)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("locked allowed operation %s status=%d, want 303", path, response.StatusCode)
		}
	}
	page = getQuickRunsPage(t, client, serverURL)
	groupPattern := regexp.MustCompile(`data-quick-run-group="` + regexp.QuoteMeta(targetGroupID) + `"[\s\S]*?<h3>Protected</h3>`)
	if !groupPattern.Match(page) {
		t.Fatalf("locked Quick Run did not move groups: %s", page)
	}
	copyID := quickRunIDForName(t, page, "Protected copy")
	if !strings.Contains(string(page), `data-quick-run-id="`+copyID+`" data-locked="false"`) {
		t.Fatalf("copy of locked Quick Run should be unlocked: %s", page)
	}

	response, err = client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/lock", url.Values{
		"csrf_token": {formToken(t, page)},
		"locked":     {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlocked edit task status=%d, want 200", response.StatusCode)
	}
}

func quickRunIDForName(t *testing.T, page []byte, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`data-quick-run-id="([^"]+)"[\s\S]*?<h3>([^<]+)</h3>`)
	for _, match := range pattern.FindAllSubmatch(page, -1) {
		if len(match) == 3 && string(match[2]) == name {
			return string(match[1])
		}
	}
	t.Fatalf("Quick Run %q id not found in response: %s", name, page)
	return ""
}

func getQuickRunsPage(t *testing.T, client *http.Client, serverURL string) []byte {
	t.Helper()
	response, err := client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return page
}

func createQuickRunFromFile(t *testing.T, client *http.Client, serverURL, scriptName, name, groupID string) {
	t.Helper()
	response, err := client.Get(serverURL + "/resources/files/quick-run/" + scriptName)
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/quick-runs", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {name},
		"script":     {scriptName},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create Quick Run status=%d", response.StatusCode)
	}
}

func createQuickRunGroup(t *testing.T, client *http.Client, serverURL, name string) (string, string) {
	t.Helper()
	response, err := client.Get(serverURL + "/config/quick-runs/groups/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	token := formToken(t, taskPage)
	response, err = client.PostForm(serverURL+"/config/quick-runs/groups", url.Values{
		"csrf_token": {token},
		"name":       {name},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	pattern := regexp.MustCompile(`data-quick-run-group="([^"]+)" data-group-name="([^"]+)"`)
	for _, match := range pattern.FindAllSubmatch(page, -1) {
		if len(match) == 3 && string(match[2]) == name {
			return string(match[1]), formToken(t, page)
		}
	}
	t.Fatalf("group %q id not found in response: %s", name, page)
	return "", ""
}
