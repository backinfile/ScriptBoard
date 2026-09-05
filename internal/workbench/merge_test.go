package workbench

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

func sharedTestDB(t *testing.T) (*sql.DB, State) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "space.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range SchemaStatements {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := State{Boards: []Board{{ID: "b", Name: "Board", Items: []Item{{ID: "a", Type: "note", Text: "A"}, {ID: "c", Type: "note", Text: "C"}, {ID: "d", Type: "note", Text: "D"}}}}}
	if _, err = Save(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	s, err = Load(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return db, s
}
func patchForTest(t *testing.T, db *sql.DB, base, next State) PatchResult {
	t.Helper()
	result, err := ApplyPatch(context.Background(), db, Patch{base, next})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestPatchIndependentModulesAndPartialConflict(t *testing.T) {
	db, base := sharedTestDB(t)
	a := cloneState(base)
	a.Boards[0].Items[0].Text = "Alice"
	one := patchForTest(t, db, base, a)
	b := cloneState(base)
	b.Boards[0].Items[1].Text = "Bob"
	two := patchForTest(t, db, base, b)
	if len(two.Conflicts) != 0 || two.State.Boards[0].Items[0].Text != "Alice" || two.State.Boards[0].Items[1].Text != "Bob" {
		t.Fatal(two)
	}
	if two.State.Boards[0].Items[0].Revision != one.State.Boards[0].Items[0].Revision {
		t.Fatal("unrelated save changed module version")
	}
	c := cloneState(base)
	c.Boards[0].Items[0].Text = "Stale Alice"
	c.Boards[0].Items[2].Text = "New D"
	three := patchForTest(t, db, base, c)
	if !slices.Contains(three.Conflicts, "item:a") || three.State.Boards[0].Items[0].Text != "Alice" || three.State.Boards[0].Items[2].Text != "New D" {
		t.Fatal(three)
	}
}
func TestPatchStructureMergesAndProtectsDeletedContent(t *testing.T) {
	db, base := sharedTestDB(t)
	a := cloneState(base)
	a.Boards[0].Items = append(a.Boards[0].Items, Item{ID: "new-a", Type: "note"})
	patchForTest(t, db, base, a)
	b := cloneState(base)
	b.Boards[0].Items = append(b.Boards[0].Items, Item{ID: "new-b", Type: "note"})
	result := patchForTest(t, db, base, b)
	if len(result.Conflicts) != 0 || len(result.State.Boards[0].Items) != 5 {
		t.Fatal(result)
	}
	deleted := cloneState(base)
	deleted.Boards = nil
	result = patchForTest(t, db, base, deleted)
	if !slices.Contains(result.Conflicts, "board:b") || len(result.State.Boards) != 1 {
		t.Fatal(result)
	}
}
func TestPatchReorderIndependentOfContent(t *testing.T) {
	db, base := sharedTestDB(t)
	a := cloneState(base)
	a.Boards[0].Items[0].Text = "Updated"
	patchForTest(t, db, base, a)
	b := cloneState(base)
	slices.Reverse(b.Boards[0].Items)
	result := patchForTest(t, db, base, b)
	if len(result.Conflicts) != 0 || result.State.Boards[0].Items[2].Text != "Updated" {
		t.Fatal(result)
	}
	c := cloneState(base)
	c.Boards[0].Items[0], c.Boards[0].Items[1] = c.Boards[0].Items[1], c.Boards[0].Items[0]
	result = patchForTest(t, db, base, c)
	if !slices.Contains(result.Conflicts, "order:b") {
		t.Fatal(result)
	}
}

func TestEmptyListsDoNotModifyUntouchedModules(t *testing.T) {
	db, base := sharedTestDB(t)
	base.Boards[0].Items = append(base.Boards[0].Items, Item{ID: "todo", Type: "todo"})
	if _, err := Save(context.Background(), db, base); err != nil {
		t.Fatal(err)
	}
	base, _ = Load(context.Background(), db)
	next := cloneState(base)
	next.Boards[0].Items[3].Tasks = []Task{}
	next.Boards[0].Items[0].Text = "changed"
	result := patchForTest(t, db, base, next)
	if result.State.Boards[0].Items[3].Revision != base.Boards[0].Items[3].Revision {
		t.Fatal("empty-list rendering modified another module")
	}
}
