package app_test

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func createScheduleGroupForTest(t *testing.T, client *http.Client, serverURL, name string) (string, string) {
	t.Helper()
	response, err := client.Get(serverURL + "/config/schedules/groups/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(taskPage), `data-task-kind="schedule-group-new"`) {
		t.Fatalf("new Schedule group task status=%d body=%s", response.StatusCode, taskPage)
	}
	response, err = client.PostForm(serverURL+"/config/schedules/groups", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {name},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/config/schedules" {
		t.Fatalf("create Schedule group status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	pattern := regexp.MustCompile(`data-schedule-group="([^"]+)" data-group-name="` + regexp.QuoteMeta(name) + `"`)
	match := pattern.FindSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("created Schedule group %q not found: %s", name, page)
	}
	return string(match[1]), formToken(t, page)
}

func TestAdminCanCreateRenameAndReorderScheduleGroups(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	firstID, _ := createScheduleGroupForTest(t, client, serverURL, "First")
	secondID, token := createScheduleGroupForTest(t, client, serverURL, "Second")

	response, err := client.PostForm(serverURL+"/config/schedules/groups/"+secondID+"/move", url.Values{
		"csrf_token": {token},
		"direction":  {"up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("move Schedule group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/schedules/groups/" + firstID + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`data-task-kind="schedule-group-edit"`, `name="name"`, `value="First"`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("edit Schedule group task missing %q: %s", expected, taskPage)
		}
	}
	response, err = client.PostForm(serverURL+"/config/schedules/groups/"+firstID+"/update", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {"Operations"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename Schedule group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	html := string(page)
	for _, expected := range []string{
		`href="/config/schedules/groups/new"`,
		`action="/config/schedules/groups/` + secondID + `/move"`,
		`action="/config/schedules/groups/` + firstID + `/delete"`,
		`data-group-name="Operations"`,
		`There are no schedules in this group.`,
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("Schedule group page missing %q: %s", expected, html)
		}
	}
	if first, second := strings.Index(html, `data-group-name="Operations"`), strings.Index(html, `data-group-name="Second"`); first < 0 || second < 0 || second > first {
		t.Fatalf("Schedule groups were not reordered: %s", html)
	}
}

func TestDeletingScheduleGroupMovesItsSchedulesToUngrouped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	groupID, _ := createScheduleGroupForTest(t, client, serverURL, "Temporary")

	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	for _, expected := range []string{`name="group_id"`, `value="` + groupID + `"`, `Temporary`} {
		if !strings.Contains(string(taskPage), expected) {
			t.Fatalf("Schedule form missing managed group option %q: %s", expected, taskPage)
		}
	}
	if strings.Contains(string(taskPage), `name="group_name"`) {
		t.Fatalf("Schedule form still accepts a free-form group name: %s", taskPage)
	}
	response, err = client.PostForm(serverURL+"/config/schedules", url.Values{
		"csrf_token":      {formToken(t, taskPage)},
		"name":            {"Preserved schedule"},
		"group_id":        {groupID},
		"script":          {"checks/preserved.ps1"},
		"expression":      {"0 2 * * *"},
		"timeout_seconds": {"90"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("create grouped Schedule status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/schedules/groups/"+groupID+"/delete", url.Values{
		"csrf_token": {formToken(t, page)},
		"confirm":    {"yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete Schedule group status=%d", response.StatusCode)
	}

	response, err = client.Get(serverURL + "/config/schedules")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	html := string(page)
	if strings.Contains(html, `data-group-name="Temporary"`) {
		t.Fatalf("deleted Schedule group remains: %s", html)
	}
	ungroupedPattern := regexp.MustCompile(`data-schedule-group="ungrouped"[\s\S]*?Preserved schedule`)
	if !ungroupedPattern.Match(page) {
		t.Fatalf("Schedule was not preserved under Ungrouped: %s", page)
	}
}

func TestScheduleRejectsUnknownManagedGroup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client, serverURL := authenticatedClient(t, filepath.Join(root, "managed"), filepath.Join(root, "state"))
	response, err := client.Get(serverURL + "/config/schedules/new")
	if err != nil {
		t.Fatal(err)
	}
	taskPage, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	response, err = client.PostForm(serverURL+"/config/schedules", url.Values{
		"csrf_token": {formToken(t, taskPage)},
		"name":       {"Invalid group"},
		"group_id":   {"missing-group"},
		"script":     {"checks/invalid.ps1"},
		"expression": {"0 2 * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "计划分组不存在") {
		t.Fatalf("unknown Schedule group status=%d body=%s", response.StatusCode, body)
	}
}
