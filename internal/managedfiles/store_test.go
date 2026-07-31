package managedfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateNameRejectsWindowsAliasesAndReservedCaseVariants(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		".git", ".GIT", ".ScriptBoard-Trash", ".SCRIPTBOARD-UPLOAD-file",
		"NUL", "con.txt", "COM1.log", "file.txt:payload", "trailing.", "trailing ",
	} {
		if err := ValidateName(name); err == nil {
			t.Errorf("unsafe managed name %q was accepted", name)
		}
	}
	for _, name := range []string{"scriptboard.cmd", "console.txt", "com10.log", "report final.txt"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("safe managed name %q was rejected: %v", name, err)
		}
	}
}

func TestResolveEntryRejectsAliasOfReservedTemporaryFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reserved := filepath.Join(root, ".scriptboard-upload-secret")
	alias := filepath.Join(root, "upload~1")
	if err := os.WriteFile(reserved, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(reserved, alias); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	if _, _, err := Open(root).OpenRegular("upload~1"); err == nil {
		t.Fatal("hard-link alias of a reserved upload file was readable")
	}
}
