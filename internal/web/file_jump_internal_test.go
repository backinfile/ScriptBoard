package web

import (
	"context"
	"os"
	"path/filepath"
	"scriptboard/internal/hostfiles"
	"testing"
)

func TestFileJumpSearchCancellationAndProtectedPaths(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "secret.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{private}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{files: manager}
	view := fileJumpView{CurrentPath: root, Query: "secret", Recursive: true, ShowHidden: true}
	if err = app.searchFileJump(context.Background(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 0 {
		t.Fatal("protected file leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	view.Query = "anything"
	if err = app.searchFileJump(ctx, &view); err != nil {
		t.Fatal(err)
	}
	if !view.Partial {
		t.Fatal("cancelled search must report partial coverage")
	}
	if _, err = app.fileJumpDestination(context.Background(), root, private, "", "", false); err == nil {
		t.Fatal("protected destination accepted")
	}
	if _, err = fileJumpPath("", "relative"); err == nil {
		t.Fatal("resolved against service working directory")
	}
}

type jumpRootsTopology struct{ roots []hostfiles.Entry }

func (topology jumpRootsTopology) Roots() ([]hostfiles.Entry, error) { return topology.roots, nil }
func (topology jumpRootsTopology) FilesystemRoot(path string) (string, error) {
	return filepath.VolumeName(path) + string(filepath.Separator), nil
}
func (jumpRootsTopology) Restricted(string) bool { return false }

func TestFileJumpAllSearchesAcrossRootsAndSkipsUnavailableLocations(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	private := filepath.Join(second, "private")
	for _, directory := range []string{first, second, private} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(first, "match-a.txt"), filepath.Join(second, "match-b.txt"), filepath.Join(private, "match-secret.txt")} {
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	topology := jumpRootsTopology{roots: []hostfiles.Entry{{Path: filepath.Join(root, "unavailable"), Kind: hostfiles.Directory}, {Path: first, Kind: hostfiles.Directory}, {Path: second, Kind: hostfiles.Directory}}}
	manager, err := hostfiles.Open(hostfiles.Options{Topology: topology, ProtectedPaths: []string{private}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{files: manager}
	view := fileJumpView{CurrentPath: first, Query: "match", All: true}
	if err = app.searchFileJump(context.Background(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 2 || !view.Partial {
		t.Fatalf("all search: %+v", view)
	}
	if view.CurrentPath != first {
		t.Fatal("all search changed the address base")
	}
	view = fileJumpView{CurrentPath: first, Query: "match", Recursive: true}
	if err = app.searchFileJump(context.Background(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 1 || view.Partial {
		t.Fatalf("directory search: %+v", view)
	}
}
