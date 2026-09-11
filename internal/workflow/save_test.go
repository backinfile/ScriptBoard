package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSaveAtomicVersionAndEntryConfirmation(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	d := Definition{Name: "single save", Confirm: true, Graph: Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "e"}}}}
	d, err := r.Save(ctx, d, "admin")
	if err != nil {
		t.Fatal(err)
	}
	var version Definition
	if err = r.Version(ctx, "published", d.ID, 1, &version); err != nil {
		t.Fatal(err)
	}
	legacy, err := r.SaveEntry(ctx, Entry{Name: "legacy", WorkflowID: d.ID, WorkflowRevision: 1}, "admin")
	if err != nil || legacy.Confirm == nil || !*legacy.Confirm {
		t.Fatal(legacy, err)
	}
	no := false
	e, err := r.SaveEntry(ctx, Entry{Name: "no confirm", WorkflowID: d.ID, WorkflowRevision: 1, Confirm: &no}, "admin")
	if err != nil || e.Confirm == nil || *e.Confirm {
		t.Fatal(e, err)
	}
	raw, _ := json.Marshal(e)
	var restored Entry
	json.Unmarshal(raw, &restored)
	if restored.Confirm == nil || *restored.Confirm {
		t.Fatal("explicit false lost")
	}
	bad := d
	bad.Graph.Nodes = []Node{{ID: "s", Kind: "start"}}
	if _, err = r.Save(ctx, bad, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var stored Definition
	var body string
	err = r.DB.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='draft' AND id=?", d.ID).Scan(&body)
	if err == nil {
		err = json.Unmarshal([]byte(body), &stored)
	}
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != d.Revision || len(stored.Graph.Nodes) != 2 {
		t.Fatal("invalid save persisted", stored)
	}
	old := d
	d.Name = "saved twice"
	d, err = r.Save(ctx, d, "admin")
	if err != nil || d.Revision != 2 {
		t.Fatal(d, err)
	}
	if _, err = r.Save(ctx, old, "other"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale save accepted", err)
	}
	if err = r.Version(ctx, "published", d.ID, 1, &version); err != nil || version.Name != "single save" {
		t.Fatal("old snapshot changed", version, err)
	}
	var count int
	r.DB.QueryRow("SELECT COUNT(*) FROM workflow_versions WHERE kind='published' AND id=?", d.ID).Scan(&count)
	if count != 2 {
		t.Fatal("failed save added version", count)
	}
}
