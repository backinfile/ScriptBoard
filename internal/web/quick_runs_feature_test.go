package web_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"html"
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
	firstID, _ := createQuickRunGroup(t, client, serverURL, "First")
	secondID, token := createQuickRunGroup(t, client, serverURL, "Second")

	response, err := client.PostForm(serverURL+"/config/quick-runs/reorder", url.Values{
		"csrf_token": {token},
		"group_id":   {secondID, firstID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder groups status=%d", response.StatusCode)
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

func TestQuickRunReorderModeUsesDragHandlesWithoutLegacyMoveActions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "reorder.cmd", "@echo off\r\necho reorder\r\n"
	if runtime.GOOS != "windows" {
		scriptName, scriptContent = "reorder.sh", "printf 'reorder\\n'\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Ordered")
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Deploy", groupID)
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Deploy backup", groupID)

	page := getQuickRunsPage(t, client, serverURL)
	for _, expected := range []string{`href="/config/quick-runs?reorder=1"`, `data-quick-run-reorder-toggle`, `>Reorder</a>`} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("normal page missing reorder entry %q: %s", expected, page)
		}
	}
	heading := regexp.MustCompile(`(?s)<div class="heading-actions quick-run-heading-actions">(.*?)</div>`).FindSubmatch(page)
	if len(heading) != 2 || strings.Index(string(heading[1]), `data-quick-run-reorder-toggle`) > strings.Index(string(heading[1]), `/config/quick-runs/groups/new`) {
		t.Fatalf("global reorder button must appear before create group: %s", page)
	}
	if strings.Contains(string(page), `quick-run-group__reorder`) || strings.Contains(string(page), `data-quick-run-drag-handle`) {
		t.Fatalf("normal page still exposes per-group reorder controls or active drag handles: %s", page)
	}
	for _, removed := range []string{`action="/config/quick-runs/groups/` + groupID + `/move"`, `action="/config/quick-runs/` + quickRunIDForName(t, page, "Deploy") + `/move"`, `name="direction"`} {
		if strings.Contains(string(page), removed) {
			t.Fatalf("normal page still contains legacy move action %q: %s", removed, page)
		}
	}

	response, err := client.Get(serverURL + "/config/quick-runs?reorder=1")
	if err != nil {
		t.Fatal(err)
	}
	reorderPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-quick-run-reorder-url="/config/quick-runs/reorder"`, `data-quick-run-reorder-active="true"`, `data-quick-run-drag-handle`, `data-quick-run-reorder-finish`, `aria-live="polite"`, `class="action-menu"`, `class="button button--compact qr__run"`} {
		if !strings.Contains(string(reorderPage), expected) {
			t.Fatalf("reorder page missing %q: %s", expected, reorderPage)
		}
	}
	if count := strings.Count(string(reorderPage), `data-quick-run-reorder-guidance`); count != 1 {
		t.Fatalf("reorder page guidance count=%d, want one global guidance block: %s", count, reorderPage)
	}
	for _, removed := range []string{`data-quick-run-group-drag-handle`, `class="quick-run-reorder-handle`} {
		if strings.Contains(string(reorderPage), removed) {
			t.Fatalf("reorder page changed the normal card structure with %q: %s", removed, reorderPage)
		}
	}
}

func TestQuickRunReorderRejectsAnIncompleteInventoryWithoutChangingOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	firstID, _ := createQuickRunGroup(t, client, serverURL, "First")
	_, token := createQuickRunGroup(t, client, serverURL, "Second")

	response, err := client.PostForm(serverURL+"/config/quick-runs/reorder", url.Values{
		"csrf_token": {token},
		"group_id":   {firstID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("incomplete reorder status=%d, want %d", response.StatusCode, http.StatusConflict)
	}

	page := getQuickRunsPage(t, client, serverURL)
	first, second := strings.Index(string(page), ">First<"), strings.Index(string(page), ">Second<")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("rejected reorder changed group order: %s", page)
	}
}

func TestQuickRunGroupMoveTopIsImmediateAndPreservesRelativeOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	firstID, _ := createQuickRunGroup(t, client, serverURL, "First")
	secondID, _ := createQuickRunGroup(t, client, serverURL, "Second")
	thirdID, token := createQuickRunGroup(t, client, serverURL, "Third")

	page := getQuickRunsPage(t, client, serverURL)
	for _, groupID := range []string{firstID, secondID, thirdID} {
		action := `action="/config/quick-runs/groups/` + groupID + `/move-top"`
		if !strings.Contains(string(page), action) {
			t.Fatalf("group menu missing one-time move-to-top action %q: %s", action, page)
		}
	}

	response, err := client.PostForm(serverURL+"/config/quick-runs/groups/"+thirdID+"/move-top", url.Values{"csrf_token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move group to top status=%d", response.StatusCode)
	}
	assertQuickRunGroupOrder(t, getQuickRunsPage(t, client, serverURL), "Third", "First", "Second")

	response, err = client.PostForm(serverURL+"/config/quick-runs/groups/"+thirdID+"/move-top", url.Values{"csrf_token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("repeat move group to top status=%d", response.StatusCode)
	}
	assertQuickRunGroupOrder(t, getQuickRunsPage(t, client, serverURL), "Third", "First", "Second")

	response, err = client.PostForm(serverURL+"/config/quick-runs/groups/"+secondID+"/move-top", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("move group to top without CSRF status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}
	assertQuickRunGroupOrder(t, getQuickRunsPage(t, client, serverURL), "Third", "First", "Second")
}

func assertQuickRunGroupOrder(t *testing.T, page []byte, names ...string) {
	t.Helper()
	last := -1
	for _, name := range names {
		position := strings.Index(string(page), `data-group-name="`+name+`"`)
		if position < 0 || position <= last {
			t.Fatalf("Quick Run group order does not contain %v in sequence: %s", names, page)
		}
		last = position
	}
}

func TestAdminCanCreateQuickRunInAGroupFromHostFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "grouped.sh", "printf 'grouped\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "grouped.cmd", "@echo off\r\necho grouped\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Deployment")

	scriptPath := filepath.Join(hostRoot, scriptName)
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/quick-run", scriptPath))
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
		"script":     {scriptPath},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create grouped Quick Run status=%d", response.StatusCode)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision, digest string
	if err := database.QueryRow(`SELECT resource_revision, resource_digest_sha256 FROM audit_events
		WHERE action='create_quick_run' ORDER BY id DESC LIMIT 1`).Scan(&revision, &digest); err != nil {
		t.Fatal(err)
	}
	expectedDigest := sha256.Sum256([]byte(scriptContent))
	if revision != "1" || digest != hex.EncodeToString(expectedDigest[:]) {
		t.Fatalf("Quick Run audit revision=%q digest=%q", revision, digest)
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
	var quickRunID string
	if err := database.QueryRow(`SELECT id FROM quick_runs WHERE name = 'Grouped deploy'`).Scan(&quickRunID); err != nil {
		t.Fatal(err)
	}
	started, err := client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/start", url.Values{"csrf_token": {formToken(t, page)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = started.Body.Close()
	var runID string
	if err := database.QueryRow(`SELECT id FROM runs WHERE source_type = 'admin/quick-run' AND source_id = ? ORDER BY created_at DESC LIMIT 1`, quickRunID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	detailResponse, err := client.Get(serverURL + "/history/runs/" + runID)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := io.ReadAll(detailResponse.Body)
	_ = detailResponse.Body.Close()
	if !bytes.Contains(detail, []byte("Deployment / Grouped deploy")) || !bytes.Contains(detail, []byte("Back to Quick Runs")) || !bytes.Contains(detail, []byte(`href="/config/quick-runs"`)) {
		t.Fatalf("Quick Run detail is missing its group source or return link: %s", detail)
	}
	if !bytes.Contains(detail, []byte(`<h1>Grouped deploy</h1>`)) || !bytes.Contains(detail, []byte(`action="/config/quick-runs/`+quickRunID+`/start"`)) ||
		!bytes.Contains(detail, []byte(`data-run-rerun`)) || bytes.Contains(detail, []byte("save-quick-run")) {
		t.Fatalf("Quick Run detail title or actions are incorrect: %s", detail)
	}
}

func TestDeletingQuickRunGroupMovesItsItemsToUngrouped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "preserved.sh", "printf 'preserved\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "preserved.cmd", "@echo off\r\necho preserved\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	groupID, token := createQuickRunGroup(t, client, serverURL, "Temporary")
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "Preserved item", groupID)

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
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "movable.sh", "printf 'movable\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "movable.cmd", "@echo off\r\necho movable\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	sourceGroupID, _ := createQuickRunGroup(t, client, serverURL, "Source")
	targetGroupID, _ := createQuickRunGroup(t, client, serverURL, "Target")
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "Movable item", sourceGroupID)
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
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "ordered.sh", "printf 'ordered\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "ordered.cmd", "@echo off\r\necho ordered\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Ordered")
	otherGroupID, _ := createQuickRunGroup(t, client, serverURL, "Other")
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "First item", groupID)
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "Second item", groupID)
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "Other item", otherGroupID)
	page := getQuickRunsPage(t, client, serverURL)
	firstID := quickRunIDForName(t, page, "First item")
	secondID := quickRunIDForName(t, page, "Second item")
	otherID := quickRunIDForName(t, page, "Other item")

	response, err := client.PostForm(serverURL+"/config/quick-runs/reorder", url.Values{
		"csrf_token":   {formToken(t, page)},
		"group_id":     {groupID, otherGroupID},
		"quick_run_id": {secondID, firstID, otherID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder Quick Runs status=%d", response.StatusCode)
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
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "historical.sh", "printf 'historical\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "historical.cmd", "@echo off\r\necho historical\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "History")
	filesPageResponse, err := client.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	filesPage, _ := io.ReadAll(filesPageResponse.Body)
	_ = filesPageResponse.Body.Close()
	response, err := client.PostForm(serverURL+"/history/runs/start", url.Values{
		"csrf_token": {formToken(t, filesPage)},
		"script":     {filepath.Join(hostRoot, scriptName)},
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
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "editable.sh", "printf 'editable\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "editable.cmd", "@echo off\r\necho editable\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	scriptPath := filepath.Join(hostRoot, scriptName)
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Original name", "")
	page := getQuickRunsPage(t, client, serverURL)
	quickRunID := quickRunIDForName(t, page, "Original name")
	if !strings.Contains(string(page), `data-quick-run-version="1"`) || !strings.Contains(string(page), `>v1<`) {
		t.Fatalf("Quick Run version is not rendered as a distinct column: %s", page)
	}

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
		`<code>` + scriptPath + `</code>`,
		`name="sync_external_interfaces"`,
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
	for _, expected := range []string{"Updated name", scriptName} {
		if !strings.Contains(string(page), expected) {
			t.Fatalf("updated Quick Run missing %q: %s", expected, page)
		}
	}
	response, err = client.Get(serverURL + "/config/quick-runs/" + quickRunID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	updatedTaskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`name="arguments"`, `value="--mode safe"`, `name="timeout_seconds"`, `value="90"`} {
		if !strings.Contains(string(updatedTaskPage), expected) {
			t.Fatalf("updated Quick Run edit task missing %q: %s", expected, updatedTaskPage)
		}
	}
	if strings.Contains(string(page), "Original name") {
		t.Fatalf("old Quick Run name remains: %s", page)
	}
}

func TestAdminCanCopyQuickRunNextToItsSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "copyable.sh", "printf 'copyable\\n'\n"
	replacementName, replacementContent := "replacement.sh", "printf 'replacement\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "copyable.cmd", "@echo off\r\necho copyable\r\n"
		replacementName, replacementContent = "replacement.cmd", "@echo off\r\necho replacement\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, replacementName), []byte(replacementContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	groupID, _ := createQuickRunGroup(t, client, serverURL, "Copies")
	scriptPath := filepath.Join(hostRoot, scriptName)
	replacementPath := filepath.Join(hostRoot, replacementName)
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Original", groupID)
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
		`name="script" autocomplete="off" spellcheck="false" value="` + html.EscapeString(scriptPath) + `"`,
		`value="` + groupID + `" selected`,
	} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("copy Quick Run task missing %q: %s", expected, taskPage)
		}
	}
	response, err = client.PostForm(serverURL+"/config/quick-runs/"+sourceID+"/copy", url.Values{
		"csrf_token":      {formToken(t, taskPage)},
		"name":            {"Replica"},
		"script":          {replacementPath},
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
	replicaID := quickRunIDForName(t, page, "Replica")
	if replicaID == sourceID {
		t.Fatalf("copy reused source id %q", sourceID)
	}
	response, err = client.Get(serverURL + "/config/quick-runs/" + replicaID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	replicaTaskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`<code>` + replacementPath + `</code>`, `name="arguments"`, `value="--copy"`, `name="timeout_seconds"`, `value="12"`} {
		if !strings.Contains(string(replicaTaskPage), expected) {
			t.Fatalf("copy edit task missing %q: %s", expected, replicaTaskPage)
		}
	}
}

func TestQuickRunSoftLockBlocksEditingAndDeletionUntilUnlocked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "locked.sh", "printf 'locked\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "locked.cmd", "@echo off\r\necho locked\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	createQuickRunFromFile(t, client, serverURL, filepath.Join(hostRoot, scriptName), "Protected", "")
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

func TestQuickRunShowsFiveRecentResultsDurationAndDirectoryAction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName, scriptContent := "weather.sh", "printf 'clear\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName, scriptContent = "weather.cmd", "@echo off\r\necho clear\r\n"
	}
	scriptPath := filepath.Join(hostRoot, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, stateRoot)
	createQuickRunFromFile(t, client, serverURL, scriptPath, "Weather history", "")
	page := getQuickRunsPage(t, client, serverURL)
	quickRunID := quickRunIDForName(t, page, "Weather history")
	directoryLink := `href="` + hostFileHref("/resources/files", hostRoot) + `"`
	if !strings.Contains(string(page), directoryLink) || !strings.Contains(string(page), "Open directory") {
		t.Fatalf("Quick Run directory action missing: %s", page)
	}
	if strings.Contains(string(page), "<dt>Arguments</dt>") || strings.Contains(string(page), "<dt>Timeout</dt>") {
		t.Fatalf("Quick Run still renders arguments or timeout facts: %s", page)
	}

	for index := 0; index < 6; index++ {
		response, err := client.PostForm(serverURL+"/config/quick-runs/"+quickRunID+"/start", url.Values{
			"csrf_token": {formToken(t, page)},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		runPath := response.Header.Get("Location")
		if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(runPath, "/history/runs/") {
			t.Fatalf("start Quick Run status=%d location=%q", response.StatusCode, runPath)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			response, err = client.Get(serverURL + runPath)
			if err != nil {
				t.Fatal(err)
			}
			runPage, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if strings.Contains(string(runPage), `data-run-status data-state="succeeded"`) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Quick Run did not finish: %s", runPage)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	page = getQuickRunsPage(t, client, serverURL)
	if count := strings.Count(string(page), "data-quick-run-history-entry"); count != 5 {
		t.Fatalf("recent result count=%d, want 5: %s", count, page)
	}
	latestPattern := regexp.MustCompile(`<dt>Most recent</dt><dd><time [^>]+>[^<]+</time></dd><dd>[^—<]+</dd>`)
	if !latestPattern.Match(page) {
		t.Fatalf("latest Quick Run time and duration are missing: %s", page)
	}
	historyURL := `/history/runs?q=Weather&#43;history&amp;quick_run_id=` + quickRunID + `&amp;focus=search`
	if !strings.Contains(string(page), historyURL) || !strings.Contains(string(page), "View all runs") {
		t.Fatalf("all Quick Run history action missing: %s", page)
	}
	if count := strings.Count(string(page), `title="Status: Succeeded · Start time:`); count != 5 {
		t.Fatalf("recent Run hover tips with start time=%d, want 5: %s", count, page)
	}
	if count := len(regexp.MustCompile(`title="[^"]*Run duration:`).FindAll(page, -1)); count != 5 {
		t.Fatalf("recent Run hover tips with duration=%d, want 5: %s", count, page)
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

func createQuickRunFromFile(t *testing.T, client *http.Client, serverURL, scriptPath, name, groupID string) {
	t.Helper()
	response, err := client.Get(hostFileRequestURL(serverURL, "/resources/files/quick-run", scriptPath))
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/quick-runs", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {name},
		"script":     {scriptPath},
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
