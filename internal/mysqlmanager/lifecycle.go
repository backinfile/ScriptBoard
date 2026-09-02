package mysqlmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

type DropDatabaseRequest struct {
	InstanceID, Database, Confirmation string
	Actor                              Actor
}

type BackupAndClearDatabaseRequest struct {
	InstanceID, Database, Confirmation string
	Actor                              Actor
}

func (m *Manager) BackupAndClearDatabase(ctx context.Context, request BackupAndClearDatabaseRequest) (Operation, error) {
	request.Database = strings.TrimSpace(request.Database)
	if request.Database == "" || IsSystemDatabase(request.Database) || request.Confirmation != request.Database {
		return Operation{}, errors.New("the complete non-system database name is required to confirm clearing")
	}
	instance, err := m.Instance(ctx, request.InstanceID)
	if err != nil {
		return Operation{}, err
	}
	operation, operationContext, release, err := m.beginOperation(ctx, "backup_and_clear_database", instance.ID, request.Database, request.Actor)
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
	if _, err := m.db.ExecContext(ctx, "UPDATE mysql_operations SET safety_backup_id=?, target_database=?, phase='clearing', updated_at=? WHERE id=?", safety.ID, request.Database, m.now().UTC().UnixNano(), operation.ID); err != nil {
		return operation, err
	}
	if err := m.backend.ClearDatabase(ctx, instance, request.Database); err != nil {
		_ = m.failOperation(ctx, operation.ID, err)
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	_ = m.updateOperation(ctx, operation.ID, "completed", "", "", safety.SizeBytes, safety.SizeBytes)
	m.recordAudit(AuditEvent{Action: "mysql_backup_and_clear_database", Target: instance.ID + "/" + request.Database, Result: "succeeded", Actor: request.Actor})
	return m.Operation(ctx, operation.ID)
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
	id := randomID()
	finalPath := filepath.Join(directory, m.now().UTC().Format("20060102T150405Z")+"-"+id+".sql.gz")
	limited := &io.LimitedReader{R: request.Reader, N: (2 << 30) + 1}
	result, err := m.backend.StoreArtifact(ctx, finalPath, limited, isGzip)
	if err != nil {
		return Backup{}, err
	}
	_, digestErr := hex.DecodeString(result.SHA256)
	if result.SizeBytes <= 0 || len(result.SHA256) != 64 || digestErr != nil {
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		return Backup{}, errors.New("Broker returned invalid MySQL import artifact metadata")
	}
	if limited.N <= 0 {
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		return Backup{}, errors.New("MySQL import exceeds the 2 GiB upload limit")
	}
	backup := Backup{ID: id, InstanceID: instance.ID, Database: request.Database, Kind: BackupImported, Path: finalPath,
		SizeBytes: result.SizeBytes, SHA256: result.SHA256, CreatedAt: m.now().UTC(),
		CreatedByUserID: request.Actor.UserID, CreatedByUsername: request.Actor.Username}
	_, err = m.db.ExecContext(ctx, `INSERT INTO mysql_backups
		(id, instance_id, database_name, plan_id, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, '', ?, ?, ?)`, backup.ID, backup.InstanceID, backup.Database, backup.Kind,
		backup.Path, backup.SizeBytes, backup.SHA256, backup.CreatedAt.UnixNano(), backup.CreatedByUserID, backup.CreatedByUsername)
	if err != nil {
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		return Backup{}, err
	}
	m.recordAudit(AuditEvent{Action: "mysql_import_backup", Target: backup.InstanceID + "/" + backup.Database, Result: "succeeded", Actor: request.Actor})
	return backup, nil
}

func (m *Manager) verifyBackupFile(ctx context.Context, backup Backup) error {
	return m.backend.VerifyArtifact(ctx, backup.Path, backup.SHA256, strings.HasSuffix(strings.ToLower(backup.Path), ".gz"))
}

func (m *Manager) DownloadBackup(ctx context.Context, id string, destination io.Writer) (string, int64, error) {
	return m.backend.DownloadBackup(ctx, id, destination)
}

func (m *Manager) DeleteBackup(ctx context.Context, id string) error {
	backup, err := m.BackupByID(ctx, id)
	if err != nil {
		return err
	}
	if !pathWithin(m.BackupRoot(), backup.Path) {
		return errors.New("backup path is outside the managed MySQL root")
	}
	if err := m.backend.DeleteArtifact(ctx, backup.Path); err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "DELETE FROM mysql_backups WHERE id=?", id)
	return err
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
