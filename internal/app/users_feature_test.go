package app_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAdministratorCreatesMaintainerWhoCanSignIn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))

	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatalf("get users: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("users status = %d, body=%s", response.StatusCode, page)
	}

	response, err = admin.PostForm(serverURL+"/settings/users", url.Values{
		"username":   {"maintainer-one"},
		"role":       {"maintainer"},
		"csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create maintainer: %v", err)
	}
	created, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read created maintainer: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.StatusCode, created)
	}
	match := regexp.MustCompile(`data-generated-password>([^<]+)<`).FindSubmatch(created)
	if len(match) != 2 {
		t.Fatalf("generated password missing from one-time response: %s", created)
	}
	userMatch := regexp.MustCompile(`data-user-id="([^"]+)" data-username="maintainer-one"`).FindSubmatch(created)
	if len(userMatch) != 2 {
		t.Fatalf("created user row missing: %s", created)
	}
	if row := regexp.MustCompile(`(?s)<tr data-user-id="[^"]+" data-username="maintainer-one">(.*?)</tr>`).FindSubmatch(created); len(row) != 2 ||
		strings.Contains(string(row[1]), `<input`) || strings.Contains(string(row[1]), `<select`) {
		t.Fatalf("user row exposes inline editing controls: %s", created)
	}

	response, err = admin.Get(serverURL + "/settings/users/" + url.PathEscape(string(userMatch[1])) + "/edit")
	if err != nil {
		t.Fatalf("get user edit task: %v", err)
	}
	editPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read user edit task: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("user edit task status=%d, body=%s", response.StatusCode, editPage)
	}
	for _, expected := range []string{
		`data-task-kind="user-edit"`,
		`name="username" value="maintainer-one"`,
		`<option value="maintainer" selected>`,
		`action="/settings/users/` + string(userMatch[1]) + `/reset-password"`,
		`action="/settings/users/` + string(userMatch[1]) + `/disable"`,
	} {
		if !strings.Contains(string(editPage), expected) {
			t.Fatalf("user edit task does not contain %q: %s", expected, editPage)
		}
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	maintainer := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response = loginAs(t, maintainer, serverURL, "maintainer-one", strings.TrimSpace(string(match[1])), http.StatusSeeOther)
	if location := response.Header.Get("Location"); location != "/monitor" {
		t.Fatalf("login redirect = %q, want /monitor", location)
	}
	response, err = maintainer.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("maintainer user-management status=%d, want 403", response.StatusCode)
	}
}

func TestSystemAdministratorCannotBeDisabledOrDemoted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	match := regexp.MustCompile(`data-user-id="([^"]+)" data-username="admin"`).FindSubmatch(usersPage)
	if len(match) != 2 {
		t.Fatalf("system administrator row missing: %s", usersPage)
	}
	adminID := url.PathEscape(string(match[1]))
	for _, operation := range []struct {
		path   string
		values url.Values
	}{
		{"/disable", url.Values{}},
		{"/update", url.Values{"username": {"demoted"}, "role": {"viewer"}}},
	} {
		operation.values.Set("csrf_token", formToken(t, usersPage))
		response, err = admin.PostForm(serverURL+"/settings/users/"+adminID+operation.path, operation.values)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("administrator %s status=%d, want 403", operation.path, response.StatusCode)
		}
	}
}

func TestViewerCanObserveOperationsButCannotOpenRestrictedAreas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	viewer := createRoleUserClient(t, admin, serverURL, "viewer-one", "viewer")

	for _, path := range []string{"/monitor", "/monitor/applications", "/monitor/websites", "/config/quick-runs", "/config/schedules", "/history/runs"} {
		response, err := viewer.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get allowed %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("allowed %s status = %d, want 200", path, response.StatusCode)
		}
	}
	for _, path := range []string{"/resources/files", hostFileHref("/resources/files/download", filepath.Join(root, "missing.txt")), "/resources/variables", "/history/audit", "/settings/updates", "/settings/users"} {
		response, err := viewer.Get(serverURL + path)
		if err != nil {
			t.Fatalf("get forbidden %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("forbidden %s status = %d, want 403", path, response.StatusCode)
		}
	}
}

func TestOperatorReadsAndExecutesFilesButCannotModifyThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName := "operator.sh"
	scriptBody := "printf 'operator-run\\n'\n"
	if runtime.GOOS == "windows" {
		scriptName = "operator.cmd"
		scriptBody = "@echo off\r\necho operator-run\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	admin, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	operator := createRoleUserClient(t, admin, serverURL, "operator-one", "operator")

	response, err := operator.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	filesPage, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(filesPage), scriptName) {
		t.Fatalf("files response status=%d body=%s", response.StatusCode, filesPage)
	}

	response, err = operator.PostForm(serverURL+"/resources/files/mkdir", url.Values{
		"path": {hostRoot}, "name": {"forbidden"}, "csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("mkdir status = %d, want 403", response.StatusCode)
	}
	for _, forbiddenPath := range []string{"/config/quick-runs/one-time", "/config/schedules/missing/run"} {
		response, err = operator.PostForm(serverURL+forbiddenPath, url.Values{
			"csrf_token": {formToken(t, filesPage)},
		})
		if err != nil {
			t.Fatalf("post forbidden %s: %v", forbiddenPath, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("post forbidden %s status = %d, want 403", forbiddenPath, response.StatusCode)
		}
	}

	scriptPath := filepath.Join(hostRoot, scriptName)
	response, err = operator.Get(hostFileRequestURL(serverURL, "/resources/files/download", scriptPath))
	if err != nil {
		t.Fatalf("download script: %v", err)
	}
	downloaded, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(downloaded), "operator-run") {
		t.Fatalf("download status=%d body=%q", response.StatusCode, downloaded)
	}

	response, err = operator.PostForm(serverURL+"/history/runs/start", url.Values{
		"script": {scriptPath}, "csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/history/runs/") {
		t.Fatalf("start status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestOperatorCannotStopAnotherUsersRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName := "owned.sh"
	scriptBody := "sleep 5\n"
	if runtime.GOOS == "windows" {
		scriptName = "owned.cmd"
		scriptBody = "@echo off\r\nping 127.0.0.1 -n 6 >nul\r\n"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	admin, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	operator := createRoleUserClient(t, admin, serverURL, "operator-stop", "operator")

	response, err := admin.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	filesPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/history/runs/start", url.Values{
		"script": {filepath.Join(hostRoot, scriptName)}, "csrf_token": {formToken(t, filesPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	runPath := response.Header.Get("Location")

	var operatorRunPage []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err = operator.Get(serverURL + runPath)
		if err != nil {
			t.Fatal(err)
		}
		operatorRunPage, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if strings.Contains(string(operatorRunPage), "running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not start: %s", operatorRunPage)
		}
		time.Sleep(25 * time.Millisecond)
	}
	response, err = operator.PostForm(serverURL+runPath+"/stop", url.Values{
		"csrf_token": {formToken(t, operatorRunPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("operator stopped another user's run: status=%d", response.StatusCode)
	}

	response, err = admin.Get(serverURL + runPath)
	if err != nil {
		t.Fatal(err)
	}
	adminRunPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+runPath+"/stop", url.Values{
		"csrf_token": {formToken(t, adminRunPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	deadline = time.Now().Add(10 * time.Second)
	for {
		response, err = admin.Get(serverURL + runPath)
		if err != nil {
			t.Fatal(err)
		}
		stoppedPage, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if !strings.Contains(string(stoppedPage), `data-run-status data-state="running"`) &&
			!strings.Contains(string(stoppedPage), `data-run-status data-state="stopping"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("administrator run did not stop: %s", stoppedPage)
		}
		time.Sleep(25 * time.Millisecond)
	}

	response, err = operator.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	operatorFiles, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = operator.PostForm(serverURL+"/history/runs/start", url.Values{
		"script": {filepath.Join(hostRoot, scriptName)}, "csrf_token": {formToken(t, operatorFiles)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	ownRunPath := response.Header.Get("Location")
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(ownRunPath, "/history/runs/") {
		t.Fatalf("operator own run start status=%d location=%q", response.StatusCode, ownRunPath)
	}
	response, err = operator.Get(serverURL + ownRunPath)
	if err != nil {
		t.Fatal(err)
	}
	ownRunPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = operator.PostForm(serverURL+ownRunPath+"/stop", url.Values{
		"csrf_token": {formToken(t, ownRunPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("operator own stop status=%d, want 303", response.StatusCode)
	}
}

func TestAdministratorDisablesUserAndRevokesTheirSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	maintainer, password, userID := createRoleUserCredential(t, admin, serverURL, "disabled-user", "maintainer")

	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/settings/users/"+url.PathEscape(userID)+"/disable", url.Values{
		"csrf_token": {formToken(t, usersPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable status = %d", response.StatusCode)
	}

	response, err = maintainer.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("disabled session status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	response, err = admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/settings/users/"+url.PathEscape(userID)+"/enable", url.Values{
		"csrf_token": {formToken(t, usersPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("enable status = %d", response.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	relogin := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	loginAs(t, relogin, serverURL, "disabled-user", password, http.StatusSeeOther)
}

func TestAdministratorRenamesUserAndChangesRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	viewer, password, userID := createRoleUserCredential(t, admin, serverURL, "role-user", "viewer")

	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/settings/users/"+url.PathEscape(userID)+"/update", url.Values{
		"username": {"renamed-operator"}, "role": {"operator"}, "csrf_token": {formToken(t, usersPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status = %d", response.StatusCode)
	}
	response, err = admin.Get(serverURL + "/history/audit.csv")
	if err != nil {
		t.Fatal(err)
	}
	auditCSV, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, action := range []string{"rename_user", "change_user_role"} {
		if !strings.Contains(string(auditCSV), action) {
			t.Errorf("user update audit is missing %q:\n%s", action, auditCSV)
		}
	}

	response, err = viewer.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("changed user session status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	jar, _ := cookiejar.New(nil)
	relogin := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	loginAs(t, relogin, serverURL, "renamed-operator", password, http.StatusSeeOther)
	response, err = relogin.Get(serverURL + "/resources/files")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("operator files status = %d", response.StatusCode)
	}
}

func TestAdministratorResetsUserPasswordAndShowsReplacementOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	userClient, oldPassword, userID := createRoleUserCredential(t, admin, serverURL, "reset-user", "viewer")

	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/settings/users/"+url.PathEscape(userID)+"/reset-password", url.Values{
		"csrf_token": {formToken(t, usersPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resetPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("reset status=%d body=%s", response.StatusCode, resetPage)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reset password response Cache-Control=%q, want no-store", response.Header.Get("Cache-Control"))
	}
	match := regexp.MustCompile(`data-generated-password>([^<]+)<`).FindSubmatch(resetPage)
	if len(match) != 2 {
		t.Fatalf("replacement password missing: %s", resetPage)
	}
	response, err = admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	nextUsersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(nextUsersPage), strings.TrimSpace(string(match[1]))) {
		t.Fatal("replacement password was shown after the successful reset response")
	}

	response, err = userClient.Get(serverURL + "/monitor")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("old session status=%d", response.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	relogin := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	loginAs(t, relogin, serverURL, "reset-user", oldPassword, http.StatusUnauthorized)
	loginAs(t, relogin, serverURL, "reset-user", strings.TrimSpace(string(match[1])), http.StatusSeeOther)
}

func TestRestrictedRolePagesHideUnavailableActions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "managed")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptName := "visible.cmd"
	if runtime.GOOS != "windows" {
		scriptName = "visible.sh"
	}
	if err := os.WriteFile(filepath.Join(hostRoot, scriptName), []byte("echo visible\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	admin, serverURL := authenticatedClient(t, hostRoot, filepath.Join(root, "state"))
	operator := createRoleUserClient(t, admin, serverURL, "operator-ui", "operator")
	viewer := createRoleUserClient(t, admin, serverURL, "viewer-ui", "viewer")

	response, err := operator.Get(hostFilesRequestURL(serverURL, hostRoot))
	if err != nil {
		t.Fatal(err)
	}
	operatorFiles, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, forbidden := range []string{"/resources/files/upload", "/resources/files/delete", "/resources/files/edit", "/resources/files/move", "/resources/files/quick-run"} {
		if strings.Contains(string(operatorFiles), forbidden) {
			t.Errorf("operator file page exposed %q", forbidden)
		}
	}
	if !strings.Contains(string(operatorFiles), "/resources/files/run?") {
		t.Fatal("operator file page did not expose script execution")
	}

	response, err = viewer.Get(serverURL + "/config/quick-runs")
	if err != nil {
		t.Fatal(err)
	}
	viewerQuickRuns, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, forbidden := range []string{"/config/quick-runs/one-time/new", "/config/quick-runs/from-source/new", "/resources/files", "/history/audit", "/resources/variables"} {
		if strings.Contains(string(viewerQuickRuns), forbidden) {
			t.Errorf("viewer page exposed %q", forbidden)
		}
	}
}

func TestAuditRecordsStableActorSnapshotsWithoutGeneratedPasswords(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	_, generatedPassword, userID := createRoleUserCredential(t, admin, serverURL, "audited-user", "viewer")

	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatal(err)
	}
	usersPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = admin.PostForm(serverURL+"/settings/users/"+url.PathEscape(userID)+"/reset-password", url.Values{
		"csrf_token": {formToken(t, usersPage)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resetPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	replacement := regexp.MustCompile(`data-generated-password>([^<]+)<`).FindSubmatch(resetPage)
	if len(replacement) != 2 {
		t.Fatalf("replacement password missing: %s", resetPage)
	}

	response, err = admin.Get(serverURL + "/history/audit.csv")
	if err != nil {
		t.Fatal(err)
	}
	auditCSV, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	auditText := string(auditCSV)
	for _, expected := range []string{"actor_user_id", "actor_username", "actor_role", "create_user", "reset_user_password", "audited-user", "administrator"} {
		if !strings.Contains(auditText, expected) {
			t.Errorf("audit CSV missing %q:\n%s", expected, auditText)
		}
	}
	for _, secret := range []string{generatedPassword, strings.TrimSpace(string(replacement[1])), "session"} {
		if secret != "" && strings.Contains(auditText, secret) {
			t.Errorf("audit CSV leaked secret %q", secret)
		}
	}
}

func TestOrdinaryUserChangesOnlyTheirOwnPassword(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	admin, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	viewer, oldPassword, _ := createRoleUserCredential(t, admin, serverURL, "self-service-user", "viewer")

	response, err := viewer.Get(serverURL + "/settings/account")
	if err != nil {
		t.Fatal(err)
	}
	accountPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if strings.Contains(string(accountPage), `autocomplete="username"`) {
		t.Fatal("ordinary account page exposed username editing")
	}
	if strings.Contains(string(accountPage), `href="/settings/users"`) {
		t.Fatal("ordinary account page exposed user management navigation")
	}
	response, err = viewer.Get(serverURL + "/settings/account/username")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("ordinary username task status=%d, want 403", response.StatusCode)
	}
	response, err = viewer.Get(serverURL + "/settings/account/password")
	if err != nil {
		t.Fatal(err)
	}
	passwordPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(passwordPage), `data-task-kind="account-password"`) {
		t.Fatalf("ordinary password task status=%d body=%s", response.StatusCode, passwordPage)
	}
	newPassword := "replacement-password-2026"
	response, err = viewer.PostForm(serverURL+"/settings/account/password", url.Values{
		"csrf_token":       {formToken(t, passwordPage)},
		"username":         {"forged-rename"},
		"current_password": {oldPassword},
		"new_password":     {newPassword},
		"confirm_password": {newPassword},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("change password status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	jar, _ := cookiejar.New(nil)
	relogin := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	loginAs(t, relogin, serverURL, "forged-rename", newPassword, http.StatusUnauthorized)
	loginAs(t, relogin, serverURL, "self-service-user", newPassword, http.StatusSeeOther)
}

func createRoleUserClient(t *testing.T, admin *http.Client, serverURL, username, role string) *http.Client {
	t.Helper()
	client, _, _ := createRoleUserCredential(t, admin, serverURL, username, role)
	return client
}

func createRoleUserCredential(t *testing.T, admin *http.Client, serverURL, username, role string) (*http.Client, string, string) {
	t.Helper()
	response, err := admin.Get(serverURL + "/settings/users")
	if err != nil {
		t.Fatalf("get users: %v", err)
	}
	page, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	response, err = admin.PostForm(serverURL+"/settings/users", url.Values{
		"username": {username}, "role": {role}, "csrf_token": {formToken(t, page)},
	})
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	created, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read created %s: %v", role, err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create %s status = %d, body=%s", role, response.StatusCode, created)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create %s Cache-Control=%q, want no-store", role, response.Header.Get("Cache-Control"))
	}
	match := regexp.MustCompile(`data-generated-password>([^<]+)<`).FindSubmatch(created)
	if len(match) != 2 {
		t.Fatalf("generated password missing: %s", created)
	}
	idMatch := regexp.MustCompile(`data-user-id="([^"]+)" data-username="` + regexp.QuoteMeta(username) + `"`).FindSubmatch(created)
	if len(idMatch) != 2 {
		t.Fatalf("created user id missing: %s", created)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	password := strings.TrimSpace(string(match[1]))
	loginAs(t, client, serverURL, username, password, http.StatusSeeOther)
	return client, password, string(idMatch[1])
}

func loginAs(t *testing.T, client *http.Client, serverURL, username, password string, wantStatus int) *http.Response {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read login: %v", err)
	}
	response, err = client.PostForm(serverURL+"/login", url.Values{
		"username":   {username},
		"password":   {password},
		"csrf_token": {formToken(t, body)},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	if response.StatusCode != wantStatus {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("login status = %d, want %d, body=%s", response.StatusCode, wantStatus, body)
	}
	_ = response.Body.Close()
	return response
}
