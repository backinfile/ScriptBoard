package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"scriptboard/internal/externaltrigger"
)

func TestMovedDirectoryRebasesExternalUploadReference(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "destination")
	target := filepath.Join(source, "incoming")
	configJSON, _ := json.Marshal(externaltrigger.UploadConfig{Directory: target, MaxBytes: 1024, ConflictPolicy: "reject"})
	if _, err := db.Exec(`INSERT INTO external_trigger_keys(id, label, token_hash, token_hint, enabled, created_at, updated_at)
		VALUES ('key', 'Key', 'hash', 'hint', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO external_trigger_entries(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES ('entry', 'key', 'upload', 'Upload', 'upload', ?, ?, 1, 1, 1)`, target, string(configJSON)); err != nil {
		t.Fatal(err)
	}
	transaction, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := updateMovedScriptReferences(transaction, source, destination); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var movedTarget, movedConfigJSON string
	if err := db.QueryRow("SELECT target, config_json FROM external_trigger_entries WHERE id = 'entry'").Scan(&movedTarget, &movedConfigJSON); err != nil {
		t.Fatal(err)
	}
	var movedConfig externaltrigger.UploadConfig
	if err := json.Unmarshal([]byte(movedConfigJSON), &movedConfig); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(destination, "incoming")
	if movedTarget != want || movedConfig.Directory != want {
		t.Fatalf("moved upload reference = target %q config %q, want %q", movedTarget, movedConfig.Directory, want)
	}
}
