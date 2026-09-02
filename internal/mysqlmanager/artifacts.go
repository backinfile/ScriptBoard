package mysqlmanager

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ArtifactResult struct {
	SizeBytes int64
	SHA256    string
}

func fileSHA256(path string) (string, error) {
	file, err := openRegularArtifact(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func openRegularArtifact(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("MySQL backup artifact is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("MySQL backup artifact changed while opening")
	}
	return file, nil
}

func commitArtifactNoReplace(temporaryPath, destinationPath string) error {
	// Hard-link publication is atomic and refuses an existing destination, so
	// a link or unusual entry cannot be silently replaced at commit time.
	if err := os.Link(temporaryPath, destinationPath); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(destinationPath)
		return err
	}
	return nil
}

func (*localBackend) PrepareArtifactRoot(_ context.Context, root string) error {
	return os.MkdirAll(root, 0o700)
}

func (*localBackend) StoreArtifact(_ context.Context, destinationPath string, source io.Reader, compressed bool) (ArtifactResult, error) {
	directory := filepath.Dir(destinationPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ArtifactResult{}, err
	}
	temporary, err := os.CreateTemp(directory, ".mysql-import-*.partial")
	if err != nil {
		return ArtifactResult{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_ = temporary.Chmod(0o600)
	hash := sha256.New()
	destination := io.MultiWriter(temporary, hash)
	if compressed {
		_, err = io.Copy(destination, source)
	} else {
		writer := gzip.NewWriter(destination)
		_, err = io.Copy(writer, source)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ArtifactResult{}, err
	}
	info, err := os.Stat(temporaryPath)
	if err != nil || info.Size() == 0 {
		return ArtifactResult{}, errors.New("imported SQL backup is empty")
	}
	if compressed {
		file, openErr := openRegularArtifact(temporaryPath)
		if openErr != nil {
			return ArtifactResult{}, openErr
		}
		err = validateGzipSQL(file, maximumExpandedSQLBytes)
		_ = file.Close()
		if err != nil {
			return ArtifactResult{}, err
		}
	}
	if err := commitArtifactNoReplace(temporaryPath, destinationPath); err != nil {
		return ArtifactResult{}, err
	}
	return ArtifactResult{SizeBytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (*localBackend) VerifyArtifact(_ context.Context, path, expectedSHA256 string, compressed bool) error {
	file, err := openRegularArtifact(path)
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("read backup for verification: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedSHA256) {
		return errors.New("backup SHA-256 verification failed")
	}
	if compressed {
		if err := validateGzipSQL(file, maximumExpandedSQLBytes); err != nil {
			return fmt.Errorf("verify compressed backup: %w", err)
		}
	}
	return nil
}

func (backend *localBackend) DownloadBackup(ctx context.Context, id string, destination io.Writer) (string, int64, error) {
	backup, err := backend.manager.BackupByID(ctx, id)
	if err != nil || !pathWithin(backend.manager.BackupRoot(), backup.Path) {
		return "", 0, errors.New("MySQL backup is unavailable")
	}
	file, err := openRegularArtifact(backup.Path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != backup.SizeBytes {
		return "", 0, errors.New("MySQL backup file changed")
	}
	written, err := io.Copy(destination, file)
	if err != nil || written != backup.SizeBytes {
		return "", 0, errors.Join(err, io.ErrUnexpectedEOF)
	}
	return backup.Database + "-" + backup.ID + ".sql.gz", written, nil
}

func (*localBackend) CleanupArtifacts(_ context.Context, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() && (strings.HasSuffix(entry.Name(), ".partial") || strings.HasPrefix(entry.Name(), ".mysql-backup-")) {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return removeErr
			}
		}
		return nil
	})
}

func validateGzipSQL(openFile io.ReadSeeker, maximumExpandedBytes int64) error {
	if maximumExpandedBytes <= 0 {
		return errors.New("invalid expanded SQL size limit")
	}
	if _, err := openFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader, err := gzip.NewReader(openFile)
	if err != nil {
		return fmt.Errorf("invalid gzip backup: %w", err)
	}
	defer reader.Close()
	written, err := io.Copy(io.Discard, io.LimitReader(reader, maximumExpandedBytes+1))
	if err != nil {
		return fmt.Errorf("invalid gzip backup: %w", err)
	}
	if written > maximumExpandedBytes {
		return fmt.Errorf("expanded size exceeds the %d-byte SQL limit", maximumExpandedBytes)
	}
	if err := reader.Close(); err != nil {
		return fmt.Errorf("invalid gzip backup: %w", err)
	}
	if written == 0 {
		return errors.New("compressed SQL backup is empty or unreadable")
	}
	return nil
}
