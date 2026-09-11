package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"scriptboard/internal/workflow"
	"testing"
	"time"
)

func TestWorkflowCustomPublishEntryRun(t *testing.T) {
	root := t.TempDir()
	client, base := authenticatedClient(t, filepath.Join(root, "host"), filepath.Join(root, "state"))
	res, err := client.Get(base + "/workflow")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("page %d %s", res.StatusCode, page)
	}
	token := formToken(t, page)
	post := func(path string, v any, dest any, want int) {
		t.Helper()
		raw, _ := json.Marshal(v)
		req, _ := http.NewRequest("POST", base+"/workflow/"+path, bytes.NewReader(raw))
		req.Header.Set("X-CSRF-Token", token)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s: %d %s", path, res.StatusCode, body)
		}
		if dest != nil {
			if err = json.Unmarshal(body, dest); err != nil {
				t.Fatal(err)
			}
		}
	}
	custom := workflow.CustomDefinition{Name: "Identity", Language: "python", Code: "def main(inputs): return inputs", Inputs: []workflow.Field{{Name: "text", Type: workflow.String, Required: true}}, Outputs: []workflow.Field{{Name: "text", Type: workflow.String, Required: true}}}
	post("custom", custom, &custom, 200)
	first := custom
	custom.Code = "def main(inputs): return {}"
	post("custom", custom, &custom, 200)
	post("custom", first, nil, 409)
	// A pure data workflow exercises production queue/engine without assuming an installed interpreter.
	d := workflow.Definition{Name: "Identity workflow", Parameters: []workflow.Field{{Name: "text", Type: workflow.String, Required: true}}, Graph: workflow.Graph{Nodes: []workflow.Node{{ID: "start", Kind: "start", Outputs: []workflow.Field{{Name: "text", Type: workflow.String, Required: true}}}, {ID: "end", Kind: "end", Inputs: []workflow.Field{{Name: "text", Type: workflow.String, Required: true}}}}, Links: []workflow.DataLink{{From: workflow.Port{Node: "start", Field: "text"}, To: workflow.Port{Node: "end", Field: "text"}}}}}
	post("draft", d, &d, 200)
	var published workflow.Definition
	post("publish", map[string]any{"id": d.ID, "revision": d.Revision}, &published, 200)
	entry := workflow.Entry{Name: "Identity entry", WorkflowID: d.ID, WorkflowRevision: published.Revision, Values: map[string]json.RawMessage{"text": json.RawMessage(`"hello"`)}, Locks: []string{"identity"}}
	post("entry", entry, &entry, 200)
	var run workflow.Run
	post("start", map[string]string{"id": entry.ID}, &run, 200)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err = client.Get(base + "/workflow/state")
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Runs []workflow.Run `json:"runs"`
		}
		json.NewDecoder(res.Body).Decode(&state)
		res.Body.Close()
		for _, r := range state.Runs {
			if r.ID == run.ID && r.Status == "succeeded" {
				if string(r.Result["text"]) != `"hello"` {
					t.Fatal(r.Result)
				}
				return
			}
			if r.Status == "failed" {
				t.Fatal(r.Error)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("run did not finish")
}
