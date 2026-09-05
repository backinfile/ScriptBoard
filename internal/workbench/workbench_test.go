package workbench

import (
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestPrivateStoreRevisionAndValidation(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE users(id TEXT PRIMARY KEY); INSERT INTO users VALUES ('a'),('b')"); err != nil {
		t.Fatal(err)
	}
	for _, query := range SchemaStatements {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	link := Item{ID: "link", Type: "links", Title: "Reference", Links: []Link{{Title: "HTTP", URL: "http://example.com"}, {Title: "TLS", URL: "https://example.com"}}}
	drawing := Item{ID: "draw", Type: "draw", Size: "xlarge", Strokes: []Stroke{{Color: "#3b5bfd", Points: []Point{{1, 2}, {3, 4}}}}}
	state := State{Boards: []Board{{ID: "board", Name: "Work", Items: []Item{link, drawing}}}}
	rev, err := Save(ctx, db, "a", state)
	if err != nil || rev != 1 {
		t.Fatalf("save %d %v", rev, err)
	}
	if _, err = Save(ctx, db, "a", state); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	other, err := Load(ctx, db, "b")
	if err != nil || len(other.Boards) != 0 {
		t.Fatal("cross-user data leak")
	}
	state.Revision = rev
	state.Boards[0].Name = "Updated"
	rev, err = Save(ctx, db, "a", state)
	if err != nil || rev != 2 {
		t.Fatal(rev, err)
	}
	loaded, err := Load(ctx, db, "a")
	if err != nil || loaded.Boards[0].Name != "Updated" || loaded.Boards[0].Items[1].Strokes[0].Points[1] != (Point{3, 4}) {
		t.Fatal(loaded, err)
	}
	state.Boards[0].Items[0].Links[0].URL = "javascript:alert(1)"
	if Validate(state) == nil {
		t.Fatal("unsafe URL accepted")
	}
}
func TestValidationRejectsMalformedState(t *testing.T) {
	cases := []State{
		{Revision: -1},
		{Boards: []Board{{ID: "bad id", Name: "x"}}},
		{Boards: []Board{{ID: "b", Name: "x", Items: []Item{{ID: "b", Type: "note"}}}}},
		{Boards: []Board{{ID: "b", Name: "x", Items: []Item{{ID: "w", Type: "timer", Mode: "999"}}}}},
		{Boards: []Board{{ID: "b", Name: "x", Items: []Item{{ID: "w", Type: "draw", Size: "huge"}}}}},
	}
	for _, s := range cases {
		if Validate(s) == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}
