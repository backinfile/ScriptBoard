package hostfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scriptboard/internal/diskspace"
)

type OperationPhase string

const (
	OperationScanning        OperationPhase = "scanning"
	OperationCopying         OperationPhase = "copying"
	OperationReadyToCommit   OperationPhase = "ready_to_commit"
	OperationCleanupPending  OperationPhase = "cleanup_pending"
	OperationTargetCommitted OperationPhase = "target_committed"
	OperationSourceTrashed   OperationPhase = "source_trashed"
	OperationCompleted       OperationPhase = "completed"
	OperationRolledBack      OperationPhase = "rolled_back"
	OperationCancelled       OperationPhase = "cancelled"
	OperationFailed          OperationPhase = "failed"
)

func (phase OperationPhase) Terminal() bool {
	return phase == OperationCompleted || phase == OperationRolledBack || phase == OperationCancelled || phase == OperationFailed
}

type FileOperation struct {
	ID                 string         `json:"id"`
	Kind               string         `json:"kind"`
	SourcePath         string         `json:"sourcePath"`
	SourcePathKey      string         `json:"-"`
	DestinationPath    string         `json:"destinationPath"`
	DestinationPathKey string         `json:"-"`
	TemporaryPath      string         `json:"-"`
	TrashPath          string         `json:"-"`
	Phase              OperationPhase `json:"phase"`
	BytesTotal         int64          `json:"bytesTotal"`
	BytesCompleted     int64          `json:"bytesCompleted"`
	VerificationDigest string         `json:"-"`
	Error              string         `json:"error,omitempty"`
	CancelRequested    bool           `json:"cancelRequested"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

type OperationStore interface {
	Create(context.Context, FileOperation) error
	Update(context.Context, FileOperation) error
	Commit(context.Context, FileOperation) error
	Pending(context.Context) ([]FileOperation, error)
	CancelRequested(context.Context, string) (bool, error)
}

type MoveEngine struct {
	manager      *Manager
	store        OperationStore
	requireSpace func(string, uint64) error
}

func NewMoveEngine(manager *Manager, store OperationStore) *MoveEngine {
	return NewMoveEngineWithOptions(manager, store, MoveEngineOptions{})
}

type MoveEngineOptions struct {
	RequireSpace func(path string, minimumBytes uint64) error
}

func NewMoveEngineWithOptions(manager *Manager, store OperationStore, options MoveEngineOptions) *MoveEngine {
	requireSpace := options.RequireSpace
	if requireSpace == nil {
		requireSpace = diskspace.Require
	}
	return &MoveEngine{manager: manager, store: store, requireSpace: requireSpace}
}

// Recover completes or rolls back every persisted non-terminal move. Work
// before the target commit is rolled back; work after it is completed so the
// source, target, trash, and database references converge after a restart.
func (engine *MoveEngine) Recover(ctx context.Context) error {
	if engine == nil || engine.manager == nil || engine.store == nil {
		return errors.New("cross-filesystem move engine is not configured")
	}
	operations, err := engine.store.Pending(ctx)
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if err := engine.recoverOne(ctx, operation); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover file operation %s: %w", operation.ID, err))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (engine *MoveEngine) recoverOne(ctx context.Context, operation FileOperation) error {
	leaseID := "file-operation:" + operation.ID
	if err := engine.manager.AcquireLease(leaseID, operation.SourcePath, operation.DestinationPath); err != nil {
		return err
	}
	defer engine.manager.ReleaseLease(leaseID)
	update := func(phase OperationPhase, errorText string) error {
		operation.Phase = phase
		operation.Error = errorText
		operation.UpdatedAt = time.Now().UTC()
		return engine.store.Update(ctx, operation)
	}

	switch operation.Phase {
	case OperationScanning, OperationCopying, OperationCleanupPending:
		if err := removeMovePath(operation.TemporaryPath); err != nil {
			return err
		}
		return update(OperationRolledBack, "rolled back during startup recovery before target commit")
	case OperationReadyToCommit:
		if _, err := os.Lstat(operation.TemporaryPath); err == nil {
			if err := removeMovePath(operation.TemporaryPath); err != nil {
				return err
			}
			return update(OperationRolledBack, "rolled back ready copy during startup recovery")
		}
		if _, err := os.Lstat(operation.DestinationPath); err != nil {
			return update(OperationRolledBack, "target was not committed before restart")
		}
		if operation.VerificationDigest != "" {
			digest, err := digestTree(operation.DestinationPath)
			if err != nil || digest != operation.VerificationDigest {
				return update(OperationFailed, "committed target could not be verified during recovery")
			}
		}
		if err := update(OperationTargetCommitted, ""); err != nil {
			return err
		}
		operation.Phase = OperationTargetCommitted
	}

	if operation.Phase == OperationTargetCommitted {
		if _, err := os.Lstat(operation.SourcePath); err == nil {
			if err := verifyTreeDigest(operation.SourcePath, operation.VerificationDigest); err != nil {
				return engine.rollbackRecoveredTarget(ctx, operation, fmt.Errorf("source changed before recovery could trash it: %w", err))
			}
			trashed, err := engine.manager.MoveToTrash(operation.SourcePath, operation.ID)
			if err != nil {
				return err
			}
			operation.TrashPath = trashed.StoredPath
		} else if os.IsNotExist(err) {
			if _, trashErr := engine.manager.resolveTrashEntry(operation.TrashPath); trashErr != nil {
				return update(OperationFailed, "source and owned trash entry are both unavailable")
			}
		} else {
			return err
		}
		if err := update(OperationSourceTrashed, ""); err != nil {
			return err
		}
		operation.Phase = OperationSourceTrashed
	}

	if operation.Phase == OperationSourceTrashed {
		if err := verifyTreeDigest(operation.DestinationPath, operation.VerificationDigest); err != nil {
			return update(OperationFailed, "committed target could not be verified during recovery: "+err.Error())
		}
		trashPath, err := engine.manager.resolveTrashEntry(operation.TrashPath)
		if err != nil {
			return update(OperationFailed, "owned source trash entry is unavailable during recovery")
		}
		if err := verifyTreeDigest(trashPath, operation.VerificationDigest); err != nil {
			return update(OperationFailed, "owned source trash entry could not be verified during recovery: "+err.Error())
		}
		operation.Phase = OperationCompleted
		operation.Error = ""
		operation.UpdatedAt = time.Now().UTC()
		return engine.store.Commit(ctx, operation)
	}
	return nil
}

type moveManifestEntry struct {
	relative string
	mode     os.FileMode
	size     int64
	modified time.Time
	digest   string
	dir      bool
}

func (engine *MoveEngine) Execute(ctx context.Context, id, source, destination string) (FileOperation, error) {
	return engine.execute(ctx, id, source, destination, nil)
}

// ExecuteWithStart is Execute with a callback invoked immediately after the
// durable operation row exists. It lets HTTP callers publish a status URL
// without racing the operation's first persisted phase.
func (engine *MoveEngine) ExecuteWithStart(ctx context.Context, id, source, destination string, started func(FileOperation)) (FileOperation, error) {
	return engine.execute(ctx, id, source, destination, started)
}

func (engine *MoveEngine) execute(ctx context.Context, id, source, destination string, started func(FileOperation)) (FileOperation, error) {
	if engine == nil || engine.manager == nil || engine.store == nil {
		return FileOperation{}, errors.New("cross-filesystem move engine is not configured")
	}
	if err := ValidateName(id); err != nil {
		return FileOperation{}, fmt.Errorf("invalid file operation ID: %w", err)
	}
	sourcePath, sourceInfo, err := engine.manager.resolveEntry(source)
	if err != nil {
		return FileOperation{}, err
	}
	if err := engine.manager.ensureMutationAllowed(sourcePath); err != nil {
		return FileOperation{}, err
	}
	destinationPath, err := engine.manager.CanonicalDestination(destination)
	if err != nil {
		return FileOperation{}, err
	}
	if _, err := os.Lstat(destinationPath); err == nil {
		return FileOperation{}, errors.New("move destination already exists")
	} else if !os.IsNotExist(err) {
		return FileOperation{}, err
	}
	sourceRoot, err := engine.manager.topology.FilesystemRoot(sourcePath)
	if err != nil {
		return FileOperation{}, err
	}
	if ComparisonKey(sourceRoot) == ComparisonKey(sourcePath) {
		return FileOperation{}, errors.New("a filesystem root cannot be moved")
	}
	destinationRoot, err := engine.manager.topology.FilesystemRoot(filepath.Dir(destinationPath))
	if err != nil {
		return FileOperation{}, err
	}
	if ComparisonKey(sourceRoot) == ComparisonKey(destinationRoot) {
		return FileOperation{}, errors.New("cross-filesystem move requires different source and destination filesystems")
	}
	leaseID := "file-operation:" + id
	if err := engine.manager.AcquireLease(leaseID, sourcePath, destinationPath); err != nil {
		return FileOperation{}, err
	}
	defer engine.manager.ReleaseLease(leaseID)

	now := time.Now().UTC()
	operation := FileOperation{
		ID: id, Kind: "cross_filesystem_move", SourcePath: sourcePath, SourcePathKey: ComparisonKey(sourcePath),
		DestinationPath: destinationPath, DestinationPathKey: ComparisonKey(destinationPath), Phase: OperationScanning,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := engine.store.Create(ctx, operation); err != nil {
		return operation, err
	}
	if started != nil {
		started(operation)
	}

	manifest, total, digest, err := engine.scan(ctx, operation, sourcePath, sourceRoot, sourceInfo)
	if err != nil {
		return engine.fail(ctx, operation, err, errors.Is(err, context.Canceled))
	}
	if total < 0 || uint64(total) > ^uint64(0)-diskspace.MinimumWritableBytes {
		return engine.fail(ctx, operation, errors.New("move size exceeds supported range"), false)
	}
	if err := engine.requireSpace(filepath.Dir(destinationPath), uint64(total)+diskspace.MinimumWritableBytes); err != nil {
		return engine.fail(ctx, operation, err, false)
	}
	trashRoot, err := engine.manager.ensureTrashRoot(sourcePath)
	if err != nil {
		return engine.fail(ctx, operation, err, false)
	}
	operation.TemporaryPath = filepath.Join(filepath.Dir(destinationPath), ".scriptboard-move-"+id)
	operation.TrashPath = filepath.Join(trashRoot, id)
	operation.BytesTotal = total
	operation.VerificationDigest = digest
	operation.Phase = OperationCopying
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Update(ctx, operation); err != nil {
		return operation, err
	}
	if _, err := os.Lstat(operation.TemporaryPath); err == nil {
		return engine.fail(ctx, operation, errors.New("move temporary path already exists"), false)
	} else if !os.IsNotExist(err) {
		return engine.fail(ctx, operation, err, false)
	}
	defer func() { _ = removeMovePath(operation.TemporaryPath) }()
	if err := engine.copy(ctx, &operation, manifest); err != nil {
		cancelled := errors.Is(err, context.Canceled)
		return engine.fail(ctx, operation, err, cancelled)
	}
	if err := verifyTreeDigest(sourcePath, operation.VerificationDigest); err != nil {
		return engine.fail(ctx, operation, fmt.Errorf("source changed during move copy: %w", err), false)
	}
	operation.Phase = OperationReadyToCommit
	operation.BytesCompleted = operation.BytesTotal
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Update(ctx, operation); err != nil {
		return operation, err
	}
	if cancelled, err := engine.cancelled(ctx, operation.ID); err != nil {
		return engine.fail(ctx, operation, err, false)
	} else if cancelled {
		return engine.fail(ctx, operation, context.Canceled, true)
	}
	if _, err := os.Lstat(destinationPath); err == nil {
		return engine.fail(ctx, operation, errors.New("move destination appeared before commit"), false)
	} else if !os.IsNotExist(err) {
		return engine.fail(ctx, operation, err, false)
	}
	if err := os.Rename(operation.TemporaryPath, destinationPath); err != nil {
		return engine.fail(ctx, operation, fmt.Errorf("commit move destination: %w", err), false)
	}
	operation.Phase = OperationTargetCommitted
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Update(ctx, operation); err != nil {
		_ = removeMovePath(destinationPath)
		return operation, err
	}
	if err := verifyTreeDigest(sourcePath, operation.VerificationDigest); err != nil {
		return engine.rollbackCommittedTarget(ctx, operation, fmt.Errorf("source changed before it could be moved to trash: %w", err))
	}
	trashed, err := engine.manager.MoveToTrash(sourcePath, id)
	if err != nil {
		_ = removeMovePath(destinationPath)
		return engine.fail(ctx, operation, fmt.Errorf("trash move source: %w", err), false)
	}
	operation.TrashPath = trashed.StoredPath
	operation.Phase = OperationSourceTrashed
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Update(ctx, operation); err != nil {
		operation.Error = "source was moved to trash; phase persistence is pending recovery: " + err.Error()
		operation.UpdatedAt = time.Now().UTC()
		_ = engine.store.Update(context.WithoutCancel(ctx), operation)
		return operation, err
	}
	operation.Phase = OperationCompleted
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Commit(ctx, operation); err != nil {
		operation.Phase = OperationSourceTrashed
		operation.Error = "reference update is pending recovery: " + err.Error()
		operation.UpdatedAt = time.Now().UTC()
		_ = engine.store.Update(context.WithoutCancel(ctx), operation)
		return operation, fmt.Errorf("commit moved file references: %w", err)
	}
	return operation, nil
}

func (engine *MoveEngine) scan(ctx context.Context, operation FileOperation, source, sourceRoot string, sourceInfo os.FileInfo) ([]moveManifestEntry, int64, string, error) {
	var entries []moveManifestEntry
	var total int64
	var walk func(string, string, os.FileInfo) error
	walk = func(path, relative string, info os.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cancelled, err := engine.cancelled(ctx, operation.ID); err != nil {
			return err
		} else if cancelled {
			return context.Canceled
		}
		if restrictedEntry(path, info) || (!info.IsDir() && !info.Mode().IsRegular()) || reservedPath(path) {
			return fmt.Errorf("restricted or special entry cannot be copied: %s", path)
		}
		entryRoot, err := engine.manager.topology.FilesystemRoot(path)
		if err != nil || ComparisonKey(entryRoot) != ComparisonKey(sourceRoot) {
			return fmt.Errorf("nested filesystem boundary cannot be copied losslessly: %s", path)
		}
		entry := moveManifestEntry{relative: relative, mode: info.Mode(), modified: info.ModTime(), dir: info.IsDir()}
		if info.Mode().IsRegular() {
			multipleLinks, err := regularFileHasMultipleLinks(path, info)
			if err != nil {
				return fmt.Errorf("inspect hard-link metadata for %s: %w", path, err)
			}
			if multipleLinks {
				return fmt.Errorf("hard-linked file cannot be copied losslessly: %s", path)
			}
			entry.size = info.Size()
			digest, err := engine.digestRegularFile(ctx, operation.ID, path, info)
			if err != nil {
				return err
			}
			entry.digest = digest
			if info.Size() > 0 && total > int64(^uint64(0)>>1)-info.Size() {
				return errors.New("move byte count overflow")
			}
			total += info.Size()
		}
		entries = append(entries, entry)
		if !info.IsDir() {
			return nil
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			childPath := filepath.Join(path, child.Name())
			childInfo, err := os.Lstat(childPath)
			if err != nil {
				return err
			}
			childRelative := child.Name()
			if relative != "." {
				childRelative = filepath.Join(relative, child.Name())
			}
			if err := walk(childPath, childRelative, childInfo); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(source, ".", sourceInfo); err != nil {
		return nil, 0, "", err
	}
	return entries, total, manifestDigest(entries), nil
}

func (engine *MoveEngine) digestRegularFile(ctx context.Context, operationID, path string, expected os.FileInfo) (string, error) {
	return digestRegularFileChecked(path, expected, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cancelled, err := engine.cancelled(ctx, operationID); err != nil {
			return err
		} else if cancelled {
			return context.Canceled
		}
		return nil
	})
}

func (engine *MoveEngine) copy(ctx context.Context, operation *FileOperation, manifest []moveManifestEntry) error {
	for _, entry := range manifest {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cancelled, err := engine.cancelled(ctx, operation.ID); err != nil {
			return err
		} else if cancelled {
			return context.Canceled
		}
		source := operation.SourcePath
		target := operation.TemporaryPath
		if entry.relative != "." {
			source = filepath.Join(source, entry.relative)
			target = filepath.Join(target, entry.relative)
		}
		if entry.dir {
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := engine.copyVerifiedRegular(ctx, operation, source, target, entry); err != nil {
			return err
		}
		operation.UpdatedAt = time.Now().UTC()
		if err := engine.store.Update(ctx, *operation); err != nil {
			return err
		}
	}
	for index := len(manifest) - 1; index >= 0; index-- {
		entry := manifest[index]
		target := operation.TemporaryPath
		source := operation.SourcePath
		if entry.relative != "." {
			target = filepath.Join(target, entry.relative)
			source = filepath.Join(source, entry.relative)
		}
		if err := os.Chmod(target, entry.mode.Perm()); err != nil {
			return err
		}
		if err := os.Chtimes(target, entry.modified, entry.modified); err != nil {
			return err
		}
		if err := copyPlatformMetadata(source, target); err != nil {
			return err
		}
		if err := verifyCopiedMetadata(source, target, entry); err != nil {
			return err
		}
	}
	verified, err := engine.digestTree(ctx, operation.ID, operation.TemporaryPath)
	if err != nil {
		return err
	}
	if verified != operation.VerificationDigest {
		return errors.New("copied tree verification failed")
	}
	return nil
}

func (engine *MoveEngine) copyVerifiedRegular(ctx context.Context, operation *FileOperation, source, target string, expected moveManifestEntry) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	info, err := sourceFile.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.size || !info.ModTime().Equal(expected.modified) {
		return errors.New("source changed after move preflight")
	}
	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	buffer := make([]byte, 4<<20)
	var written int64
	var copyErr error
	for {
		if err := ctx.Err(); err != nil {
			copyErr = err
			break
		}
		if cancelled, err := engine.cancelled(ctx, operation.ID); err != nil {
			copyErr = err
			break
		} else if cancelled {
			copyErr = context.Canceled
			break
		}
		read, readErr := sourceFile.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			writeCount, writeErr := targetFile.Write(chunk)
			if writeErr == nil && writeCount != read {
				writeErr = io.ErrShortWrite
			}
			if writeErr != nil {
				copyErr = writeErr
				break
			}
			_, _ = hash.Write(chunk)
			written += int64(read)
			operation.BytesCompleted += int64(read)
			operation.UpdatedAt = time.Now().UTC()
			if err := engine.store.Update(ctx, *operation); err != nil {
				copyErr = err
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			copyErr = readErr
			break
		}
	}
	if copyErr == nil {
		copyErr = targetFile.Sync()
	}
	if closeErr := targetFile.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if written != expected.size || hex.EncodeToString(hash.Sum(nil)) != expected.digest {
		return errors.New("source changed while it was copied")
	}
	return nil
}

func verifyTreeDigest(path, expected string) error {
	if expected == "" {
		return nil
	}
	actual, err := digestTree(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("tree digest does not match move preflight")
	}
	return nil
}

func (engine *MoveEngine) rollbackCommittedTarget(ctx context.Context, operation FileOperation, cause error) (FileOperation, error) {
	if err := verifyTreeDigest(operation.DestinationPath, operation.VerificationDigest); err != nil {
		operation.Phase = OperationFailed
		operation.Error = cause.Error() + "; committed target was left in place because it changed: " + err.Error()
		operation.UpdatedAt = time.Now().UTC()
		_ = engine.store.Update(context.WithoutCancel(ctx), operation)
		return operation, errors.New(operation.Error)
	}
	if err := removeMovePath(operation.DestinationPath); err != nil {
		operation.Phase = OperationFailed
		operation.Error = cause.Error() + "; committed target could not be rolled back: " + err.Error()
		operation.UpdatedAt = time.Now().UTC()
		_ = engine.store.Update(context.WithoutCancel(ctx), operation)
		return operation, errors.New(operation.Error)
	}
	operation.Phase = OperationRolledBack
	operation.Error = cause.Error()
	operation.UpdatedAt = time.Now().UTC()
	if err := engine.store.Update(context.WithoutCancel(ctx), operation); err != nil {
		return operation, errors.Join(cause, err)
	}
	return operation, cause
}

func (engine *MoveEngine) rollbackRecoveredTarget(ctx context.Context, operation FileOperation, cause error) error {
	rolledBack, err := engine.rollbackCommittedTarget(ctx, operation, cause)
	if rolledBack.Phase == OperationRolledBack {
		return nil
	}
	return err
}

func digestRegularFile(path string, expected os.FileInfo) (string, error) {
	return digestRegularFileChecked(path, expected, nil)
}

func digestRegularFileChecked(path string, expected os.FileInfo, check func() error) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return "", errors.New("file changed during move preflight")
	}
	hash := sha256.New()
	buffer := make([]byte, 4<<20)
	for {
		if check != nil {
			if err := check(); err != nil {
				return "", err
			}
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func manifestDigest(entries []moveManifestEntry) string {
	hash := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%s\n", filepath.ToSlash(entry.relative), entry.mode.Perm(), entry.size, entry.digest)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func digestTree(root string) (string, error) {
	return digestTreeChecked(root, nil)
}

func (engine *MoveEngine) digestTree(ctx context.Context, operationID, root string) (string, error) {
	return digestTreeChecked(root, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cancelled, err := engine.cancelled(ctx, operationID)
		if err != nil {
			return err
		}
		if cancelled {
			return context.Canceled
		}
		return nil
	})
}

func digestTreeChecked(root string, check func() error) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	var entries []moveManifestEntry
	var walk func(string, string, os.FileInfo) error
	walk = func(path, relative string, info os.FileInfo) error {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		if restrictedEntry(path, info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("copied tree contains a restricted entry")
		}
		entry := moveManifestEntry{relative: relative, mode: info.Mode(), dir: info.IsDir()}
		if info.Mode().IsRegular() {
			entry.size = info.Size()
			digest, err := digestRegularFileChecked(path, info, check)
			if err != nil {
				return err
			}
			entry.digest = digest
		}
		entries = append(entries, entry)
		if !info.IsDir() {
			return nil
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			childPath := filepath.Join(path, child.Name())
			childInfo, err := os.Lstat(childPath)
			if err != nil {
				return err
			}
			childRelative := child.Name()
			if relative != "." {
				childRelative = filepath.Join(relative, child.Name())
			}
			if err := walk(childPath, childRelative, childInfo); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, ".", info); err != nil {
		return "", err
	}
	return manifestDigest(entries), nil
}

func (engine *MoveEngine) cancelled(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, err
	}
	return engine.store.CancelRequested(ctx, id)
}

func (engine *MoveEngine) fail(ctx context.Context, operation FileOperation, cause error, cancelled bool) (FileOperation, error) {
	if operation.TemporaryPath != "" {
		if cleanupErr := removeMovePath(operation.TemporaryPath); cleanupErr != nil {
			operation.Error = cause.Error() + "; temporary copy cleanup is pending recovery: " + cleanupErr.Error()
			operation.Phase = OperationCleanupPending
			operation.UpdatedAt = time.Now().UTC()
			_ = engine.store.Update(context.WithoutCancel(ctx), operation)
			return operation, errors.Join(cause, cleanupErr)
		}
	}
	operation.Error = cause.Error()
	operation.Phase = OperationFailed
	if cancelled {
		operation.Phase = OperationCancelled
	}
	operation.UpdatedAt = time.Now().UTC()
	_ = engine.store.Update(context.WithoutCancel(ctx), operation)
	return operation, cause
}

func removeMovePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && !restrictedEntry(path, info) {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}
