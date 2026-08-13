// Package architecture contains repository-level dependency and layout gates.
package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacyAppPackageCannotReturn(t *testing.T) {
	root := repositoryRoot(t)
	if _, err := os.Stat(filepath.Join(root, "internal", "app")); !os.IsNotExist(err) {
		t.Fatalf("legacy internal/app directory exists: %v", err)
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		legacyImport := `"scriptboard/internal/` + `app"`
		if strings.Contains(string(body), legacyImport) {
			t.Errorf("legacy app import in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebUIHasOneCanonicalRoot(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{
		filepath.Join("internal", "web", "ui", "assets"),
		filepath.Join("internal", "web", "ui", "templates"),
	} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || !info.IsDir() {
			t.Fatalf("canonical Web UI directory %s is missing: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "web", "web")); !os.IsNotExist(err) {
		t.Fatalf("legacy nested Web UI root exists: %v", err)
	}
}

func TestADRNumbersDoNotAcquireNewDuplicates(t *testing.T) {
	root := repositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "docs", "adr"))
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".md" || name == "README.md" {
			continue
		}
		separator := strings.IndexByte(name, '-')
		if separator != 4 {
			t.Errorf("ADR filename must start with four digits: %s", name)
			continue
		}
		counts[name[:separator]]++
	}
	legacyDuplicates := map[string]int{"0109": 2, "0128": 2}
	for number, count := range counts {
		allowed := 1
		if legacyCount, ok := legacyDuplicates[number]; ok {
			allowed = legacyCount
		}
		if count != allowed {
			t.Errorf("ADR number %s occurs %d times; expected %d", number, count, allowed)
		}
	}
	for number, expected := range legacyDuplicates {
		if counts[number] != expected {
			t.Errorf("documented ADR number %s occurs %d times; expected %d", number, counts[number], expected)
		}
	}
}

func TestRuntimeCommandsDelegateCompositionToBootstrap(t *testing.T) {
	root := repositoryRoot(t)
	for _, command := range []string{"scriptboard", "scriptboard-broker", "scriptboard-runner", "scriptboard-ai-host"} {
		path := filepath.Join(root, "cmd", command, "main.go")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(body)
		if !strings.Contains(content, `"scriptboard/internal/bootstrap"`) {
			t.Errorf("%s main does not delegate to bootstrap", command)
		}
		forbiddenImports := []string{
			`internal/privilegebroker`,
			`internal/runnerhost`,
			`internal/assistant/runtimehost`,
		}
		if command != "scriptboard" {
			forbiddenImports = append(forbiddenImports, `"database/sql"`, `"modernc.org/sqlite"`)
		}
		for _, forbidden := range forbiddenImports {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s main composes runtime dependency %q", command, forbidden)
			}
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
