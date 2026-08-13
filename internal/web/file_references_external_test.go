package web

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
	if _, err := db.Exec(`INSERT INTO external_trigger_keys(id, label, token_hash, token_hint, enabled, created_at, updated_at)
		VALUES ('log-key', 'Log key', 'log-hash', 'log-hint', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO external_trigger_entries(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES ('entry', 'key', 'upload', 'Upload', 'upload', ?, ?, 1, 1, 1)`, target, string(configJSON)); err != nil {
		t.Fatal(err)
	}
	logTarget := filepath.Join(source, "events.log")
	logConfigJSON, _ := json.Marshal(externaltrigger.LogConfig{File: logTarget, MaxMessageBytes: 1024})
	if _, err := db.Exec(`INSERT INTO external_trigger_entries(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
		VALUES ('log-entry', 'log-key', 'log', 'Log', 'log', ?, ?, 1, 1, 1)`, logTarget, string(logConfigJSON)); err != nil {
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
	var movedLogTarget, movedLogConfigJSON string
	if err := db.QueryRow("SELECT target, config_json FROM external_trigger_entries WHERE id = 'log-entry'").Scan(&movedLogTarget, &movedLogConfigJSON); err != nil {
		t.Fatal(err)
	}
	var movedLogConfig externaltrigger.LogConfig
	if err := json.Unmarshal([]byte(movedLogConfigJSON), &movedLogConfig); err != nil {
		t.Fatal(err)
	}
	wantLog := filepath.Join(destination, "events.log")
	if movedLogTarget != wantLog || movedLogConfig.File != wantLog {
		t.Fatalf("moved log reference = target %q config %q, want %q", movedLogTarget, movedLogConfig.File, wantLog)
	}
}

func TestExternalFileReferencesIncludeLogAndUploadTargets(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := filepath.Join(t.TempDir(), "targets")
	for _, item := range []struct{ id, action, target, config string }{
		{"upload", "upload", filepath.Join(root, "incoming"), `{"directory":"` + filepath.ToSlash(filepath.Join(root, "incoming")) + `"}`},
		{"log", "log", filepath.Join(root, "events.log"), `{"file":"` + filepath.ToSlash(filepath.Join(root, "events.log")) + `"}`},
	} {
		if _, err := db.Exec(`INSERT INTO external_trigger_keys(id, label, token_hash, token_hint, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, 1, 1, 1)`, item.id+"-key", item.id+" key", item.id+" hash", item.id+" hint"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO external_trigger_entries(id, key_id, name, label, action_type, target, config_json, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, 1)`, item.id, item.id+"-key", item.id, item.id, item.action, item.target, item.config); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{db: db}
	count, err := app.countExternalFileReferences(root)
	if err != nil || count != 2 {
		t.Fatalf("external file references=%d error=%v, want 2", count, err)
	}
}
