package memorysettings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"scriptboard/internal/config"
	"scriptboard/internal/resourcelimits"
	"scriptboard/internal/store/memorysettings"
	"sync"
	"testing"
)

func TestStoredMemoryPersistsAndOverridesStartupConfiguration(t *testing.T) {
	root := t.TempDir()
	first, err := memorysettings.Ensure(root, resourcelimits.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	memory := first.Memory
	memory.Total = "8GiB"
	memory.PerRun = "512MiB"
	if err := memorysettings.Save(root, first.Revision, memory); err != nil {
		t.Fatal(err)
	}
	reopened, err := memorysettings.Ensure(root, resourcelimits.Defaults())
	if err != nil || reopened.Memory != memory {
		t.Fatalf("reopen=%+v err=%v", reopened, err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("runner_memory_limit: 1GiB\n"), 0600)
	loaded, err := config.Load([]string{"--config", path, "--state-root", root, "--runner-memory-limit", "2GiB"}, func(string) string { return "" })
	if err != nil || loaded.Memory != memory {
		t.Fatalf("startup replaced stored limits: %+v %v", loaded.Memory, err)
	}
	if err := memorysettings.Save(root, first.Revision, memory); !errors.Is(err, memorysettings.ErrConflict) {
		t.Fatalf("stale save=%v", err)
	}
	destination := filepath.Join(t.TempDir(), "runner-memory.db")
	if ok, err := memorysettings.Backup(context.Background(), root, destination); err != nil || !ok {
		t.Fatal(err)
	}
	restored, err := memorysettings.Read(filepath.Dir(destination))
	if err != nil || restored.Memory != memory {
		t.Fatalf("backup=%+v err=%v", restored, err)
	}
}
func TestConcurrentMemorySaveHasOneWinner(t *testing.T) {
	root := t.TempDir()
	first, err := memorysettings.Ensure(root, resourcelimits.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := first.Memory
			next.Total = "6GiB"
			results <- memorysettings.Save(root, first.Revision, next)
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, memorysettings.ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}
func TestInvalidMemoryNeverReplacesSavedPolicy(t *testing.T) {
	root := t.TempDir()
	first, err := memorysettings.Ensure(root, resourcelimits.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	bad := first.Memory
	bad.Total = "0"
	if err := memorysettings.Save(root, first.Revision, bad); err == nil {
		t.Fatal("accepted zero limit")
	}
	after, err := memorysettings.Read(root)
	if err != nil || after != first {
		t.Fatalf("invalid save changed state: %+v %v", after, err)
	}
}
