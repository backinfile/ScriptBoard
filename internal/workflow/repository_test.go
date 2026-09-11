package workflow

import (
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func testRepository(t *testing.T) Repository {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, s := range SchemaStatements {
		if _, err = db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return Repository{db}
}
func TestCustomVersionIsolation(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	d, err := r.SaveCustom(ctx, CustomDefinition{Name: "Version", Language: "python", Code: "def main(inputs): return inputs", Inputs: []Field{{Name: "version", Type: String}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	first := d
	d.Code = "def main(inputs): return {}"
	d, err = r.SaveCustom(ctx, d, "admin")
	if err != nil || d.Revision != 2 {
		t.Fatal(d, err)
	}
	if _, err = r.SaveCustom(ctx, first, "other"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	var old CustomDefinition
	if err = r.Version(ctx, "custom", d.ID, 1, &old); err != nil || old.Code != first.Code {
		t.Fatal(old, err)
	}
}
func TestPublishAndLockedEntry(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	d := Definition{Name: "Build", Graph: Graph{Nodes: []Node{{ID: "start", Kind: "start"}, {ID: "end", Kind: "end"}}, Dependencies: []Dependency{{From: "start", To: "end"}}}}
	d, err := r.SaveDraft(ctx, d, "admin")
	if err != nil {
		t.Fatal(err)
	}
	published, err := r.Publish(ctx, d.ID, d.Revision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.SaveEntry(ctx, Entry{Name: "Run build", WorkflowID: d.ID, WorkflowRevision: published.Revision, Locks: []string{"workspace:build"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	e, err = r.SetEntryLock(ctx, e.ID, e.Revision, true, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveEntry(ctx, e, "admin"); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	e, err = r.SetEntryLock(ctx, e.ID, e.Revision, false, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "new name"
	d, err = r.SaveDraft(ctx, d, "admin")
	if err != nil {
		t.Fatal(err)
	}
	published, err = r.Publish(ctx, d.ID, d.Revision, "admin")
	if err != nil || published.Revision != 2 {
		t.Fatal(published, err)
	}
	if _, err = r.SaveEntry(ctx, e, "admin"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale binding accepted", err)
	}
	var old Definition
	if err = r.Version(ctx, "published", d.ID, 1, &old); err != nil || old.Name != "Build" {
		t.Fatal(old, err)
	}
}
