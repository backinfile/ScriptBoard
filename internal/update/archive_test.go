package update

import "testing"

func TestSafeArchivePathRejectsWindowsAliasesAndStreams(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"release/NUL",
		"release/con.txt",
		"release/COM1.log",
		"release/COM¹.log",
		"release/Lpt9",
		"release/file.txt:payload",
		"release/trailing.",
		"release/trailing ",
		"release/line\nbreak",
		"release/NUL .txt",
	} {
		if _, err := safeArchivePath(name); err == nil {
			t.Fatalf("unsafe archive path %q was accepted", name)
		}
	}
	for _, name := range []string{
		"release/scriptboard.exe",
		"release/config/default.yaml",
		"release/console.txt",
		"release/com10.log",
	} {
		if got, err := safeArchivePath(name); err != nil || got != name {
			t.Fatalf("safe archive path %q: got %q, error %v", name, got, err)
		}
	}
}
