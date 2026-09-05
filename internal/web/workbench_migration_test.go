package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"scriptboard/internal/workbench"
	"testing"
)

func TestSchema65UpgradesWorkbenchAndKeepsUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES('kept','kept','hash','viewer',1,1);DROP TABLE personal_workbenches;PRAGMA user_version=65`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var username string
	if err = db.QueryRow("SELECT username FROM users WHERE id='kept'").Scan(&username); err != nil || username != "kept" {
		t.Fatal(username, err)
	}
	if _, err = workbench.Save(context.Background(), db, workbench.State{Boards: []workbench.Board{}}); err != nil {
		t.Fatal(err)
	}
}

func TestSchema66SharesAllBoardsAndRejectsStaleClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES('a','a','hash','viewer',1,1),('b','b','hash','viewer',1,1); DROP TABLE shared_workbench; PRAGMA user_version=66"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"a", "b"} {
		boards := []workbench.Board{{ID: "same", Name: user, Items: []workbench.Item{{ID: "note", Type: "note", Text: user}}}}
		content, _ := json.Marshal(boards)
		if _, err = db.Exec("INSERT INTO personal_workbenches(user_id,revision,content) VALUES(?,7,?)", user, string(content)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := workbench.Load(context.Background(), db)
	if err != nil || len(state.Boards) != 2 || state.Revision != 8 {
		t.Fatal(state, err)
	}
	if err = workbench.Validate(state); err != nil {
		t.Fatal(err)
	}
	if state.Boards[0].Items[0].Text != "a" || state.Boards[1].Items[0].Text != "b" {
		t.Fatal(state)
	}
	stale := state
	stale.Revision = 7
	if _, err = workbench.Save(context.Background(), db, stale); !errors.Is(err, workbench.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = db.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	state, err = workbench.Load(context.Background(), db)
	if err != nil || len(state.Boards) != 2 {
		t.Fatal("shared content must survive account deletion", err)
	}
}

func TestSharedMigrationRetainsCapacityForManyPersonalBoards(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, user := range []string{"a", "b"} {
		if _, err = db.Exec("INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?, 'hash','viewer',1,1)", user, user); err != nil {
			t.Fatal(err)
		}
		boards := []workbench.Board{}
		for i := 0; i < 17; i++ {
			boards = append(boards, workbench.Board{ID: fmt.Sprintf("board-%d", i), Name: "Kept", Items: []workbench.Item{}})
		}
		data, _ := json.Marshal(boards)
		if _, err = db.Exec("INSERT INTO personal_workbenches VALUES(?,1,?)", user, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = workbench.MigrateShared(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err := workbench.Load(context.Background(), db)
	if err != nil || len(state.Boards) != 34 || state.Capacity.Boards != 34 {
		t.Fatal(state, err)
	}
	state.Boards[0].Name = "Edited after upgrade"
	if _, err = workbench.Save(context.Background(), db, state); err != nil {
		t.Fatal(err)
	}
	state.Revision++
	state.Capacity.Boards = 1000
	state.Boards = append(state.Boards, workbench.Board{ID: "extra", Name: "Over capacity"})
	if _, err = workbench.Save(context.Background(), db, state); !errors.Is(err, workbench.ErrInvalid) {
		t.Fatal("client must not raise capacity", err)
	}
}
