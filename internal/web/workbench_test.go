package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"scriptboard/internal/workbench"
	"strings"
	"testing"
)

func TestWorkbenchAuthenticationIsolationConflictAndNativeForms(t *testing.T) {
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
	var private workbench.State
	json.NewDecoder(r.Body).Decode(&private)
	r.Body.Close()
	if len(private.Boards) != 0 {
		t.Fatal("private board leaked")
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
	r, e = client.PostForm(base+"/resources/workbench/action", url.Values{"csrf_token": {csrf}, "revision": {"1"}, "board": {"b1"}, "item": {"t1"}, "index": {"0"}, "action": {"edit-task"}, "text": {"Changed"}, "done": {"on"}})
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
