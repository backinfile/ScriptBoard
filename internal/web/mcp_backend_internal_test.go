package web

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPQuickRunPaginationAdvancesPastInvalidPublications(t *testing.T) {
	application, err := Open(Config{StateRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	scriptPath := filepath.Join(t.TempDir(), "published.cmd")
	content := []byte("@echo off\r\necho ready\r\n")
	if err := os.WriteFile(scriptPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	for index := 1; index <= 4; index++ {
		publication := "invalid"
		if index == 4 {
			publication = digest
		}
		id := fmt.Sprintf("quick-%02d", index)
		if _, err := application.db.Exec(`INSERT INTO quick_runs
			(id,name,script_path,script_path_key,arguments_template,timeout_seconds,sort_order,created_at,locked,script_sha256,revision,updated_at)
			VALUES(?,?,?,?,?,30,?,?,0,?,1,?)`, id, id, scriptPath, scriptPath, "", index, index, publication, index); err != nil {
			t.Fatal(err)
		}
	}
	page, err := application.ListQuickRuns(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "quick-04" || page.NextCursor != "" {
		t.Fatalf("page=%+v, want the valid publication after invalid rows", page)
	}
}
