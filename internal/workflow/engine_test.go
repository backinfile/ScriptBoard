package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeExecutor struct {
	calls int
	err   error
}

func (f *fakeExecutor) Execute(_ context.Context, _ Run, _ Node, _ map[string]json.RawMessage, started func(string) error) (map[string]json.RawMessage, error) {
	f.calls++
	if err := started("process"); err != nil {
		return nil, err
	}
	return map[string]json.RawMessage{}, f.err
}
func TestEngineFailureAndUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		err          error
	}{{"failure", "failed", errors.New("exit 1")}, {"uncertain", "needs_attention", ErrUncertain}} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRepository(t)
			ctx := context.Background()
			d, err := r.SaveDraft(ctx, Definition{Name: "failure", Graph: Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "n", Kind: "script"}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "n"}, {From: "n", To: "e"}}}}, "admin")
			if err != nil {
				t.Fatal(err)
			}
			d, err = r.Publish(ctx, d.ID, d.Revision, "admin")
			if err != nil {
				t.Fatal(err)
			}
			entry, err := r.SaveEntry(ctx, Entry{Name: "entry", WorkflowID: d.ID, WorkflowRevision: d.Revision, Locks: []string{"workspace"}}, "admin")
			if err != nil {
				t.Fatal(err)
			}
			run, err := r.Enqueue(ctx, entry.ID, "admin")
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := r.Claim(ctx, run.ID); err != nil || !ok {
				t.Fatal(ok, err)
			}
			executor := &fakeExecutor{err: tc.err}
			engine := Engine{Repository: r, Executor: executor}
			engine.execute(ctx, run)
			got, err := r.Run(ctx, run.ID)
			if err != nil || got.Status != tc.status || executor.calls != 1 {
				t.Fatal(got, err, executor.calls)
			}
			for _, step := range got.Steps {
				if step.NodeID == "e" && step.Status != "skipped" {
					t.Fatal(step)
				}
			}
			var count int
			r.DB.QueryRow("SELECT count(*) FROM workflow_resource_locks").Scan(&count)
			if (tc.status == "needs_attention") != (count == 1) {
				t.Fatalf("resource ownership %s %d", tc.status, count)
			}
		})
	}
}
func TestLookupSnapshotAndSecrecy(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	if _, err := r.DB.Exec("CREATE TABLE variables(name TEXT,value TEXT,value_type TEXT,is_password INTEGER); INSERT INTO variables VALUES('release','1.2.3','version',0),('password','secret','text',1)"); err != nil {
		t.Fatal(err)
	}
	tx, _ := r.DB.BeginTx(ctx, nil)
	snapshot := Snapshot{Entry: Entry{Values: map[string]json.RawMessage{"nested": json.RawMessage("{\"version\":\"2.0\"}")}}, Workflow: Definition{Graph: Graph{Nodes: []Node{{Kind: "lookup", Config: map[string]json.RawMessage{"path": json.RawMessage("\"variables.release\"")}}}}}}
	if err := captureLookups(ctx, tx, &snapshot); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	r.DB.Exec("UPDATE variables SET value='9.9.9' WHERE name='release'")
	raw, err := lookupValue(snapshot, "variables.release")
	if err != nil || string(raw) != "\"1.2.3\"" {
		t.Fatal(string(raw), err)
	}
	raw, err = lookupValue(snapshot, "entry.nested.version")
	if err != nil || string(raw) != "\"2.0\"" {
		t.Fatal(string(raw), err)
	}
	if _, err = lookupValue(snapshot, "entry.nested.missing"); err == nil {
		t.Fatal("missing path accepted")
	}
	tx, _ = r.DB.BeginTx(ctx, nil)
	defer tx.Rollback()
	snapshot.Workflow.Graph.Nodes[0].Config["path"] = json.RawMessage("\"variables.password\"")
	if err = captureLookups(ctx, tx, &snapshot); err == nil {
		t.Fatal("secret variable accepted")
	}
}
func TestPublishedNodeValidation(t *testing.T) {
	for _, kind := range []string{"unknown", "literal", "lookup"} {
		r := testRepository(t)
		ctx := context.Background()
		d, err := r.SaveDraft(ctx, Definition{Name: "invalid", Graph: Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "n", Kind: kind}, {ID: "e", Kind: "end"}}}}, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Publish(ctx, d.ID, d.Revision, "admin"); !errors.Is(err, ErrInvalid) {
			t.Fatal(kind, err)
		}
	}
}

func TestConditionFalseCompletesWithSkippedSuccessors(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	d, err := r.SaveDraft(ctx, Definition{Name: "condition", Graph: Graph{Nodes: []Node{
		{ID: "s", Kind: "start"},
		{ID: "n", Kind: "condition", Inputs: []Field{{Name: "condition", Type: Boolean, Required: true}}, Values: map[string]json.RawMessage{"condition": json.RawMessage("false")}},
		{ID: "e", Kind: "end"},
	}, Dependencies: []Dependency{{From: "s", To: "n"}, {From: "n", To: "e"}}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.Publish(ctx, d.ID, d.Revision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := r.SaveEntry(ctx, Entry{Name: "condition", WorkflowID: d.ID, WorkflowRevision: d.Revision}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Enqueue(ctx, entry.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Claim(ctx, run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	executor := &fakeExecutor{}
	engine := Engine{Repository: r, Executor: executor}
	engine.execute(ctx, run)
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.Status != "succeeded" || executor.calls != 0 {
		t.Fatal(got, err)
	}
	for _, step := range got.Steps {
		if step.NodeID == "e" && step.Status != "skipped" {
			t.Fatal(step)
		}
	}
}
func TestEnumRejectsLinkedAndLocalInvalidValues(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "s", Outputs: []Field{{Name: "value", Type: String}}}, {ID: "e", Inputs: []Field{{Name: "environment", Type: String, Enum: []string{"qa", "release"}, Required: true}}}}, Links: []DataLink{{From: Port{"s", "value"}, To: Port{"e", "environment"}}}}
	if _, err := ResolveInputs(g, "e", map[string]map[string]json.RawMessage{"s": {"value": json.RawMessage("\"other\"")}}); err == nil {
		t.Fatal("invalid linked enum accepted")
	}
	g.Links = nil
	g.Nodes[1].Values = map[string]json.RawMessage{"environment": json.RawMessage("\"other\"")}
	if _, err := ResolveInputs(g, "e", nil); err == nil {
		t.Fatal("invalid local enum accepted")
	}
}
