package app_test

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFilesPageShowsManagedRootLocation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed & scripts")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(managedRoot)
	if err != nil {
		t.Fatalf("resolve managed root links: %v", err)
	}
	absoluteRoot, err := filepath.Abs(canonicalRoot)
	if err != nil {
		t.Fatalf("resolve managed root: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`class="managed-root-location"`,
		"Managed root location",
		html.EscapeString(absoluteRoot),
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}
}

func TestFilesPageOffersDropUploadForCurrentDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "nested"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/nested/")
	if err != nil {
		t.Fatalf("get nested files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read nested files: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`data-file-drop-form`,
		`data-file-upload-form`,
		`action="/resources/files/upload"`,
		`enctype="multipart/form-data"`,
		`name="path" value="nested"`,
		`name="conflict_action" value=""`,
		`name="files" type="file" multiple required`,
		`data-file-drop-zone`,
		`Drop files here to upload`,
		`Choose files`,
		`<noscript>`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files page does not contain %q: %s", expected, page)
		}
	}
	if strings.Contains(page, `name="replace"`) {
		t.Fatalf("files page still exposes a replace-by-default control: %s", page)
	}
}

func TestFilesPageSearchesCurrentDirectoryAndHighlightsMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "nested"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	for name, content := range map[string]string{
		"Deploy & Verify.ps1": "Write-Output verified",
		"notes.txt":           "deploy belongs in content only",
	} {
		if err := os.WriteFile(filepath.Join(managedRoot, name), []byte(content), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "nested", "nested-deploy.ps1"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("create nested match: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/?q=%20DEPLOY%20")
	if err != nil {
		t.Fatalf("search files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files search: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`data-file-search`,
		`data-search-input`,
		`value="DEPLOY"`,
		`Found <strong>1</strong> item in this directory`,
		`<mark>Deploy</mark> &amp; Verify.ps1`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("files search does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{"notes.txt", "nested-deploy.ps1"} {
		if strings.Contains(page, excluded) {
			t.Fatalf("files search unexpectedly contains %q: %s", excluded, page)
		}
	}
}

func TestFilesPageSortsByVisibleTypeAndPreservesSortAcrossDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "workspace"), 0o755); err != nil {
		t.Fatalf("create workspace directory: %v", err)
	}
	for name := range map[string]struct{}{
		"automation.ps1": {},
		"diagram.png":    {},
		"notes.txt":      {},
		"archive.bin":    {},
	} {
		if err := os.WriteFile(filepath.Join(managedRoot, name), []byte(name), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/?sort=type&direction=asc&q=auto")
	if err != nil {
		t.Fatalf("get typed file sort: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read typed file sort: %v", err)
	}
	filteredPage := string(body)
	for _, expected := range []string{
		`value="type" selected`,
		`Type · Ascending`,
		`Runnable script`,
		`href="/resources/files/?direction=asc&amp;sort=type"`,
	} {
		if !strings.Contains(filteredPage, expected) {
			t.Fatalf("typed file sort does not contain %q: %s", expected, filteredPage)
		}
	}

	response, err = client.Get(serverURL + "/resources/files/?sort=type&direction=asc")
	if err != nil {
		t.Fatalf("get all typed files: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read all typed files: %v", err)
	}
	page := string(body)
	ordered := []string{"workspace", "automation.ps1", "diagram.png", "notes.txt", "archive.bin"}
	last := -1
	for _, name := range ordered {
		position := strings.Index(page, ">"+name+"<")
		if position < 0 {
			t.Fatalf("typed file sort is missing %q: %s", name, page)
		}
		if position <= last {
			t.Fatalf("typed file sort order %v is not preserved: %s", ordered, page)
		}
		last = position
	}
	for _, label := range []string{"Directory", "Runnable script", "Image", "Previewable text", "Other file"} {
		if !strings.Contains(page, label) {
			t.Fatalf("typed file sort is missing label %q: %s", label, page)
		}
	}
	if !strings.Contains(page, `href="/resources/files/workspace/?direction=asc&amp;sort=type"`) {
		t.Fatalf("directory link does not clear the query and preserve sorting: %s", page)
	}
}

func TestFilesPageNormalizesSortAndShowsDedicatedNoResultsState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "visible.txt"), []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/?q=missing&sort=unknown&direction=desc&page=9")
	if err != nil {
		t.Fatalf("get empty search result: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read empty search result: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`data-no-search-results`,
		`No filenames match`,
		`<code>missing</code>`,
		`href="/resources/files/"`,
		`<option value="" selected>Natural order</option>`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("empty search result does not contain %q: %s", expected, page)
		}
	}
	for _, excluded := range []string{`class="file-table"`, `class="pagination"`, `visible.txt`, `files.unknown`} {
		if strings.Contains(page, excluded) {
			t.Fatalf("empty search result unexpectedly contains %q: %s", excluded, page)
		}
	}
}

func TestFilesPagePaginationPreservesNormalizedSearchState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	for index := 1; index <= 22; index++ {
		name := fmt.Sprintf("report-%02d.txt", index)
		if err := os.WriteFile(filepath.Join(managedRoot, name), []byte(name), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/?q=%20REPORT%20&sort=name&direction=desc")
	if err != nil {
		t.Fatalf("get paginated file search: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read paginated file search: %v", err)
	}
	page := string(body)
	for _, expected := range []string{
		`Found <strong>22</strong> items in this directory`,
		`href="/resources/files/?direction=desc&amp;page=2&amp;q=REPORT&amp;sort=name"`,
		`href="/resources/files/?direction=desc&amp;sort=name"`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("paginated file search does not contain %q: %s", expected, page)
		}
	}
}

func TestFilesPageListsManagedEntriesAndHidesReservedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "alpha"), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "zeta.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(managedRoot, ".scriptboard-trash"), 0o755); err != nil {
		t.Fatalf("create trash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, ".scriptboard-trash", "hidden.txt"), []byte("hidden"), 0o644); err != nil {
		t.Fatalf("create hidden file: %v", err)
	}

	linkCreated := os.Symlink(root, filepath.Join(managedRoot, "outside")) == nil
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d, want %d: %s", response.StatusCode, http.StatusOK, page)
	}
	if !strings.Contains(page, "alpha") || !strings.Contains(page, "zeta.txt") {
		t.Fatalf("managed entries missing from page: %s", page)
	}
	if strings.Contains(page, ".scriptboard-trash") || strings.Contains(page, "hidden.txt") {
		t.Fatalf("reserved trash leaked into page: %s", page)
	}
	if strings.Index(page, "alpha") > strings.Index(page, "zeta.txt") {
		t.Fatalf("directory is not listed before file: %s", page)
	}
	if linkCreated && (!strings.Contains(page, "outside") || !strings.Contains(page, "Restricted entry")) {
		t.Fatalf("link is not shown as restricted: %s", page)
	}
}

func TestAdminCanBrowseNestedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "子目录"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "子目录", "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatalf("create nested file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/%E5%AD%90%E7%9B%AE%E5%BD%95/")
	if err != nil {
		t.Fatalf("browse nested directory: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read nested directory: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "inside.txt") {
		t.Fatalf("nested directory response: status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), `?path=%E5%AD%90%E7%9B%AE%E5%BD%95`) {
		t.Fatalf("nested operations do not preserve current path: %s", body)
	}
}

func TestAdminCanMoveAndRenameManagedEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(managedRoot, "source"), 0o755); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(managedRoot, "target"), 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "source", "old.txt"), []byte("move me"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/resources/files/source/")
	if err != nil {
		t.Fatalf("get source directory: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read source directory: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":      {"source/old.txt"},
		"destination": {"target/new.txt"},
		"csrf_token":  {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("move file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/files/target/" {
		t.Fatalf("move response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "source", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(managedRoot, "target", "new.txt"))
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(content) != "move me" {
		t.Fatalf("moved content = %q", content)
	}
}

func TestMoveNameConflictRequiresAChoiceAndKeepsOverwriteRecoverable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	for _, directory := range []string{"source", "target"} {
		if err := os.MkdirAll(filepath.Join(managedRoot, directory), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	for relative, content := range map[string]string{
		"source/item.txt":      "incoming",
		"target/item.txt":      "current",
		"source/overwrite.txt": "incoming overwrite",
		"target/overwrite.txt": "current overwrite",
	} {
		if err := os.WriteFile(filepath.Join(managedRoot, filepath.FromSlash(relative)), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/resources/files/source/")
	if err != nil {
		t.Fatalf("get source directory: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read source directory: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":      {"source/item.txt"},
		"destination": {"target/item.txt"},
		"csrf_token":  {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("request move conflict: %v", err)
	}
	conflictPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read move conflict: %v", err)
	}
	if response.StatusCode != http.StatusConflict ||
		!bytes.Contains(conflictPage, []byte(`data-file-conflict`)) ||
		!bytes.Contains(conflictPage, []byte(`value="item (2).txt"`)) {
		t.Fatalf("move conflict response: status=%d body=%s", response.StatusCode, conflictPage)
	}
	if current, err := os.ReadFile(filepath.Join(managedRoot, "target", "item.txt")); err != nil || string(current) != "current" {
		t.Fatalf("move conflict changed current target: content=%q err=%v", current, err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {"source/item.txt"},
		"destination":     {"target/item.txt"},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"rename"},
		"new_name":        {"../escaped.txt"},
	})
	if err != nil {
		t.Fatalf("reject path-like rename: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("path-like rename status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "source", "item.txt")); err != nil {
		t.Fatalf("path-like rename changed source: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {"source/item.txt"},
		"destination":     {"target/item.txt"},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"rename"},
		"new_name":        {"item-copy.txt"},
	})
	if err != nil {
		t.Fatalf("rename incoming move: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/files/target/" {
		t.Fatalf("rename move response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if renamed, err := os.ReadFile(filepath.Join(managedRoot, "target", "item-copy.txt")); err != nil || string(renamed) != "incoming" {
		t.Fatalf("renamed move content=%q err=%v", renamed, err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/move", url.Values{
		"source":          {"source/overwrite.txt"},
		"destination":     {"target/overwrite.txt"},
		"csrf_token":      {formToken(t, page)},
		"conflict_action": {"overwrite"},
	})
	if err != nil {
		t.Fatalf("overwrite move target: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("overwrite move status=%d", response.StatusCode)
	}
	if overwritten, err := os.ReadFile(filepath.Join(managedRoot, "target", "overwrite.txt")); err != nil || string(overwritten) != "incoming overwrite" {
		t.Fatalf("overwrite move content=%q err=%v", overwritten, err)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash after overwrite move: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash after overwrite move: %v", err)
	}
	if !bytes.Contains(trashPage, []byte("target/overwrite.txt")) {
		t.Fatalf("overwritten move target was not retained in trash: %s", trashPage)
	}
}

func postManagedUpload(t *testing.T, client *http.Client, serverURL, csrfToken, relative, name, content, conflictAction string) (int, []byte) {
	t.Helper()
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	for _, field := range []struct{ name, value string }{
		{name: "csrf_token", value: csrfToken},
		{name: "path", value: relative},
		{name: "conflict_action", value: conflictAction},
	} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			t.Fatalf("write upload field %s: %v", field.name, err)
		}
	}
	filePart, err := writer.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("create upload part: %v", err)
	}
	if _, err := filePart.Write([]byte(content)); err != nil {
		t.Fatalf("write upload part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close upload body: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read upload response: %v", err)
	}
	return response.StatusCode, body
}

func TestUploadNameConflictDefaultsToSkipAndSupportsRename(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "same.txt"), []byte("current"), 0o644); err != nil {
		t.Fatalf("write current file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	csrfToken := formToken(t, page)

	response, err = client.PostForm(serverURL+"/resources/files/conflicts", url.Values{
		"csrf_token": {csrfToken},
		"path":       {""},
		"name":       {"same.txt", "new.txt"},
	})
	if err != nil {
		t.Fatalf("preflight upload conflicts: %v", err)
	}
	preflight, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read upload conflicts: %v", err)
	}
	if response.StatusCode != http.StatusOK ||
		!bytes.Contains(preflight, []byte(`"name":"same.txt"`)) ||
		!bytes.Contains(preflight, []byte(`"suggested":"same (2).txt"`)) ||
		bytes.Contains(preflight, []byte(`"name":"new.txt"`)) {
		t.Fatalf("upload conflict preflight: status=%d body=%s", response.StatusCode, preflight)
	}
	response, err = client.PostForm(serverURL+"/resources/files/conflicts", url.Values{
		"csrf_token": {csrfToken},
		"path":       {""},
		"name":       {"../outside.txt"},
	})
	if err != nil {
		t.Fatalf("preflight invalid upload name: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid upload name status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}

	status, result := postManagedUpload(t, client, serverURL, csrfToken, "", "same.txt", "incoming", "")
	if status != http.StatusMultiStatus || !bytes.Contains(result, []byte("Skipped")) {
		t.Fatalf("default conflict result: status=%d body=%s", status, result)
	}
	if current, err := os.ReadFile(filepath.Join(managedRoot, "same.txt")); err != nil || string(current) != "current" {
		t.Fatalf("default conflict changed current file: content=%q err=%v", current, err)
	}

	status, result = postManagedUpload(t, client, serverURL, csrfToken, "", "same.txt", "incoming", "rename")
	if status != http.StatusOK || !bytes.Contains(result, []byte("same (2).txt")) {
		t.Fatalf("rename conflict result: status=%d body=%s", status, result)
	}
	if renamed, err := os.ReadFile(filepath.Join(managedRoot, "same (2).txt")); err != nil || string(renamed) != "incoming" {
		t.Fatalf("renamed upload content=%q err=%v", renamed, err)
	}
	if current, err := os.ReadFile(filepath.Join(managedRoot, "same.txt")); err != nil || string(current) != "current" {
		t.Fatalf("renamed upload changed current file: content=%q err=%v", current, err)
	}
}

func TestAdminCanStreamUploadAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	if err := writer.WriteField("csrf_token", formToken(t, page)); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	if err := writer.WriteField("path", ""); err != nil {
		t.Fatalf("write path field: %v", err)
	}
	filePart, err := writer.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := filePart.Write([]byte("hello from upload")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	resultPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(resultPage, []byte("hello.txt")) || !bytes.Contains(resultPage, []byte("Succeeded")) {
		t.Fatalf("upload response: status=%d body=%q", response.StatusCode, resultPage)
	}
	content, err := os.ReadFile(filepath.Join(managedRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(content) != "hello from upload" {
		t.Fatalf("uploaded content = %q", content)
	}

	response, err = client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files before replacement: %v", err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files before replacement: %v", err)
	}
	requestBody.Reset()
	writer = multipart.NewWriter(&requestBody)
	for name, value := range map[string]string{
		"csrf_token":      formToken(t, page),
		"path":            "",
		"conflict_action": "overwrite",
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write replacement field %s: %v", name, err)
		}
	}
	filePart, err = writer.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatalf("create replacement file part: %v", err)
	}
	if _, err := filePart.Write([]byte("replacement upload")); err != nil {
		t.Fatalf("write replacement file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close replacement multipart writer: %v", err)
	}
	request, err = http.NewRequest(http.MethodPost, serverURL+"/resources/files/upload", &requestBody)
	if err != nil {
		t.Fatalf("create replacement upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("replace uploaded file: %v", err)
	}
	resultPage, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read replacement response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(resultPage, []byte("Succeeded")) {
		t.Fatalf("replacement response: status=%d body=%q", response.StatusCode, resultPage)
	}
	content, err = os.ReadFile(filepath.Join(managedRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read replacement file: %v", err)
	}
	if string(content) != "replacement upload" {
		t.Fatalf("replacement content = %q", content)
	}
	matches, err := filepath.Glob(filepath.Join(managedRoot, ".scriptboard-upload-*"))
	if err != nil {
		t.Fatalf("glob upload temporary files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("upload temporary files remain: %v", matches)
	}
}

func TestAdminCanCreateDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/mkdir", url.Values{
		"path":       {""},
		"name":       {"新目录"},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("create directory: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/files/" {
		t.Fatalf("create directory response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	info, err := os.Stat(filepath.Join(managedRoot, "新目录"))
	if err != nil {
		t.Fatalf("stat created directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestAdminCanDownloadARegularFileWithRange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "report.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	request, err := http.NewRequest(http.MethodGet, serverURL+"/resources/files/download/report.txt", nil)
	if err != nil {
		t.Fatalf("create download request: %v", err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("download file: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if response.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range response: status=%d body=%q", response.StatusCode, body)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "report.txt") {
		t.Fatalf("content disposition = %q", disposition)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
}

func TestAdminCanMoveFileToTrashAndRestoreIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "recover.txt"), []byte("recover me"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {"recover.txt"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/trash" {
		t.Fatalf("delete response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "recover.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file remains at original path: %v", err)
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if !strings.Contains(string(trashPage), "recover.txt") {
		t.Fatalf("trash entry missing: %s", trashPage)
	}

	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("restore file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/files/" {
		t.Fatalf("restore response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	content, err := os.ReadFile(filepath.Join(managedRoot, "recover.txt"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "recover me" {
		t.Fatalf("restored content = %q", content)
	}
}

func TestAdminCanRestoreTrashEntryWhenOriginalPathIsOccupied(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	originalPath := filepath.Join(managedRoot, "recover.txt")
	if err := os.WriteFile(originalPath, []byte("recover me"), 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {"recover.txt"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete response status = %d", response.StatusCode)
	}
	if err := os.WriteFile(originalPath, []byte("new file"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}

	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("request restore conflict: %v", err)
	}
	conflictPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read restore conflict: %v", err)
	}
	if response.StatusCode != http.StatusConflict ||
		!bytes.Contains(conflictPage, []byte(`data-file-conflict`)) ||
		!bytes.Contains(conflictPage, []byte(`value="overwrite"`)) ||
		!bytes.Contains(conflictPage, []byte(`value="rename"`)) ||
		!bytes.Contains(conflictPage, []byte(`value="recover (2).txt"`)) {
		t.Fatalf("restore conflict response: status=%d body=%s", response.StatusCode, conflictPage)
	}
	current, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read current file: %v", err)
	}
	if string(current) != "new file" {
		t.Fatalf("current content = %q", current)
	}

	response, err = client.PostForm(serverURL+"/resources/trash/restore", url.Values{
		"id":              {hiddenValue(t, conflictPage, "id")},
		"csrf_token":      {formToken(t, conflictPage)},
		"conflict_action": {"overwrite"},
	})
	if err != nil {
		t.Fatalf("overwrite occupied restore target: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/files/" {
		t.Fatalf("overwrite restore response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	restored, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(restored) != "recover me" {
		t.Fatalf("restored content = %q", restored)
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash after overwrite restore: %v", err)
	}
	updatedTrashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash after overwrite restore: %v", err)
	}
	if !bytes.Contains(updatedTrashPage, []byte("recover.txt")) {
		t.Fatalf("overwritten current file was not retained in trash: %s", updatedTrashPage)
	}
}

func TestAdminCanPermanentlyPurgeTrashEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "purge.txt"), []byte("delete forever"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)
	response, err := client.Get(serverURL + "/resources/files/")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/files/delete", url.Values{
		"path":       {"purge.txt"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("trash file: %v", err)
	}
	_ = response.Body.Close()
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/resources/trash/purge", url.Values{
		"id":         {hiddenValue(t, trashPage, "id")},
		"confirm":    {"yes"},
		"csrf_token": {formToken(t, trashPage)},
	})
	if err != nil {
		t.Fatalf("purge trash: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/trash" {
		t.Fatalf("purge response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	entries, err := os.ReadDir(filepath.Join(managedRoot, ".scriptboard-trash"))
	if err != nil {
		t.Fatalf("read trash directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("purged content remains: %v", entries)
	}
}

func TestTextEditRejectsAnExternalChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	filePath := filepath.Join(managedRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/edit/note.txt")
	if err != nil {
		t.Fatalf("get editor: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read editor: %v", err)
	}
	if !strings.Contains(string(page), "original") {
		t.Fatalf("editor does not contain original text: %s", page)
	}
	if err := os.WriteFile(filePath, []byte("external change"), 0o644); err != nil {
		t.Fatalf("write external change: %v", err)
	}

	response, err = client.PostForm(serverURL+"/resources/files/edit/note.txt", url.Values{
		"content":    {"my change"},
		"digest":     {hiddenValue(t, page, "digest")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("save text: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file after conflict: %v", err)
	}
	if string(content) != "external change" {
		t.Fatalf("external change was overwritten: %q", content)
	}
}

func TestTextEditAtomicallySavesAndKeepsOldVersionInTrash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatalf("create managed root: %v", err)
	}
	filePath := filepath.Join(managedRoot, "note.txt")
	if err := os.WriteFile(filePath, []byte("before"), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}
	client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

	response, err := client.Get(serverURL + "/resources/files/edit/note.txt")
	if err != nil {
		t.Fatalf("get editor: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read editor: %v", err)
	}
	response, err = client.PostForm(serverURL+"/resources/files/edit/note.txt", url.Values{
		"content":    {"after"},
		"digest":     {hiddenValue(t, page, "digest")},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("save text: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusSeeOther)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(content) != "after" {
		t.Fatalf("saved content = %q", content)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat saved file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("saved permissions = %o, want 640", info.Mode().Perm())
	}
	response, err = client.Get(serverURL + "/resources/trash")
	if err != nil {
		t.Fatalf("get trash: %v", err)
	}
	trashPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if !strings.Contains(string(trashPage), "note.txt") {
		t.Fatalf("old version missing from trash: %s", trashPage)
	}
}

func TestTextEditPreservesExistingLineEndingStyle(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		original  string
		submitted string
		want      string
	}{
		{
			name:      "LF",
			original:  "#!/bin/sh\necho before\n",
			submitted: "#!/bin/sh\r\necho after\r\n",
			want:      "#!/bin/sh\necho after\n",
		},
		{
			name:      "CRLF",
			original:  "@echo off\r\necho before\r\n",
			submitted: "@echo off\r\necho after\r\n",
			want:      "@echo off\r\necho after\r\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			managedRoot := filepath.Join(root, "managed")
			stateRoot := filepath.Join(root, "state")
			if err := os.MkdirAll(managedRoot, 0o755); err != nil {
				t.Fatalf("create managed root: %v", err)
			}
			filePath := filepath.Join(managedRoot, "script.txt")
			if err := os.WriteFile(filePath, []byte(testCase.original), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}
			client, serverURL := authenticatedClient(t, managedRoot, stateRoot)

			response, err := client.Get(serverURL + "/resources/files/edit/script.txt")
			if err != nil {
				t.Fatalf("get editor: %v", err)
			}
			page, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatalf("read editor: %v", err)
			}

			response, err = client.PostForm(serverURL+"/resources/files/edit/script.txt", url.Values{
				"content":    {testCase.submitted},
				"digest":     {hiddenValue(t, page, "digest")},
				"csrf_token": {formToken(t, page)},
			})
			if err != nil {
				t.Fatalf("save text: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("save status = %d, want %d", response.StatusCode, http.StatusSeeOther)
			}

			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("read saved file: %v", err)
			}
			if string(content) != testCase.want {
				t.Fatalf("saved content = %q, want %q", content, testCase.want)
			}
		})
	}
}
