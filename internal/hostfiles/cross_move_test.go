package hostfiles_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"scriptboard/internal/hostfiles"
)

type splitTopology struct {
	roots []string
}

func (topology splitTopology) Roots() ([]hostfiles.Entry, error) {
	entries := make([]hostfiles.Entry, 0, len(topology.roots))
	for _, root := range topology.roots {
		entries = append(entries, hostfiles.Entry{Name: filepath.Base(root), Path: root, Kind: hostfiles.Directory})
	}
	return entries, nil
}

func (topology splitTopology) FilesystemRoot(path string) (string, error) {
	for _, root := range topology.roots {
		if hostfiles.Contains(root, path) {
			return root, nil
		}
	}
	return "", os.ErrNotExist
}

func (splitTopology) Restricted(string) bool { return false }

type memoryOperationStore struct {
	mu         sync.Mutex
	operations map[string]hostfiles.FileOperation
	phases     []hostfiles.OperationPhase
	onUpdate   func(hostfiles.FileOperation)
	cancelWhen func(hostfiles.FileOperation) bool
	commitFail int
	updateFail map[hostfiles.OperationPhase]int
}

func newMemoryOperationStore() *memoryOperationStore {
	return &memoryOperationStore{operations: make(map[string]hostfiles.FileOperation)}
}

func (store *memoryOperationStore) Create(_ context.Context, operation hostfiles.FileOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.operations[operation.ID] = operation
	store.phases = append(store.phases, operation.Phase)
	return nil
}

func (store *memoryOperationStore) Update(_ context.Context, operation hostfiles.FileOperation) error {
	store.mu.Lock()
	if store.updateFail != nil && store.updateFail[operation.Phase] > 0 {
		store.updateFail[operation.Phase]--
		store.mu.Unlock()
		return errors.New("injected phase update failure")
	}
	store.operations[operation.ID] = operation
	store.phases = append(store.phases, operation.Phase)
	hook := store.onUpdate
	store.mu.Unlock()
	if hook != nil {
		hook(operation)
	}
	return nil
}

func (store *memoryOperationStore) Commit(_ context.Context, operation hostfiles.FileOperation) error {
	store.mu.Lock()
	if store.commitFail > 0 {
		store.commitFail--
		store.mu.Unlock()
		return errors.New("injected reference transaction failure")
	}
	store.mu.Unlock()
	operation.Phase = hostfiles.OperationCompleted
	return store.Update(context.Background(), operation)
}

func (store *memoryOperationStore) Pending(context.Context) ([]hostfiles.FileOperation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var result []hostfiles.FileOperation
	for _, operation := range store.operations {
		if !operation.Phase.Terminal() {
			result = append(result, operation)
		}
	}
	return result, nil
}

func (store *memoryOperationStore) CancelRequested(_ context.Context, id string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.cancelWhen != nil && store.cancelWhen(store.operations[id]), nil
}

func TestCrossFilesystemMoveCopiesVerifiesTrashesAndCommits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(filepath.Join(left, "source", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(right, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source")
	destination := filepath.Join(right, "target", "moved")
	if err := os.WriteFile(filepath.Join(source, "nested", "payload.txt"), []byte("verified payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "cross-move-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-1", source, destination)
	if err != nil {
		t.Fatalf("cross-filesystem move: %v", err)
	}
	if operation.Phase != hostfiles.OperationCompleted || operation.BytesCompleted != operation.BytesTotal {
		t.Fatalf("operation = %#v", operation)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "nested", "payload.txt"))
	if err != nil || string(content) != "verified payload" {
		t.Fatalf("destination content = %q, err = %v", content, err)
	}
	if _, err := os.Stat(operation.TrashPath); err != nil {
		t.Fatalf("source was not retained in filesystem trash: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsRestrictedDescendantBeforeCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(filepath.Join(left, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(left, "source", "link")); err != nil {
		t.Skipf("create link: %v", err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "cross-move-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	engine := hostfiles.NewMoveEngine(manager, store)
	source := filepath.Join(left, "source")
	destination := filepath.Join(right, "moved")

	if _, err := engine.Execute(context.Background(), "move-2", source, destination); err == nil {
		t.Fatal("move with a restricted descendant succeeded")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after rejected preflight: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after rejected preflight: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsAFilesystemRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "root-move-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	engine := hostfiles.NewMoveEngine(manager, newMemoryOperationStore())
	if _, err := engine.Execute(context.Background(), "move-root", left, filepath.Join(right, "moved")); err == nil {
		t.Fatal("cross-filesystem move accepted a filesystem root")
	}
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("filesystem root changed after rejected move: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsHardLinkedFilesBeforeCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(filepath.Join(left, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(left, "source", "first.txt")
	second := filepath.Join(left, "source", "second.txt")
	if err := os.WriteFile(first, []byte("shared inode"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "hard-link-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	engine := hostfiles.NewMoveEngine(manager, newMemoryOperationStore())
	destination := filepath.Join(right, "destination")
	operation, err := engine.Execute(context.Background(), "move-hard-link", filepath.Join(left, "source"), destination)
	if err == nil || operation.Phase != hostfiles.OperationFailed {
		t.Fatalf("hard-linked move operation = %#v, error = %v", operation, err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("source changed after hard-link rejection: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after hard-link rejection: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsInsufficientDestinationSpaceBeforeCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("source remains intact"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "space-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	var checkedPath string
	engine := hostfiles.NewMoveEngineWithOptions(manager, store, hostfiles.MoveEngineOptions{
		RequireSpace: func(path string, minimumBytes uint64) error {
			checkedPath = path
			if minimumBytes <= uint64(len("source remains intact")) {
				t.Fatalf("space check did not include the writable reserve: %d", minimumBytes)
			}
			return errors.New("injected insufficient destination space")
		},
	})

	operation, err := engine.Execute(context.Background(), "move-no-space", source, destination)
	if err == nil || operation.Phase != hostfiles.OperationFailed {
		t.Fatalf("space failure operation = %#v, error = %v", operation, err)
	}
	if hostfiles.ComparisonKey(checkedPath) != hostfiles.ComparisonKey(right) {
		t.Fatalf("space checked at %q, want destination filesystem directory %q", checkedPath, right)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after space preflight failure: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after space preflight failure: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsCopiedContentVerificationFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("content that must be verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "verification-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	var once sync.Once
	store.onUpdate = func(operation hostfiles.FileOperation) {
		if operation.Phase == hostfiles.OperationCopying && operation.BytesTotal > 0 && operation.BytesCompleted == operation.BytesTotal {
			once.Do(func() {
				if err := os.WriteFile(operation.TemporaryPath, []byte("corrupt"), 0o600); err != nil {
					t.Errorf("corrupt copied temporary file: %v", err)
				}
			})
		}
	}
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-bad-checksum", source, destination)
	if err == nil || operation.Phase != hostfiles.OperationFailed {
		t.Fatalf("verification failure operation = %#v, error = %v", operation, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after verification failure: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after verification failure: %v", err)
	}
}

func TestCrossFilesystemMoveRejectsDestinationConflictBeforeCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "conflict-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	engine := hostfiles.NewMoveEngine(manager, store)

	if _, err := engine.Execute(context.Background(), "move-conflict", source, destination); err == nil {
		t.Fatal("move overwrote an existing destination")
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "existing destination" {
		t.Fatalf("existing destination changed: content=%q error=%v", content, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after destination conflict: %v", err)
	}
	if len(store.operations) != 0 {
		t.Fatalf("destination conflict created a persistent operation: %#v", store.operations)
	}
}

func TestCrossFilesystemMoveRecoveryCompletesAfterTargetCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "recovery-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	operation := hostfiles.FileOperation{
		ID: "recover-1", Kind: "cross_filesystem_move", SourcePath: source, SourcePathKey: hostfiles.ComparisonKey(source),
		DestinationPath: destination, DestinationPathKey: hostfiles.ComparisonKey(destination), Phase: hostfiles.OperationTargetCommitted,
	}
	if err := store.Create(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	engine := hostfiles.NewMoveEngine(manager, store)
	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover move: %v", err)
	}
	stored := store.operations[operation.ID]
	if stored.Phase != hostfiles.OperationCompleted {
		t.Fatalf("recovered operation = %#v", stored)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after recovery: %v", err)
	}
	if _, err := os.Stat(stored.TrashPath); err != nil {
		t.Fatalf("recovered source trash missing: %v", err)
	}
}

func TestCrossFilesystemMoveRecoveryRollsBackEveryPreCommitPhase(t *testing.T) {
	t.Parallel()

	for _, phase := range []hostfiles.OperationPhase{
		hostfiles.OperationScanning,
		hostfiles.OperationCopying,
		hostfiles.OperationReadyToCommit,
		hostfiles.OperationCleanupPending,
	} {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
			if err := os.MkdirAll(left, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(right, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(left, "source.txt")
			destination := filepath.Join(right, "destination.txt")
			temporary := filepath.Join(right, ".scriptboard-move-recover-precommit")
			if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(temporary, []byte("partial target"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "precommit-recovery-test", Topology: splitTopology{roots: []string{left, right}}})
			if err != nil {
				t.Fatal(err)
			}
			store := newMemoryOperationStore()
			operation := hostfiles.FileOperation{
				ID: "recover-precommit", Kind: "cross_filesystem_move",
				SourcePath: source, SourcePathKey: hostfiles.ComparisonKey(source),
				DestinationPath: destination, DestinationPathKey: hostfiles.ComparisonKey(destination),
				TemporaryPath: temporary, Phase: phase,
			}
			if err := store.Create(context.Background(), operation); err != nil {
				t.Fatal(err)
			}

			engine := hostfiles.NewMoveEngine(manager, store)
			if err := engine.Recover(context.Background()); err != nil {
				t.Fatalf("recover %s operation: %v", phase, err)
			}
			if recovered := store.operations[operation.ID]; recovered.Phase != hostfiles.OperationRolledBack {
				t.Fatalf("recovered operation = %#v", recovered)
			}
			if _, err := os.Stat(temporary); !os.IsNotExist(err) {
				t.Fatalf("temporary copy survived recovery: %v", err)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source changed during precommit recovery: %v", err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination exists after precommit recovery: %v", err)
			}
		})
	}
}

func TestCrossFilesystemMoveRejectsSourceChangesDuringCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	source := filepath.Join(left, "source")
	destination := filepath.Join(right, "destination")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload.bin"), make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "source-change-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	var once sync.Once
	store.onUpdate = func(operation hostfiles.FileOperation) {
		if operation.Phase == hostfiles.OperationCopying && operation.BytesCompleted > 0 {
			once.Do(func() {
				if err := os.WriteFile(filepath.Join(source, "late.txt"), []byte("created during copy"), 0o600); err != nil {
					t.Errorf("mutate source during copy: %v", err)
				}
			})
		}
	}
	engine := hostfiles.NewMoveEngine(manager, store)

	if _, err := engine.Execute(context.Background(), "move-source-change", source, destination); err == nil {
		t.Fatal("move committed after the source changed during copy")
	}
	if _, err := os.Stat(filepath.Join(source, "late.txt")); err != nil {
		t.Fatalf("changed source was not preserved: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after source-change rollback: %v", err)
	}
}

func TestCrossFilesystemMoveCanCancelWithPartialByteProgress(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "payload.bin")
	destination := filepath.Join(right, "payload.bin")
	if err := os.WriteFile(source, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "cancel-progress-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	store.cancelWhen = func(operation hostfiles.FileOperation) bool {
		return operation.Phase == hostfiles.OperationCopying && operation.BytesCompleted > 0 && operation.BytesCompleted < operation.BytesTotal
	}
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-cancel", source, destination)
	if err == nil {
		t.Fatal("move ignored a cancellation requested during file copy")
	}
	if operation.Phase != hostfiles.OperationCancelled || operation.BytesCompleted <= 0 || operation.BytesCompleted >= operation.BytesTotal {
		t.Fatalf("cancelled operation lacks partial progress: %#v", operation)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after cancellation: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after cancellation: %v", err)
	}
}

func TestCrossFilesystemMoveCanCancelDuringLargeFilePreflight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "payload.bin")
	destination := filepath.Join(right, "payload.bin")
	if err := os.WriteFile(source, make([]byte, 12<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "cancel-scan-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	cancelChecks := 0
	store.cancelWhen = func(operation hostfiles.FileOperation) bool {
		if operation.Phase != hostfiles.OperationScanning {
			return false
		}
		cancelChecks++
		return cancelChecks >= 3
	}
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-cancel-scan", source, destination)
	if err == nil || operation.Phase != hostfiles.OperationCancelled {
		t.Fatalf("scan cancellation operation = %#v, error = %v", operation, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed after preflight cancellation: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after preflight cancellation: %v", err)
	}
}

func TestCrossFilesystemMoveRecoversReferenceCommitFailureAfterSourceTrash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("recoverable"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "commit-recovery-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	store.commitFail = 1
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-commit-recovery", source, destination)
	if err == nil || operation.Phase != hostfiles.OperationSourceTrashed {
		t.Fatalf("commit failure operation = %#v, error = %v", operation, err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("committed target was removed after recoverable database failure: %v", err)
	}
	if _, err := os.Stat(operation.TrashPath); err != nil {
		t.Fatalf("source trash was removed after recoverable database failure: %v", err)
	}

	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover reference commit: %v", err)
	}
	if recovered := store.operations[operation.ID]; recovered.Phase != hostfiles.OperationCompleted {
		t.Fatalf("recovered operation = %#v", recovered)
	}
}

func TestCrossFilesystemMoveRecoversWhenSourceTrashedPhaseWasNotPersisted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(left, "source.txt")
	destination := filepath.Join(right, "destination.txt")
	if err := os.WriteFile(source, []byte("recover phase"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hostfiles.Open(hostfiles.Options{InstanceID: "phase-recovery-test", Topology: splitTopology{roots: []string{left, right}}})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryOperationStore()
	store.updateFail = map[hostfiles.OperationPhase]int{hostfiles.OperationSourceTrashed: 1}
	engine := hostfiles.NewMoveEngine(manager, store)

	operation, err := engine.Execute(context.Background(), "move-phase-recovery", source, destination)
	if err == nil || operation.Phase != hostfiles.OperationSourceTrashed {
		t.Fatalf("phase persistence failure operation = %#v, error = %v", operation, err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("committed destination was not preserved: %v", err)
	}
	if _, err := os.Stat(operation.TrashPath); err != nil {
		t.Fatalf("source trash was not preserved: %v", err)
	}

	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover unpersisted source-trash phase: %v", err)
	}
	if recovered := store.operations[operation.ID]; recovered.Phase != hostfiles.OperationCompleted {
		t.Fatalf("recovered operation = %#v", recovered)
	}
}
