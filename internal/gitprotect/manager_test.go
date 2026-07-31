package gitprotect

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDescribeEntriesReturnsProtectionReasonsInOneSnapshot(t *testing.T) {
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, ".gitignore"), []byte("ignored.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "kept.txt"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "ignored.log"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, "gitprotect.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE git_state (
		id INTEGER PRIMARY KEY,
		status TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		branch TEXT NOT NULL,
		git_executable TEXT NOT NULL,
		max_tracked_file_bytes INTEGER NOT NULL,
		max_repository_bytes INTEGER NOT NULL,
		last_commit TEXT NOT NULL,
		abnormal_reason TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	manager, err := New(db, managedRoot, gitExecutable, stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(); err != nil {
		t.Fatalf("enable Version Protection: %v", err)
	}

	description, err := manager.DescribeEntries([]File{
		{Path: "kept.txt", Size: 4},
		{Path: "ignored.log", Size: 7},
		{Path: "large.bin", Size: (10 << 20) + 1},
	})
	if err != nil {
		t.Fatalf("DescribeEntries: %v", err)
	}
	if !description.State.Enabled || description.State.RepositoryBytes == 0 {
		t.Fatalf("state = %#v", description.State)
	}
	want := map[string]string{
		"kept.txt":    "已受保护",
		"ignored.log": "未保护：被 .gitignore 排除",
		"large.bin":   "未保护：超过 10 MiB",
	}
	for path, reason := range want {
		if got := description.Reasons[path]; got != reason {
			t.Fatalf("reason for %q = %q, want %q", path, got, reason)
		}
	}
}
