package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path"
	"path/filepath"
	"scriptboard/internal/testsupport/securitycorpus"
	"strings"
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

func TestArchiveExtractionRejectsSharedUnsafePathCorpusWithoutPublishing(t *testing.T) {
	for index, name := range securitycorpus.UnsafeArchivePaths() {
		archivePath := filepath.Join(t.TempDir(), "unsafe.zip")
		file, err := os.Create(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		entry, err := writer.Create(name)
		if err == nil {
			_, err = entry.Write([]byte("x"))
		}
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatalf("build unsafe archive %d: %v", index, err)
		}
		if _, _, err := MeasureArchive(archivePath); err == nil {
			t.Fatalf("unsafe archive path %q was measured as safe", name)
		}
		destination := filepath.Join(filepath.Dir(archivePath), "published")
		if err := ExtractArchive(archivePath, destination, 1); err == nil {
			t.Fatalf("unsafe archive path %q was extracted", name)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatalf("unsafe archive path %q left a destination: %v", name, err)
		}
	}
}

func TestTarArchiveRejectsLinksAndSpecialFiles(t *testing.T) {
	for _, typeflag := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo, tar.TypeChar, tar.TypeBlock} {
		root := t.TempDir()
		archivePath := filepath.Join(root, "unsafe.tar.gz")
		file, err := os.Create(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		compressed := gzip.NewWriter(file)
		writer := tar.NewWriter(compressed)
		err = writer.WriteHeader(&tar.Header{Name: "release/unsafe", Typeflag: typeflag, Linkname: "../outside", Mode: 0o600})
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if closeErr := compressed.Close(); err == nil {
			err = closeErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := MeasureArchive(archivePath); err == nil {
			t.Fatalf("unsafe tar type %d was measured as safe", typeflag)
		}
		destination := filepath.Join(root, "published")
		if err := ExtractArchive(archivePath, destination, 1); err == nil {
			t.Fatalf("unsafe tar type %d was extracted", typeflag)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatalf("unsafe tar type %d left a destination: %v", typeflag, err)
		}
	}
}

func TestSafeArchivePathRejectsWindowsAliasesAndStreams(t *testing.T) {
	t.Parallel()

	unsafe := append(securitycorpus.UnsafeArchivePaths(), "release/COM¹.log", "release/Lpt9", "release/NUL .txt")
	for _, name := range unsafe {
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

func FuzzSafeArchivePath(f *testing.F) {
	for _, seed := range append(securitycorpus.UnsafeArchivePaths(), "release/scriptboard", "release/config/", ".") {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		normalized, err := safeArchivePath(raw)
		if err != nil || normalized == "" {
			return
		}
		if raw != normalized && raw != normalized+"/" {
			t.Fatalf("archive path %q was silently normalized to %q", raw, normalized)
		}
		if path.Clean(normalized) != normalized || path.IsAbs(normalized) || strings.ContainsAny(normalized, "\\\x00\r\n") {
			t.Fatalf("unsafe normalized archive path %q from %q", normalized, raw)
		}
	})
}
