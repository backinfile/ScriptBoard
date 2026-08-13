package web_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	app "scriptboard/internal/web"
)

func TestOpenRejectsTailDeletionThatStillPassesLocalHashChain(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ResetAdminCredentials("admin"); err != nil {
		_ = application.Close()
		t.Fatal(err)
	}
	if _, err := application.ResetAdminCredentials("admin"); err != nil {
		_ = application.Close()
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var lastID int64
	if err := database.QueryRow(`SELECT MAX(id) FROM audit_events`).Scan(&lastID); err != nil || lastID < 2 {
		_ = database.Close()
		t.Fatalf("last audit id=%d err=%v", lastID, err)
	}
	var previousHash string
	if err := database.QueryRow(`SELECT event_hash FROM audit_events WHERE id < ? ORDER BY id DESC LIMIT 1`, lastID).Scan(&previousHash); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM audit_events WHERE id = ?`, lastID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE audit_chain_state SET tail_hash = ? WHERE id = 1`, previousHash); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := app.Open(app.Config{StateRoot: stateRoot})
	if reopened != nil {
		_ = reopened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "external audit checkpoint") {
		t.Fatalf("tail-deleted startup error=%v", err)
	}
}
