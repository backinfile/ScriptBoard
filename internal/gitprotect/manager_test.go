package gitprotect

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	if err := os.Mkdir(filepath.Join(managedRoot, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "nested", "file.txt"), []byte("baseline"), 0o600); err != nil {
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
	baseline, err := manager.State()
	if err != nil {
		t.Fatal(err)
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

	attributesPath := filepath.Join(managedRoot, ".gitattributes")
	if err := os.WriteFile(attributesPath, []byte(strings.Repeat("x", int(maxRepositoryMetadataBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.validateSafeRepository(); err == nil {
		t.Fatal("oversized .gitattributes was accepted")
	}
	if err := os.Remove(attributesPath); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(managedRoot, ".git", "config")
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, unsafeConfig := range map[string]string{
		"include":         "\n[include]\n\tpath = C:/unsafe/external-config\n",
		"commit signing":  "\n[commit]\n\tgpgSign = true\n",
		"credential hook": "\n[credential]\n\thelper = !unsafe-command\n",
		"maintenance":     "\n[gc]\n\trecentObjectsHook = unsafe-command\n",
	} {
		t.Run(name, func(t *testing.T) {
			content := append(append([]byte(nil), originalConfig...), unsafeConfig...)
			if err := os.WriteFile(configPath, content, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.WriteFile(configPath, originalConfig, 0o600) })
			if err := manager.validateSafeRepository(); err == nil {
				t.Fatalf("unsafe Git configuration %q was accepted", name)
			}
		})
	}

	worktreeConfig := filepath.Join(managedRoot, ".git", "config.worktree")
	if err := os.WriteFile(worktreeConfig, []byte("[gpg]\n\tprogram = unsafe-command\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.validateSafeRepository(); err == nil {
		t.Fatal("unsafe worktree Git configuration was accepted")
	}
	if err := os.Remove(worktreeConfig); err != nil {
		t.Fatal(err)
	}

	commonDirectory := filepath.Join(managedRoot, ".git", "commondir")
	if err := os.WriteFile(commonDirectory, []byte("../outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.validateSafeRepository(); err == nil {
		t.Fatal("external Git common directory was accepted")
	}
	if err := os.Remove(commonDirectory); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "file.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(managedRoot, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(managedRoot, "nested")); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("symlink escape test unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
	if err := manager.RestoreFile("nested/file.txt", baseline.LastCommit); err == nil {
		t.Fatal("restore followed a symlink outside the managed root")
	}
	content, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "outside" {
		t.Fatalf("outside file was changed to %q", content)
	}
}

func TestValidCommitIDRejectsGitOptionInjection(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"--output=C:/temp/pwned:managed.txt",
		"HEAD",
		strings.Repeat("a", 39),
		strings.Repeat("g", 40),
		strings.Repeat("a", 41),
	} {
		if validCommitID(value) {
			t.Fatalf("unsafe commit ID %q was accepted", value)
		}
	}
	for _, value := range []string{strings.Repeat("a", 40), strings.Repeat("B", 64)} {
		if !validCommitID(value) {
			t.Fatalf("valid commit ID %q was rejected", value)
		}
	}
}
