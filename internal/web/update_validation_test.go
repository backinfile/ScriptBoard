package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/buildinfo"
	updatepkg "scriptboard/internal/update"
)

func TestUpdateValidationResumesWorkflowRecoveryAfterCommit(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := updatepkg.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := updatepkg.OperationDirectory(root, id)
	if err != nil {
		t.Fatal(err)
	}
	manifest := updatepkg.Manifest{
		Schema: updatepkg.ManifestSchema, Product: "scriptboard", Repository: buildinfo.Repository,
		Version: "1.2.3", Tag: "v1.2.3", Commit: strings.Repeat("b", 40),
		PublishedAt: "2026-07-29T00:00:00Z", DatabaseSchema: 14, UpdaterProtocol: 2, MinimumUpdaterProtocol: 2,
	}
	for _, platform := range []struct{ os, arch, extension string }{
		{"windows", "amd64", "-setup.exe"}, {"windows", "arm64", "-setup.exe"},
		{"linux", "amd64", ".run"}, {"linux", "arm64", ".run"},
	} {
		manifest.Assets = append(manifest.Assets, updatepkg.Asset{
			OS: platform.os, Arch: platform.arch,
			Name:   fmt.Sprintf("scriptboard-v1.2.3-%s-%s%s", platform.os, platform.arch, platform.extension),
			SHA256: strings.Repeat("c", 64), Size: 100, UnpackedSize: 200,
		})
	}
	operation := updatepkg.Operation{
		Schema: updatepkg.OperationSchema, ID: id, Nonce: strings.Repeat("n", 24), Phase: updatepkg.PhaseValidating,
		PreviousVersion: "1.2.2", TargetVersion: manifest.Version, PreviousCommit: strings.Repeat("a", 40), TargetCommit: manifest.Commit,
		StateRoot: root, InstallRoot: filepath.Join(root, "installed"), ConfigPath: filepath.Join(root, "config.yaml"),
		DatabasePath: filepath.Join(root, "app.db"), ArchivePath: filepath.Join(directory, "archive"),
		ExtractedPath: filepath.Join(directory, "extracted"), SnapshotPath: filepath.Join(directory, "snapshot"),
		Manifest: manifest,
	}
	if err := updatepkg.SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	if _, active := updatepkg.PendingValidation(root, buildinfo.Current()); !active {
		t.Fatal("saved update fixture does not enter validation")
	}
	app, err := Open(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if !app.validation.Load() {
		t.Fatal("expected the application to open in update validation mode")
	}
	if _, err := app.db.Exec("INSERT INTO workflow_runs (id, entry_id, status, snapshot, actor, created_at, updated_at) VALUES ('fixture', '', 'running', '{}', '{}', 0, 0)"); err != nil {
		t.Fatal(err)
	}
	operation.Phase = updatepkg.PhaseCommitted
	if err := updatepkg.SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for app.validation.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if app.validation.Load() {
		t.Fatal("application did not leave update validation")
	}
	var status string
	if err := app.db.QueryRow("SELECT status FROM workflow_runs WHERE id = 'fixture'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "needs_attention" {
		t.Fatalf("workflow recovery did not run after commit: status=%q", status)
	}
}
