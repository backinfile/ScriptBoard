package hostfiles_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scriptboard/internal/hostfiles"
)

type fixedTopology struct {
	root string
}

func (topology fixedTopology) Roots() ([]hostfiles.Entry, error) {
	return []hostfiles.Entry{{Name: topology.root, Path: topology.root, Kind: hostfiles.Directory}}, nil
}

func (topology fixedTopology) FilesystemRoot(string) (string, error) {
	return topology.root, nil
}

func (fixedTopology) Restricted(string) bool { return false }

func TestManagerListsAbsoluteDirectoryAndHidesProtectedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	visible := filepath.Join(root, "visible.txt")
	protected := filepath.Join(root, "private")
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatalf("write visible file: %v", err)
	}
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatalf("create protected directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(protected, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write protected file: %v", err)
	}

	manager, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{protected}})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}
	entries, err := manager.List(root)
	if err != nil {
		t.Fatalf("list host directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != visible || entries[0].Name != "visible.txt" {
		t.Fatalf("entries = %#v, want only %q", entries, visible)
	}

	if _, err := manager.ReadText(filepath.Join(protected, "secret.txt"), 1<<20); !errors.Is(err, hostfiles.ErrProtected) {
		t.Fatalf("read protected file error = %v, want ErrProtected", err)
	}
}

func TestManagerNeverFollowsFilesystemLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create filesystem link: %v", err)
	}

	manager, err := hostfiles.Open(hostfiles.Options{})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}
	entries, err := manager.List(root)
	if err != nil {
		t.Fatalf("list link parent: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != hostfiles.Restricted {
		t.Fatalf("link entries = %#v, want one restricted entry", entries)
	}
	if _, err := manager.ReadText(filepath.Join(link, "secret.txt"), 1<<20); err == nil {
		t.Fatal("read through a filesystem link unexpectedly succeeded")
	}
}

func TestManagerReportsHostRoots(t *testing.T) {
	t.Parallel()

	manager, err := hostfiles.Open(hostfiles.Options{})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}
	roots, err := manager.Roots()
	if err != nil {
		t.Fatalf("list host roots: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("host roots are empty")
	}
	for _, root := range roots {
		if root.Kind != hostfiles.Directory || !filepath.IsAbs(root.Path) {
			t.Fatalf("invalid host root: %#v", root)
		}
	}
}

func TestSystemFilesystemRootRecognizesTheHostVolumeRoot(t *testing.T) {
	t.Parallel()

	root := string(filepath.Separator)
	if volume := filepath.VolumeName(t.TempDir()); volume != "" {
		root = volume + string(filepath.Separator)
	}
	isRoot, err := hostfiles.IsFilesystemRoot(root)
	if err != nil {
		t.Fatalf("inspect filesystem root: %v", err)
	}
	if !isRoot {
		t.Fatalf("%q was not recognized as a filesystem root", root)
	}
}

func TestHostPathHelpersKeepAbsoluteUnicodePathsInsideTheModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "数据")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "path-helper-test", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := manager.Destination(directory, "脚本.ps1")
	if err != nil {
		t.Fatalf("canonical destination: %v", err)
	}
	if !filepath.IsAbs(destination) || hostfiles.Base(destination) != "脚本.ps1" {
		t.Fatalf("destination = %q", destination)
	}
	if parent, ok := hostfiles.Parent(destination); !ok || hostfiles.ComparisonKey(parent) != hostfiles.ComparisonKey(directory) {
		t.Fatalf("parent = %q, ok = %t", parent, ok)
	}
	rebased, err := hostfiles.Rebase(directory, filepath.Join(root, "归档"), destination)
	if err != nil || hostfiles.Base(rebased) != "脚本.ps1" {
		t.Fatalf("rebase = %q, error = %v", rebased, err)
	}
	breadcrumbs := hostfiles.Breadcrumbs(destination)
	if len(breadcrumbs) < 2 || hostfiles.ComparisonKey(breadcrumbs[len(breadcrumbs)-1].Path) != hostfiles.ComparisonKey(destination) {
		t.Fatalf("breadcrumbs = %#v", breadcrumbs)
	}

	upper, lower := hostfiles.ComparisonKey(strings.ToUpper(destination)), hostfiles.ComparisonKey(strings.ToLower(destination))
	if runtime.GOOS == "windows" && upper != lower {
		t.Fatalf("Windows comparison keys differ: %q != %q", upper, lower)
	}
	if runtime.GOOS != "windows" && upper == lower {
		t.Fatalf("Unix comparison keys unexpectedly ignore case: %q", upper)
	}
}

func TestManagerTrashIsRecoverableAndPrivateToTheFilesystem(t *testing.T) {
	t.Parallel()

	filesystem := t.TempDir()
	directory := filepath.Join(filesystem, "files")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create file directory: %v", err)
	}
	path := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(path, []byte("notes"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "test-instance", Topology: fixedTopology{root: filesystem}})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}

	trashed, err := manager.MoveToTrash(path, "trash-id")
	if err != nil {
		t.Fatalf("move to trash: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original path still exists: %v", err)
	}
	if filepath.Dir(trashed.StoredPath) != filepath.Join(filesystem, ".scriptboard-trash") {
		t.Fatalf("trash path = %q", trashed.StoredPath)
	}
	entries, err := manager.List(filesystem)
	if err != nil {
		t.Fatalf("list filesystem root: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == ".scriptboard-trash" {
			t.Fatalf("private trash leaked into file listing: %#v", entries)
		}
	}
	if err := manager.RestoreFromTrash(trashed.StoredPath, trashed.OriginalPath); err != nil {
		t.Fatalf("restore trash: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "notes" {
		t.Fatalf("restored content = %q, err = %v", content, err)
	}
}

func TestManagerRefusesTrashOwnedByAnotherInstance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	trashRoot := filepath.Join(root, ".scriptboard-trash")
	if err := os.Mkdir(trashRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trashRoot, ".scriptboard-owner"), []byte("scriptboard-trash-v1\nother-instance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "this-instance", Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.MoveToTrash(path, "entry-id"); err == nil {
		t.Fatal("delete adopted a trash directory owned by another instance")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "keep" {
		t.Fatalf("source changed after trash ownership rejection: content=%q error=%v", content, err)
	}
}

func TestManagerRefusesToClaimAnUnmarkedExistingTrashDirectory(t *testing.T) {
	t.Parallel()

	filesystem := t.TempDir()
	files := filepath.Join(filesystem, "files")
	trash := filepath.Join(filesystem, ".scriptboard-trash")
	if err := os.Mkdir(files, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(trash, "user-content.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(files, "source.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "safe-owner", Topology: fixedTopology{root: filesystem}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MoveToTrash(source, "entry"); err == nil {
		t.Fatal("unmarked existing trash directory was claimed")
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
		t.Fatalf("pre-existing trash content changed: content=%q error=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(trash, ".scriptboard-owner")); !os.IsNotExist(err) {
		t.Fatalf("ownership marker was created in an unowned directory: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after refused delete: %v", err)
	}
}

func TestManagerRefusesMutationOfAProtectedAncestor(t *testing.T) {
	t.Parallel()

	filesystem := t.TempDir()
	source := filepath.Join(filesystem, "source")
	protected := filepath.Join(source, "private")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatalf("create protected tree: %v", err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{
		ProtectedPaths: []string{protected},
		Topology:       fixedTopology{root: filesystem},
	})
	if err != nil {
		t.Fatalf("open host files: %v", err)
	}

	if err := manager.Move(source, filepath.Join(filesystem, "moved")); !errors.Is(err, hostfiles.ErrProtected) {
		t.Fatalf("move protected ancestor error = %v, want ErrProtected", err)
	}
	if manager.CanMutate(source) {
		t.Fatal("protected ancestor was advertised as mutable")
	}
}

func TestManagerRefusesMoveAndDeleteOfAFilesystemRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manager, err := hostfiles.Open(hostfiles.Options{Topology: fixedTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.MoveToTrash(root, "root-delete"); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("delete filesystem root error = %v", err)
	}
	if manager.CanMutate(root) {
		t.Fatal("filesystem root was advertised as mutable")
	}
	destination := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-moved")
	if err := manager.Move(root, destination); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("move filesystem root error = %v", err)
	}
}

func TestLexicalProtectedPathStillProtectsItsAncestorWhenItIsASymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostRoot := filepath.Join(root, "host")
	actualInstall := filepath.Join(root, "actual-install")
	if err := os.MkdirAll(hostRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actualInstall, 0o700); err != nil {
		t.Fatal(err)
	}
	installLink := filepath.Join(hostRoot, "install")
	if err := os.Symlink(actualInstall, installLink); err != nil {
		t.Skipf("create protected-path symlink: %v", err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{installLink}})
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.Move(hostRoot, filepath.Join(root, "moved-host")); !errors.Is(err, hostfiles.ErrProtected) {
		t.Fatalf("moving ancestor of lexical protected path returned %v", err)
	}
}
