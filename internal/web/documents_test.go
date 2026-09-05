package web_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

func addDocument(t *testing.T, client *http.Client, serverURL, csrfToken, path string) *http.Response {
	t.Helper()
	response, err := client.PostForm(serverURL+"/resources/documents", url.Values{
		"csrf_token": {csrfToken},
		"action":     {"add"},
		"path":       {path},
	})
	if err != nil {
		t.Fatalf("add document: %v", err)
	}
	return response
}

func documentsCSRFToken(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	response, err := client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatalf("get documents: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read documents: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get documents status=%d", response.StatusCode)
	}
	return formToken(t, body)
}

func TestDocumentsAddListSearchAndRemove(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(hostRoot, "deploy.md")
	otherPath := filepath.Join(hostRoot, "notes.txt")
	if err := os.WriteFile(documentPath, []byte("# Deploy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	token := documentsCSRFToken(t, client, serverURL)

	response := addDocument(t, client, serverURL, token, documentPath)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add document status=%d", response.StatusCode)
	}
	// Adding the same path again refreshes the row instead of duplicating it.
	response = addDocument(t, client, serverURL, token, documentPath)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("re-add document status=%d", response.StatusCode)
	}
	response = addDocument(t, client, serverURL, token, otherPath)
	_ = response.Body.Close()

	response, err := client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, expected := range []string{
		`data-grouped-records="document-groups"`,
		`data-document-reorder-url="/resources/documents/reorder"`,
		`data-document-reorder-toggle`,
		`data-document-id="` + hostfiles.ComparisonKey(documentPath) + `"`,
		`action="/resources/documents"`,
		`data-copy-value="` + documentPath + `"`,
		`/config/groups/new?return_to=%2Fresources%2Fdocuments`,
		`href="/resources/files"`,
		`<code class="qr__path" title="` + documentPath + `">`,
		`data-lucide="grip-vertical"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("documents page does not contain %q: %s", expected, body)
		}
	}
	// Markdown documents link to the shared preview and editor; the directory action
	// lands on the parent listing with the row focused.
	if !strings.Contains(body, `/resources/files/view?path=`) || !strings.Contains(body, `/resources/files/edit?path=`) {
		t.Fatalf("documents page lacks preview/edit links: %s", body)
	}
	if !strings.Contains(body, `focus_path=`) {
		t.Fatalf("documents page lacks the focused directory link: %s", body)
	}

	response, err = client.Get(serverURL + "/resources/documents?q=deploy")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(filtered), documentPath) || strings.Contains(string(filtered), otherPath) {
		t.Fatalf("documents search does not filter by name/path: %s", filtered)
	}

	response, err = client.PostForm(serverURL+"/resources/documents", url.Values{
		"csrf_token": {token},
		"action":     {"remove"},
		"path":       {documentPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("remove document status=%d", response.StatusCode)
	}
	response, err = client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(remaining), `data-document-id="`+hostfiles.ComparisonKey(documentPath)+`"`) {
		t.Fatalf("removed document still listed: %s", remaining)
	}
	if !strings.Contains(string(remaining), `data-document-id="`+hostfiles.ComparisonKey(otherPath)+`"`) {
		t.Fatalf("unrelated document missing after removal: %s", remaining)
	}
}

func TestDocumentsRejectInvalidAdditions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	token := documentsCSRFToken(t, client, serverURL)

	response := addDocument(t, client, serverURL, token, hostRoot)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("add directory status=%d, want %d: %s", response.StatusCode, http.StatusBadRequest, body)
	}

	response = addDocument(t, client, serverURL, token, filepath.Join(hostRoot, "missing.md"))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("add missing document status=%d, want %d", response.StatusCode, http.StatusNotFound)
	}

	response, err := client.PostForm(serverURL+"/resources/documents", url.Values{
		"csrf_token": {token},
		"action":     {"unknown"},
		"path":       {filepath.Join(hostRoot, "x.md")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown action status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestDocumentMoveGroupAndTaskPage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(hostRoot, "runbook.md")
	if err := os.WriteFile(documentPath, []byte("# Runbook\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	token := documentsCSRFToken(t, client, serverURL)
	response := addDocument(t, client, serverURL, token, documentPath)
	_ = response.Body.Close()

	response, err := client.Get(serverURL + "/config/groups/new?return_to=%2Fresources%2Fdocuments")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/groups?return_to=%2Fresources%2Fdocuments", url.Values{
		"csrf_token": {formToken(t, task)},
		"name":       {"Ops docs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/resources/documents" {
		t.Fatalf("create group status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`/config/groups/([^/"]+)/edit`).FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("group edit link missing: %s", page)
	}
	groupID := string(match[1])

	response, err = client.Get(serverURL + "/resources/documents/move-group?path=" + url.QueryEscape(documentPath))
	if err != nil {
		t.Fatal(err)
	}
	moveTask, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`data-task-kind="document-move-group"`, `value="` + groupID + `"`, `name="action" value="move-group"`} {
		if !strings.Contains(string(moveTask), expected) {
			t.Fatalf("move-group task page does not contain %q: %s", expected, moveTask)
		}
	}

	response, err = client.PostForm(serverURL+"/resources/documents", url.Values{
		"csrf_token": {token},
		"action":     {"move-group"},
		"path":       {documentPath},
		"group_id":   {groupID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move document to group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	groupStart := strings.Index(body, `data-group-name="Ops docs"`)
	rowIndex := strings.Index(body, `data-document-id="`+hostfiles.ComparisonKey(documentPath)+`"`)
	if groupStart < 0 || rowIndex < 0 || rowIndex < groupStart {
		t.Fatalf("document is not listed under its group: %s", body)
	}

	// 删除共享分组时文档保留并回到“未分组”。
	response, err = client.Get(serverURL + "/config/groups/" + groupID + "/delete?return_to=%2Fresources%2Fdocuments")
	if err != nil {
		t.Fatal(err)
	}
	deleteTask, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/groups/"+groupID+"/delete?return_to=%2Fresources%2Fdocuments", url.Values{
		"csrf_token": {formToken(t, deleteTask)},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete group status=%d", response.StatusCode)
	}
	response, err = client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	page, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `data-document-id="`+hostfiles.ComparisonKey(documentPath)+`"`) || strings.Contains(string(page), `data-group-name="Ops docs"`) {
		t.Fatalf("group deletion did not re-home the document: %s", page)
	}
}

func TestDocumentsReorder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(hostRoot, "alpha.md")
	secondPath := filepath.Join(hostRoot, "beta.md")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("# Doc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	client, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	token := documentsCSRFToken(t, client, serverURL)
	for _, path := range []string{firstPath, secondPath} {
		response := addDocument(t, client, serverURL, token, path)
		_ = response.Body.Close()
	}
	firstKey := hostfiles.ComparisonKey(firstPath)
	secondKey := hostfiles.ComparisonKey(secondPath)

	response, err := client.PostForm(serverURL+"/resources/documents/reorder", url.Values{
		"csrf_token":  {token},
		"document_id": {secondKey, firstKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder documents status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	secondIndex := strings.Index(body, `data-document-id="`+secondKey+`"`)
	firstIndex := strings.Index(body, `data-document-id="`+firstKey+`"`)
	if secondIndex < 0 || firstIndex < 0 || secondIndex > firstIndex {
		t.Fatalf("reordered documents are not listed in the saved order: %s", body)
	}

	// 并发增删后必须提交完整清单，否则拒绝部分覆盖。
	response, err = client.PostForm(serverURL+"/resources/documents/reorder", url.Values{
		"csrf_token":  {token},
		"document_id": {firstKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("partial reorder status=%d, want %d", response.StatusCode, http.StatusConflict)
	}
}

func TestDocumentsRequireReadFilesPermission(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	viewer := createRoleUserClient(t, admin, serverURL, "documents-viewer", "viewer")

	response, err := viewer.Get(serverURL + "/resources/documents")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer documents page status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}

	response, err = viewer.PostForm(serverURL+"/resources/documents", url.Values{
		"csrf_token": {"invalid"},
		"action":     {"add"},
		"path":       {"x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer document mutation status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}
}
