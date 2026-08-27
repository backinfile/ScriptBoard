package hostfiles_test

import (
	"errors"
	"path/filepath"
	"testing"

	"scriptboard/internal/hostfiles"
)

func TestRunLeasesShareAnExactPublicationAndStillBlockMutations(t *testing.T) {
	root := t.TempDir()
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "run-lease-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "published.ps1")
	if err := manager.AcquireLease("run:first", script); err != nil {
		t.Fatal(err)
	}
	if err := manager.AcquireLease("run:second", script); err != nil {
		t.Fatalf("overlapping Run could not share its publication lease: %v", err)
	}
	if err := manager.AcquireLease("file-mutation:test", script); !errors.Is(err, hostfiles.ErrPathBusy) {
		t.Fatalf("file mutation error = %v, want ErrPathBusy", err)
	}
	manager.ReleaseLease("run:first")
	if !manager.LeaseConflicts(script) {
		t.Fatal("remaining overlapping Run no longer protected the publication")
	}
	manager.ReleaseLease("run:second")
	if manager.LeaseConflicts(script) {
		t.Fatal("publication remained leased after every Run finished")
	}
}

func TestRunLeasesDoNotShareContainingPaths(t *testing.T) {
	root := t.TempDir()
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "run-lease-containment-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AcquireLease("run:directory", root); err != nil {
		t.Fatal(err)
	}
	if err := manager.AcquireLease("run:script", filepath.Join(root, "published.ps1")); !errors.Is(err, hostfiles.ErrPathBusy) {
		t.Fatalf("containing Run lease error = %v, want ErrPathBusy", err)
	}
}
