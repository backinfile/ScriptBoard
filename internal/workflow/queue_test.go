package workflow

import (
	"context"
	"testing"
)

func queuedEntry(t *testing.T, r Repository) Entry {
	t.Helper()
	ctx := context.Background()
	d, err := r.SaveDraft(ctx, Definition{Name: "Queue", Graph: Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "e"}}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.Publish(ctx, d.ID, d.Revision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.SaveEntry(ctx, Entry{Name: "Entry", WorkflowID: d.ID, WorkflowRevision: d.Revision, Locks: []string{"workspace", "cluster"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestQueueAtomicLocksRecovery(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	e := queuedEntry(t, r)
	a, err := r.Enqueue(ctx, e.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Enqueue(ctx, e.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Claim(ctx, b.ID); err != nil || ok {
		t.Fatal("overtook prior queued owner", ok, err)
	}
	if ok, err := r.Claim(ctx, a.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := r.Claim(ctx, b.ID); err != nil || ok {
		t.Fatal("overlap allowed", ok, err)
	}
	if err = r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := r.Run(ctx, a.ID)
	if err != nil || recovered.Status != "needs_attention" {
		t.Fatal(recovered, err)
	}
	if ok, err := r.Claim(ctx, b.ID); err != nil || ok {
		t.Fatal("recovery released locks", ok, err)
	}
	if err = r.ResolveAttention(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Claim(ctx, b.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = r.BeginStep(ctx, b.ID, "s"); err != nil {
		t.Fatal(err)
	}
	if err = r.BeginStep(ctx, b.ID, "s"); err == nil {
		t.Fatal("repeated dispatch allowed")
	}
	if err = r.RequestCancel(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = r.BeginStep(ctx, b.ID, "e"); err == nil {
		t.Fatal("dispatch during cancellation")
	}
}
func TestQueuedCancelPreservesSnapshot(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	e := queuedEntry(t, r)
	run, err := r.Enqueue(ctx, e.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	e.Name = "Changed"
	if _, err = r.SaveEntry(ctx, e, "admin"); err != nil {
		t.Fatal(err)
	}
	if err = r.RequestCancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := r.Run(ctx, run.ID)
	if err != nil || saved.Status != "cancelled" || saved.Snapshot.Entry.Name != "Entry" {
		t.Fatal(saved, err)
	}
	if ok, err := r.Claim(ctx, run.ID); err != nil || ok {
		t.Fatal("cancelled run claimed", ok, err)
	}
}
