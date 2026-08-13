package update

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfExtractingInstallerIsMeasuredAndExtractedAsZIP(t *testing.T) {
	for _, extension := range []string{".exe", ".run"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			payload := filepath.Join(root, "payload.zip")
			file, err := os.Create(payload)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			entry, err := writer.Create("scriptboard")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			installer := filepath.Join(root, "scriptboard"+extension)
			output, err := os.Create(installer)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := output.Write([]byte("native-launcher-prefix")); err != nil {
				t.Fatal(err)
			}
			payloadBytes, err := os.ReadFile(payload)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := output.Write(payloadBytes); err != nil {
				t.Fatal(err)
			}
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}

			size, count, err := MeasureArchive(installer)
			if err != nil || size != 7 || count != 1 {
				t.Fatalf("measure self-extracting installer: size=%d count=%d err=%v", size, count, err)
			}
			destination := filepath.Join(root, "extracted")
			if err := ExtractArchive(installer, destination, size); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(destination, "scriptboard")); err != nil || string(got) != "payload" {
				t.Fatalf("extracted payload=%q err=%v", got, err)
			}
		})
	}
}

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
