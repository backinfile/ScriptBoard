package hostfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/diskspace"
	"scriptboard/internal/pathsecurity"
	"scriptboard/internal/privatepath"
)

const trashDirectoryName = ".scriptboard-trash"
const trashMarkerName = ".scriptboard-owner"

var trashRootMarker = []byte("scriptboard-trash-root-v2\n")

type Trashed struct {
	OriginalPath string
	StoredPath   string
	StoredName   string
	Size         int64
	Directory    bool
}

// UploadBatchInput describes one file in an all-or-nothing upload. The caller
// retains ownership of Source; UploadBatch reads it but does not close it.
type UploadBatchInput struct {
	Name       string
	Source     io.Reader
	MaxBytes   int64
	StoredName string
}

type UploadBatchResult struct {
	Name                  string
	Path                  string
	Trashed               *Trashed
	QuickRunsSynchronized int64
	ScriptSHA256          string
}

type Script struct {
	Path      string
	Directory string
	Digest    string
	Info      os.FileInfo
}

type PreparedDirectory struct {
	Path string
	Info os.FileInfo
}

// PrepareAppendFile canonicalizes a regular text file, or a new file below an
// accessible directory, using the same protected-path boundary as other host
// mutations.
func (m *Manager) PrepareAppendFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("host path must be absolute")
	}
	target := filepath.Clean(path)
	_, err := os.Lstat(target)
	if err == nil {
		canonical, resolvedInfo, resolveErr := m.resolveEntry(target)
		if resolveErr != nil {
			return "", resolveErr
		}
		if !resolvedInfo.Mode().IsRegular() || restrictedEntry(canonical, resolvedInfo) {
			return "", fmt.Errorf("append target is not a regular file")
		}
		if err := m.ensureMutationAllowed(canonical); err != nil {
			return "", err
		}
		return canonical, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect append target: %w", err)
	}
	return m.CanonicalDestination(target)
}

// AppendText appends one already-bounded UTF-8 record to a host file. Existing
// links and non-regular files are rejected, and new files are created without
// replacing a path that appeared after validation.
func (m *Manager) AppendText(path, record string) error {
	m.appendMu.Lock()
	defer m.appendMu.Unlock()
	return m.appendText(path, record)
}

// AppendRotatingText appends a record and rotates the target before the write
// when it would exceed maxBytes. Archives use the stable file.1, file.2, ...
// convention so readers can keep following the configured current path.
func (m *Manager) AppendRotatingText(path, record string, maxBytes int64, backups int) error {
	if maxBytes < 1<<20 || maxBytes > 1<<30 || backups < 1 || backups > 100 {
		return fmt.Errorf("invalid log rotation policy")
	}
	m.appendMu.Lock()
	defer m.appendMu.Unlock()
	target, err := m.PrepareAppendFile(path)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(target); statErr == nil && info.Size()+int64(len(record)) > maxBytes {
		if err := m.rotateAppendTarget(target, backups); err != nil {
			return err
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect append target: %w", statErr)
	}
	return m.appendText(target, record)
}

func (m *Manager) rotateAppendTarget(target string, backups int) error {
	if err := os.Remove(target + "." + fmt.Sprint(backups)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oldest log archive: %w", err)
	}
	for generation := backups - 1; generation >= 1; generation-- {
		source := target + "." + fmt.Sprint(generation)
		destination := target + "." + fmt.Sprint(generation+1)
		if err := os.Rename(source, destination); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("advance log archive: %w", err)
		}
	}
	if err := os.Rename(target, target+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return nil
}

func (m *Manager) appendText(path, record string) error {
	if !utf8.ValidString(record) || strings.IndexByte(record, 0) >= 0 {
		return fmt.Errorf("append record is not safe UTF-8 text")
	}
	target, err := m.PrepareAppendFile(path)
	if err != nil {
		return err
	}
	if err := diskspace.Require(filepath.Dir(target), diskspace.MinimumWritableBytes); err != nil {
		return err
	}

	existing, existingErr := os.Lstat(target)
	flags := os.O_WRONLY | os.O_APPEND
	if os.IsNotExist(existingErr) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if existingErr != nil {
		return fmt.Errorf("inspect append target: %w", existingErr)
	}
	file, err := os.OpenFile(target, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open append target: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || (existingErr == nil && !os.SameFile(existing, opened)) {
		return fmt.Errorf("append target changed while it was being opened")
	}
	if _, err := io.WriteString(file, record); err != nil {
		return fmt.Errorf("append text: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync appended text: %w", err)
	}
	return nil
}

func (m *Manager) PrepareDirectory(path string) (PreparedDirectory, error) {
	target, err := m.resolveDirectory(path)
	if err != nil {
		return PreparedDirectory{}, err
	}
	// A working directory is part of an execution request. Executing from a
	// protected path, or from an ancestor that contains one, would let a script
	// reach application-private data through a relative path.
	if err := m.ensureMutationAllowed(target); err != nil {
		return PreparedDirectory{}, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return PreparedDirectory{}, err
	}
	return PreparedDirectory{Path: target, Info: info}, nil
}

func (m *Manager) Upload(directory, name string, source io.Reader, maxBytes int64, replace bool, storedName string) (*Trashed, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	parent, err := m.resolveDirectory(directory)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(parent, name)
	if err := m.ensureMutationAllowed(target); err != nil {
		return nil, err
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return nil, err
	}
	existing, existingErr := os.Lstat(target)
	if existingErr == nil {
		if !replace {
			return nil, fmt.Errorf("an entry with the same name already exists")
		}
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("only a regular file can be replaced")
		}
	} else if !os.IsNotExist(existingErr) {
		return nil, fmt.Errorf("inspect upload target: %w", existingErr)
	}

	temporary, err := os.CreateTemp(parent, ".scriptboard-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create upload temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("set upload permissions: %w", err)
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(source, maxBytes+1))
	if copyErr == nil && written > maxBytes {
		copyErr = fmt.Errorf("file exceeds %d byte limit", maxBytes)
	}
	if syncErr := temporary.Sync(); copyErr == nil && syncErr != nil {
		copyErr = syncErr
	}
	if closeErr := temporary.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return nil, fmt.Errorf("write upload: %w", copyErr)
	}
	var trashed *Trashed
	if existingErr == nil {
		old, err := m.MoveToTrash(target, storedName)
		if err != nil {
			return nil, err
		}
		trashed = &old
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		if trashed != nil {
			_ = m.RestoreFromTrash(trashed.StoredPath, trashed.OriginalPath)
		}
		return nil, fmt.Errorf("commit upload: %w", err)
	}
	return trashed, nil
}

// UploadBatch stages and syncs every input before changing any destination.
// If a commit step fails, every destination already changed by this call is
// restored before the error is returned.
func (m *Manager) UploadBatch(directory string, inputs []UploadBatchInput, replace bool) ([]UploadBatchResult, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("at least one upload is required")
	}
	parent, err := m.resolveDirectory(directory)
	if err != nil {
		return nil, err
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return nil, err
	}
	leaseID := fmt.Sprintf("batch-upload:%p:%d", &inputs, time.Now().UnixNano())
	if err := m.AcquireLease(leaseID, parent); err != nil {
		return nil, err
	}
	defer m.ReleaseLease(leaseID)
	type stagedUpload struct {
		input     UploadBatchInput
		target    string
		temporary string
		exists    bool
	}
	staged := make([]stagedUpload, 0, len(inputs))
	defer func() {
		for _, item := range staged {
			_ = os.Remove(item.temporary)
		}
	}()
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if err := ValidateName(input.Name); err != nil {
			return nil, err
		}
		if input.Source == nil || input.MaxBytes <= 0 {
			return nil, fmt.Errorf("upload %q has no readable content or size limit", input.Name)
		}
		key := ComparisonKey(input.Name)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("upload batch contains duplicate name %q", input.Name)
		}
		seen[key] = struct{}{}
		target := filepath.Join(parent, input.Name)
		if err := m.ensureMutationAllowed(target); err != nil {
			return nil, err
		}
		existing, existingErr := os.Lstat(target)
		exists := existingErr == nil
		if exists {
			if !replace {
				return nil, fmt.Errorf("an entry with the same name already exists: %s", input.Name)
			}
			if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("only a regular file can be replaced: %s", input.Name)
			}
			if err := ValidateName(input.StoredName); err != nil {
				return nil, fmt.Errorf("invalid trash entry ID for %s: %w", input.Name, err)
			}
		} else if !os.IsNotExist(existingErr) {
			return nil, fmt.Errorf("inspect upload target %s: %w", input.Name, existingErr)
		}
		temporary, createErr := os.CreateTemp(parent, ".scriptboard-upload-*")
		if createErr != nil {
			return nil, fmt.Errorf("create upload temporary file for %s: %w", input.Name, createErr)
		}
		temporaryPath := temporary.Name()
		staged = append(staged, stagedUpload{input: input, target: target, temporary: temporaryPath, exists: exists})
		if chmodErr := temporary.Chmod(0o644); chmodErr != nil {
			_ = temporary.Close()
			return nil, fmt.Errorf("set upload permissions for %s: %w", input.Name, chmodErr)
		}
		written, copyErr := io.Copy(temporary, io.LimitReader(input.Source, input.MaxBytes+1))
		if copyErr == nil && written > input.MaxBytes {
			copyErr = fmt.Errorf("file exceeds %d byte limit", input.MaxBytes)
		}
		if syncErr := temporary.Sync(); copyErr == nil && syncErr != nil {
			copyErr = syncErr
		}
		if closeErr := temporary.Close(); copyErr == nil && closeErr != nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return nil, fmt.Errorf("stage upload %s: %w", input.Name, copyErr)
		}
	}

	results := make([]UploadBatchResult, 0, len(staged))
	rollback := func(cause error) error { return errors.Join(cause, m.RollbackUploadBatch(results)) }
	for _, item := range staged {
		var trashed *Trashed
		if item.exists {
			old, trashErr := m.MoveToTrash(item.target, item.input.StoredName)
			if trashErr != nil {
				return nil, rollback(fmt.Errorf("prepare replacement %s: %w", item.input.Name, trashErr))
			}
			trashed = &old
		}
		if renameErr := os.Rename(item.temporary, item.target); renameErr != nil {
			if trashed != nil {
				_ = m.RestoreFromTrash(trashed.StoredPath, trashed.OriginalPath)
			}
			return nil, rollback(fmt.Errorf("commit upload %s: %w", item.input.Name, renameErr))
		}
		results = append(results, UploadBatchResult{Name: item.input.Name, Path: item.target, Trashed: trashed})
	}
	return results, nil
}

// RollbackUploadBatch restores a completed batch in reverse commit order. It
// is used when a caller cannot durably record the replacement metadata.
func (m *Manager) RollbackUploadBatch(results []UploadBatchResult) error {
	var rollbackErr error
	for index := len(results) - 1; index >= 0; index-- {
		result := results[index]
		if removeErr := os.Remove(result.Path); removeErr != nil && !os.IsNotExist(removeErr) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove committed upload %s: %w", result.Name, removeErr))
			continue
		}
		if result.Trashed != nil {
			if restoreErr := m.RestoreFromTrash(result.Trashed.StoredPath, result.Trashed.OriginalPath); restoreErr != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore replaced file %s: %w", result.Name, restoreErr))
			}
		}
	}
	return rollbackErr
}

func (m *Manager) PrepareScript(path string) (Script, error) {
	file, info, err := m.OpenRegular(path)
	if err != nil {
		return Script{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Script{}, fmt.Errorf("digest script: %w", err)
	}
	return Script{Path: file.Name(), Directory: filepath.Dir(file.Name()), Digest: hex.EncodeToString(hash.Sum(nil)), Info: info}, nil
}

func (m *Manager) RemoveRegular(path string) error {
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return err
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("only a regular file can be removed")
	}
	return os.Remove(target)
}

func (m *Manager) Info(path string) (os.FileInfo, error) {
	_, info, err := m.resolveEntry(path)
	return info, err
}

func (m *Manager) ToggleOwnerExecute(path string) (bool, error) {
	target, info, err := m.resolveEntry(path)
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("only a regular file owner execute bit can be changed")
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return false, err
	}
	if err := diskspace.Require(filepath.Dir(target), diskspace.MinimumWritableBytes); err != nil {
		return false, err
	}
	mode := info.Mode().Perm()
	enabled := mode&0o100 == 0
	if enabled {
		mode |= 0o100
	} else {
		mode &^= 0o100
	}
	if err := os.Chmod(target, mode); err != nil {
		return false, err
	}
	return enabled, nil
}

func (m *Manager) MoveToTrash(path, storedName string) (Trashed, error) {
	if err := ValidateName(storedName); err != nil {
		return Trashed{}, fmt.Errorf("invalid trash entry ID: %w", err)
	}
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return Trashed{}, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Trashed{}, fmt.Errorf("restricted or special entry cannot be deleted")
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return Trashed{}, err
	}
	root, err := m.topology.FilesystemRoot(target)
	if err != nil {
		return Trashed{}, fmt.Errorf("resolve filesystem root: %w", err)
	}
	if ComparisonKey(root) == ComparisonKey(target) {
		return Trashed{}, fmt.Errorf("a filesystem root cannot be deleted")
	}
	trashRoot, err := m.ensureTrashRoot(target)
	if err != nil {
		return Trashed{}, err
	}
	storedPath := filepath.Join(trashRoot, storedName)
	if _, err := os.Lstat(storedPath); err == nil {
		return Trashed{}, fmt.Errorf("trash entry already exists")
	} else if !os.IsNotExist(err) {
		return Trashed{}, err
	}
	if err := os.Rename(target, storedPath); err != nil {
		return Trashed{}, fmt.Errorf("move entry to trash: %w", err)
	}
	return Trashed{OriginalPath: target, StoredPath: storedPath, StoredName: storedName, Size: info.Size(), Directory: info.IsDir()}, nil
}

func (m *Manager) RestoreFromTrash(storedPath, original string) error {
	stored, err := m.resolveTrashEntry(storedPath)
	if err != nil {
		return err
	}
	target := filepath.Clean(original)
	if !filepath.IsAbs(target) {
		return fmt.Errorf("restore target must be absolute")
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return err
	}
	parent, err := m.resolveDirectory(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("invalid restore directory: %w", err)
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return err
	}
	target = filepath.Join(parent, filepath.Base(target))
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("restore target already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect restore target: %w", err)
	}
	if err := os.Rename(stored, target); err != nil {
		return fmt.Errorf("restore trash entry: %w", err)
	}
	return nil
}

func (m *Manager) RestoreFromTrashToAvailablePath(storedPath, original string) (string, error) {
	stored, err := m.resolveTrashEntry(storedPath)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(original)
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("restore target must be absolute")
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return "", err
	}
	parent, err := m.resolveDirectory(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return "", err
	}
	name := filepath.Base(target)
	for attempt := 0; ; attempt++ {
		candidate := name
		if attempt > 0 {
			candidate = restoredCandidateName(name, attempt)
		}
		destination := filepath.Join(parent, candidate)
		if _, err := os.Lstat(destination); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if err := os.Rename(stored, destination); err != nil {
			return "", fmt.Errorf("restore trash entry: %w", err)
		}
		return destination, nil
	}
}

func (m *Manager) AvailableName(directory, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	parent, err := m.resolveDirectory(directory)
	if err != nil {
		return "", err
	}
	for attempt := 1; ; attempt++ {
		candidate := name
		if attempt > 1 {
			candidate = availableCandidateName(name, attempt)
		}
		if _, err := os.Lstat(filepath.Join(parent, candidate)); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect available name: %w", err)
		}
	}
}

func (m *Manager) PurgeTrash(storedPath string) error {
	target, err := m.resolveTrashEntry(storedPath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

func (m *Manager) SaveText(path, expectedDigest, content, storedName string, maxBytes int64) (Trashed, error) {
	current, err := m.ReadText(path, maxBytes)
	if err != nil {
		return Trashed{}, err
	}
	if current.Digest != expectedDigest {
		return Trashed{}, ErrConflict
	}
	content = preserveLineEndingStyle(current.Content, content)
	if int64(len([]byte(content))) > maxBytes || !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return Trashed{}, fmt.Errorf("content is not safe UTF-8 within the size limit")
	}
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return Trashed{}, err
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return Trashed{}, err
	}
	if err := diskspace.Require(filepath.Dir(target), diskspace.MinimumWritableBytes); err != nil {
		return Trashed{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".scriptboard-edit-*")
	if err != nil {
		return Trashed{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return Trashed{}, err
	}
	if _, err := io.WriteString(temporary, content); err != nil {
		_ = temporary.Close()
		return Trashed{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Trashed{}, err
	}
	if err := temporary.Close(); err != nil {
		return Trashed{}, err
	}
	trashed, err := m.MoveToTrash(target, storedName)
	if err != nil {
		return Trashed{}, err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = m.RestoreFromTrash(trashed.StoredPath, trashed.OriginalPath)
		return Trashed{}, fmt.Errorf("commit edited content: %w", err)
	}
	return trashed, nil
}

func (m *Manager) RollbackTextSave(path, storedPath string) error {
	target, _, err := m.resolveEntry(path)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	return m.RestoreFromTrash(storedPath, path)
}

func (m *Manager) CreateDirectory(directory, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	parent, err := m.resolveDirectory(directory)
	if err != nil {
		return err
	}
	target := filepath.Join(parent, name)
	if err := m.ensureMutationAllowed(target); err != nil {
		return err
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	return nil
}

func (m *Manager) Move(source, destination string) error {
	sourcePath, sourceInfo, err := m.resolveEntry(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() && !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("restricted or special entry cannot be moved")
	}
	destinationPath := filepath.Clean(destination)
	if !filepath.IsAbs(destinationPath) {
		return fmt.Errorf("move destination must be absolute")
	}
	if err := m.ensureMutationAllowed(sourcePath); err != nil {
		return err
	}
	if err := m.ensureMutationAllowed(destinationPath); err != nil {
		return err
	}
	if sourceInfo.IsDir() && pathContains(sourcePath, destinationPath) {
		return fmt.Errorf("a directory cannot be moved into itself")
	}
	if err := ValidateName(filepath.Base(destinationPath)); err != nil {
		return err
	}
	parent, err := m.resolveDirectory(filepath.Dir(destinationPath))
	if err != nil {
		return err
	}
	destinationPath = filepath.Join(parent, filepath.Base(destinationPath))
	sourceRoot, err := m.topology.FilesystemRoot(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve source filesystem: %w", err)
	}
	if ComparisonKey(sourceRoot) == ComparisonKey(sourcePath) {
		return fmt.Errorf("a filesystem root cannot be moved")
	}
	destinationRoot, err := m.topology.FilesystemRoot(parent)
	if err != nil {
		return fmt.Errorf("resolve destination filesystem: %w", err)
	}
	if ComparisonKey(sourceRoot) != ComparisonKey(destinationRoot) {
		return fmt.Errorf("cross-filesystem move requires the persistent move engine")
	}
	if err := diskspace.Require(parent, diskspace.MinimumWritableBytes); err != nil {
		return err
	}
	if _, err := os.Lstat(destinationPath); err == nil {
		return fmt.Errorf("move destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		return fmt.Errorf("move entry: %w", err)
	}
	return nil
}

func (m *Manager) SameFilesystem(source, destination string) (bool, error) {
	sourcePath, _, err := m.resolveEntry(source)
	if err != nil {
		return false, err
	}
	destinationPath, err := m.CanonicalDestination(destination)
	if err != nil {
		return false, err
	}
	sourceRoot, err := m.topology.FilesystemRoot(sourcePath)
	if err != nil {
		return false, err
	}
	destinationRoot, err := m.topology.FilesystemRoot(filepath.Dir(destinationPath))
	if err != nil {
		return false, err
	}
	return ComparisonKey(sourceRoot) == ComparisonKey(destinationRoot), nil
}

func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, `/\`) || pathsecurity.UnsafeWindowsComponent(name) {
		return fmt.Errorf("name contains an invalid path component")
	}
	if reservedName(name) {
		return fmt.Errorf("name is reserved by ScriptBoard")
	}
	return nil
}

func (m *Manager) ensureTrashRoot(path string) (string, error) {
	root, err := m.topology.FilesystemRoot(path)
	if err != nil {
		return "", fmt.Errorf("resolve filesystem root: %w", err)
	}
	if err := diskspace.Require(root, diskspace.MinimumWritableBytes); err != nil {
		return "", err
	}
	trashBase := filepath.Join(root, trashDirectoryName)
	createdBase := false
	if err := os.Mkdir(trashBase, 0o700); err == nil {
		createdBase = true
	} else if !os.IsExist(err) {
		return "", fmt.Errorf("create filesystem trash: %w", err)
	}
	info, err := os.Lstat(trashBase)
	if err != nil || !info.IsDir() || restrictedEntry(trashBase, info) {
		return "", fmt.Errorf("filesystem trash is not a private directory")
	}
	cleanupEmptyBase := func() { _ = os.Remove(trashBase) }
	if err := privatepath.ProtectDirectory(trashBase); err != nil {
		if createdBase {
			cleanupEmptyBase()
		}
		return "", fmt.Errorf("protect filesystem trash: %w", err)
	}
	baseMarker := filepath.Join(trashBase, trashMarkerName)
	legacyExpected := []byte("scriptboard-trash-v1\n" + m.instanceID + "\n")
	if !createdBase {
		if err := verifyTrashMarker(baseMarker, legacyExpected); err == nil {
			return trashBase, nil
		}
		marker, markerErr := readTrashMarker(baseMarker)
		if markerErr != nil {
			return "", fmt.Errorf("refuse unowned filesystem trash: %w", markerErr)
		}
		if !bytes.Equal(marker, trashRootMarker) && !bytes.HasPrefix(marker, []byte("scriptboard-trash-v1\n")) {
			return "", fmt.Errorf("refuse unowned filesystem trash: filesystem trash marker is unknown")
		}
	} else if err := writeTrashMarker(baseMarker, trashRootMarker); err != nil {
		_ = os.Remove(baseMarker)
		cleanupEmptyBase()
		return "", fmt.Errorf("mark filesystem trash root: %w", err)
	}

	instanceHash := sha256.Sum256([]byte(m.instanceID))
	trashRoot := filepath.Join(trashBase, hex.EncodeToString(instanceHash[:16]))
	createdRoot := false
	if err := os.Mkdir(trashRoot, 0o700); err == nil {
		createdRoot = true
	} else if !os.IsExist(err) {
		return "", fmt.Errorf("create instance filesystem trash: %w", err)
	}
	rootInfo, err := os.Lstat(trashRoot)
	if err != nil || !rootInfo.IsDir() || restrictedEntry(trashRoot, rootInfo) {
		return "", fmt.Errorf("instance filesystem trash is not a private directory")
	}
	if err := privatepath.ProtectDirectory(trashRoot); err != nil {
		if createdRoot {
			_ = os.Remove(trashRoot)
		}
		return "", fmt.Errorf("protect instance filesystem trash: %w", err)
	}
	marker := filepath.Join(trashRoot, trashMarkerName)
	expected := []byte("scriptboard-trash-v2\n" + m.instanceID + "\n")
	if createdRoot {
		if err := writeTrashMarker(marker, expected); err != nil {
			_ = os.Remove(marker)
			_ = os.Remove(trashRoot)
			return "", fmt.Errorf("mark instance filesystem trash ownership: %w", err)
		}
	} else if err := verifyTrashMarker(marker, expected); err != nil {
		return "", fmt.Errorf("refuse unowned instance filesystem trash: %w", err)
	}
	return trashRoot, nil
}

func (m *Manager) resolveTrashEntry(storedPath string) (string, error) {
	if !filepath.IsAbs(storedPath) {
		return "", fmt.Errorf("invalid trash entry path")
	}
	trashRoot := filepath.Dir(filepath.Clean(storedPath))
	marker := filepath.Join(trashRoot, trashMarkerName)
	expected := []byte("scriptboard-trash-v2\n" + m.instanceID + "\n")
	if filepath.Base(trashRoot) == trashDirectoryName {
		expected = []byte("scriptboard-trash-v1\n" + m.instanceID + "\n")
	} else {
		instanceHash := sha256.Sum256([]byte(m.instanceID))
		if filepath.Base(filepath.Dir(trashRoot)) != trashDirectoryName || filepath.Base(trashRoot) != hex.EncodeToString(instanceHash[:16]) {
			return "", fmt.Errorf("invalid trash entry path")
		}
	}
	if err := verifyTrashMarker(marker, expected); err != nil {
		return "", err
	}
	if err := ValidateName(filepath.Base(storedPath)); err != nil {
		return "", err
	}
	if _, err := os.Lstat(storedPath); err != nil {
		return "", err
	}
	return filepath.Clean(storedPath), nil
}

func verifyTrashMarker(marker string, expected []byte) error {
	content, err := readTrashMarker(marker)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, expected) {
		return fmt.Errorf("filesystem trash belongs to another ScriptBoard instance")
	}
	return nil
}

func readTrashMarker(marker string) ([]byte, error) {
	info, err := os.Lstat(marker)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || restrictedEntry(marker, info) {
		return nil, fmt.Errorf("filesystem trash ownership marker is restricted")
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		return nil, fmt.Errorf("read filesystem trash ownership: %w", err)
	}
	return content, nil
}

func writeTrashMarker(marker string, content []byte) error {
	markerFile, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, writeErr = markerFile.Write(content); writeErr == nil {
		writeErr = markerFile.Sync()
	}
	if closeErr := markerFile.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func preserveLineEndingStyle(original, submitted string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(submitted, "\r\n", "\n"), "\r", "\n")
	withoutCRLF := strings.ReplaceAll(original, "\r\n", "")
	switch {
	case strings.Contains(original, "\r\n") && !strings.ContainsAny(withoutCRLF, "\r\n"):
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	case !strings.Contains(original, "\r\n") && strings.Contains(original, "\r") && !strings.Contains(original, "\n"):
		return strings.ReplaceAll(normalized, "\n", "\r")
	default:
		return normalized
	}
}

func availableCandidateName(name string, attempt int) string {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if stem == "" {
		stem, extension = name, ""
	}
	return fmt.Sprintf("%s (%d)%s", stem, attempt, extension)
}

func restoredCandidateName(name string, attempt int) string {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if stem == "" {
		stem, extension = name, ""
	}
	if attempt == 1 {
		return stem + " (restored)" + extension
	}
	return fmt.Sprintf("%s (restored %d)%s", stem, attempt, extension)
}

func reservedName(name string) bool {
	lower := strings.ToLower(name)
	return lower == trashDirectoryName || strings.HasPrefix(lower, ".scriptboard-upload-") || strings.HasPrefix(lower, ".scriptboard-edit-") || strings.HasPrefix(lower, ".scriptboard-move-")
}

func reservedPath(path string) bool {
	clean := filepath.Clean(path)
	for {
		name := filepath.Base(clean)
		if reservedName(name) {
			return true
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			return false
		}
		clean = parent
	}
}
