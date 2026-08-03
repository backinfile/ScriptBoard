package capability

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogLoadsOnlyDigestBoundedPlaybooks(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "runtime"))
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.BundleVersion() != "1.0.0" || len(catalog.List()) != 5 {
		t.Fatalf("catalog = version %q resources %#v", catalog.BundleVersion(), catalog.List())
	}
	playbook, err := catalog.Resolve("diagnose-failed-run", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if playbook.ID != "diagnose-failed-run" || len(playbook.Guidance) == 0 || playbook.AutomaticSelection {
		t.Fatalf("playbook = %#v", playbook)
	}
}

func TestCatalogRejectsChangedAndEscapingResources(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "runtime"))
	tampered := t.TempDir()
	if err := copyBundle(root, tampered); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tampered, "playbooks", "diagnose-failed-run.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tampered); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("tampered bundle error = %v", err)
	}
}

func copyBundle(source, destination string) error {
	if err := os.MkdirAll(filepath.Join(destination, "playbooks"), 0o700); err != nil {
		return err
	}
	for _, relative := range []string{
		"capabilities.json", "playbooks/diagnose-failed-run.md", "playbooks/investigate-website-incident.md",
		"playbooks/triage-host-pressure.md", "playbooks/review-script-safety.md", "playbooks/design-schedule.md",
	} {
		body, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(relative)))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, filepath.FromSlash(relative)), body, 0o600); err != nil {
			return err
		}
	}
	return nil
}
