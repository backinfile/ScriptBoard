package mysqlmanager

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type DropDatabaseRequest struct {
	InstanceID, Database, Confirmation string
	Actor                              Actor
}

func (m *Manager) DropDatabase(ctx context.Context, request DropDatabaseRequest) (Operation, error) {
	request.Database = strings.TrimSpace(request.Database)
	if request.Database == "" || IsSystemDatabase(request.Database) || request.Confirmation != request.Database {
		return Operation{}, errors.New("the complete non-system database name is required to confirm deletion")
	}
	instance, err := m.Instance(ctx, request.InstanceID)
	if err != nil {
		return Operation{}, err
	}
	operation, operationContext, release, err := m.beginOperation(ctx, "drop_database", instance.ID, request.Database, request.Actor)
	if err != nil {
		return Operation{}, err
	}
	defer release()
	ctx = operationContext
	safety, err := m.runBackup(ctx, operation, instance, BackupRequest{
		InstanceID: instance.ID, Database: request.Database, Kind: BackupSafety,
		ActorUserID: request.Actor.UserID, ActorUsername: request.Actor.Username,
	}, false)
	if err != nil {
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	if _, err := m.db.ExecContext(ctx, "UPDATE mysql_operations SET safety_backup_id=?, target_database=?, phase='replacing', updated_at=? WHERE id=?", safety.ID, request.Database, m.now().UTC().UnixNano(), operation.ID); err != nil {
		return operation, err
	}
	if err := m.backend.DropDatabase(ctx, instance, request.Database); err != nil {
		_ = m.failOperation(ctx, operation.ID, err)
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	_ = m.updateOperation(ctx, operation.ID, "completed", "", "", safety.SizeBytes, safety.SizeBytes)
	m.recordAudit(AuditEvent{Action: "mysql_drop_database", Target: instance.ID + "/" + request.Database, Result: "succeeded", Actor: request.Actor})
	return m.Operation(ctx, operation.ID)
}

type ImportRequest struct {
	InstanceID, Database, Filename string
	Actor                          Actor
	Reader                         io.Reader
}

const maximumExpandedSQLBytes int64 = 8 << 30

func (m *Manager) ImportBackup(ctx context.Context, request ImportRequest) (Backup, error) {
	request.Database, request.Filename = strings.TrimSpace(request.Database), strings.TrimSpace(request.Filename)
	extension := strings.ToLower(filepath.Ext(request.Filename))
	isGzip := strings.HasSuffix(strings.ToLower(request.Filename), ".sql.gz")
	if request.Database == "" || IsSystemDatabase(request.Database) || request.Reader == nil || !(extension == ".sql" || isGzip) {
		return Backup{}, errors.New("import requires a non-system database and a .sql or .sql.gz file")
	}
	instance, err := m.Instance(ctx, request.InstanceID)
	if err != nil {
		return Backup{}, err
	}
	digest := sha256.Sum256([]byte(request.Database))
	directory := filepath.Join(m.BackupRoot(), instance.ID, hex.EncodeToString(digest[:8]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Backup{}, err
	}
	id := randomID()
	finalPath := filepath.Join(directory, m.now().UTC().Format("20060102T150405Z")+"-"+id+".sql.gz")
	temporary, err := os.CreateTemp(directory, ".mysql-import-*.partial")
	if err != nil {
		return Backup{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_ = temporary.Chmod(0o600)
	hash := sha256.New()
	destination := io.MultiWriter(temporary, hash)
	limited := &io.LimitedReader{R: request.Reader, N: (2 << 30) + 1}
	if isGzip {
		if _, err = io.Copy(destination, limited); err == nil {
			err = validateGzipSQL(temporary, maximumExpandedSQLBytes)
		}
	} else {
		compressed := gzip.NewWriter(destination)
		_, err = io.Copy(compressed, limited)
		if closeErr := compressed.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil && limited.N <= 0 {
		err = errors.New("MySQL import exceeds the 2 GiB upload limit")
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Backup{}, err
	}
	info, err := os.Stat(temporaryPath)
	if err != nil || info.Size() == 0 {
		return Backup{}, errors.New("imported SQL backup is empty")
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return Backup{}, err
	}
	backup := Backup{ID: id, InstanceID: instance.ID, Database: request.Database, Kind: BackupImported, Path: finalPath,
		SizeBytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), CreatedAt: m.now().UTC(),
		CreatedByUserID: request.Actor.UserID, CreatedByUsername: request.Actor.Username}
	_, err = m.db.ExecContext(ctx, `INSERT INTO mysql_backups
		(id, instance_id, database_name, plan_id, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, '', ?, ?, ?)`, backup.ID, backup.InstanceID, backup.Database, backup.Kind,
		backup.Path, backup.SizeBytes, backup.SHA256, backup.CreatedAt.UnixNano(), backup.CreatedByUserID, backup.CreatedByUsername)
	if err != nil {
		_ = os.Remove(finalPath)
		return Backup{}, err
	}
	m.recordAudit(AuditEvent{Action: "mysql_import_backup", Target: backup.InstanceID + "/" + backup.Database, Result: "succeeded", Actor: request.Actor})
	return backup, nil
}

func validateGzipSQL(openFile *os.File, maximumExpandedBytes int64) error {
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

func verifyBackupFile(backup Backup) error {
	file, err := os.Open(backup.Path)
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("read backup for verification: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), backup.SHA256) {
		return errors.New("backup SHA-256 verification failed")
	}
	if strings.HasSuffix(strings.ToLower(backup.Path), ".gz") {
		if err := validateGzipSQL(file, maximumExpandedSQLBytes); err != nil {
			return fmt.Errorf("verify compressed backup: %w", err)
		}
	}
	return nil
}

func (m *Manager) DeleteBackup(ctx context.Context, id string) error {
	backup, err := m.BackupByID(ctx, id)
	if err != nil {
		return err
	}
	if !pathWithin(m.BackupRoot(), backup.Path) {
		return errors.New("backup path is outside the managed MySQL root")
	}
	if err := os.Remove(backup.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err = m.db.ExecContext(ctx, "DELETE FROM mysql_backups WHERE id=?", id)
	return err
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
