package web

import (
	"context"
	"path/filepath"
	"scriptboard/internal/workbench"
	"testing"
)

func TestSchema65UpgradesPrivateWorkbenchAndKeepsUsers(t *testing.T) {
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
	if _, err = workbench.Save(context.Background(), db, "kept", workbench.State{Boards: []workbench.Board{}}); err != nil {
		t.Fatal(err)
	}
}
