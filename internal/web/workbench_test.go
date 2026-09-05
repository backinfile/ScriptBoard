package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"scriptboard/internal/workbench"
	"strings"
	"testing"
	"time"
)

func TestWorkbenchAuthenticationSharingConflictAndNativeForms(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	res, e := client.Get(base + "/resources/workbench")
	if e != nil {
		t.Fatal(e)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(page, []byte("data-workbench")) {
		t.Fatalf("page %d", res.StatusCode)
	}
	csrf := formToken(t, page)
	state := workbench.State{Boards: []workbench.Board{{ID: "b1", Name: "My board", Items: []workbench.Item{{ID: "n1", Type: "note", Text: "<script>alert(1)</script>"}, {ID: "t1", Type: "todo", Tasks: []workbench.Task{{Text: "Next"}}}}}}}
	send := func(token string, s workbench.State) int {
		t.Helper()
		data, _ := json.Marshal(s)
		req, _ := http.NewRequest("POST", base+"/resources/workbench/state", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if code := send("", state); code != 403 {
		t.Fatalf("csrf %d", code)
	}
	if code := send(csrf, state); code != 200 {
		t.Fatalf("save %d", code)
	}
	if code := send(csrf, state); code != 409 {
		t.Fatalf("conflict %d", code)
	}
	viewer := createRoleUserClient(t, client, base, "workbench-viewer", "viewer")
	r, e := viewer.Get(base + "/resources/workbench/state")
	if e != nil {
		t.Fatal(e)
	}
	var shared workbench.State
	json.NewDecoder(r.Body).Decode(&shared)
	r.Body.Close()
	if len(shared.Boards) != 1 || shared.Revision != 1 || shared.Boards[0].ID != "b1" {
		t.Fatal("shared board unavailable to viewer")
	}
	r, e = client.Get(base + "/resources/workbench")
	if e != nil {
		t.Fatal(e)
	}
	page, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if strings.Contains(string(page), "<script>alert(1)</script>") {
		t.Fatal("stored script rendered unescaped")
	}
	r, e = viewer.Get(base + "/resources/workbench")
	if e != nil {
		t.Fatal(e)
	}
	viewerPage, _ := io.ReadAll(r.Body)
	r.Body.Close()
	viewerCSRF := formToken(t, viewerPage)
	r, e = viewer.PostForm(base+"/resources/workbench/action", url.Values{"csrf_token": {viewerCSRF}, "revision": {"1"}, "board": {"b1"}, "item": {"t1"}, "index": {"0"}, "action": {"edit-task"}, "text": {"Changed"}, "done": {"on"}})
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	r, e = client.Get(base + "/resources/workbench/state")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	json.NewDecoder(r.Body).Decode(&state)
	if state.Revision != 2 || state.Boards[0].Items[1].Tasks[0].Text != "Changed" || !state.Boards[0].Items[1].Tasks[0].Done {
		t.Fatal(state)
	}
	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e = anonymous.Get(base + "/resources/workbench/state")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode == 200 {
		t.Fatal("anonymous state access")
	}
}

func TestWorkbenchPatchPublishesSSE(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	res, err := client.Get(base + "/resources/workbench")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	token := formToken(t, page)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/resources/workbench/events", nil)
	events, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	if events.StatusCode != 200 || events.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(events.Status)
	}
	reader := bufio.NewReader(events.Body)
	readEvent := func() {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil || line != "event: update\n" {
			t.Fatal(line, err)
		}
		for {
			line, err = reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line == "\n" {
				break
			}
		}
	}
	readEvent()
	payload := workbench.Patch{Base: workbench.State{Boards: []workbench.Board{}}, Next: workbench.State{Boards: []workbench.Board{{ID: "sse", Name: "Shared", Items: []workbench.Item{{ID: "n", Type: "note", Text: "Saved"}}}}}}
	data, _ := json.Marshal(payload)
	req, _ = http.NewRequest("PATCH", base+"/resources/workbench/state", bytes.NewReader(data))
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatal(res.Status, string(body))
	}
	readEvent()
	var result workbench.PatchResult
	if err = json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.State.Boards[0].Items[0].Revision != 1 {
		t.Fatal(result)
	}
}
