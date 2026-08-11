package mysqlmanager

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type RestoreRequest struct {
	InstanceID, BackupID, TargetDatabase string
	Actor                                Actor
}

func (m *Manager) Restore(ctx context.Context, request RestoreRequest) (Operation, error) {
	request.TargetDatabase = strings.TrimSpace(request.TargetDatabase)
	if request.TargetDatabase == "" || IsSystemDatabase(request.TargetDatabase) {
		return Operation{}, errors.New("a non-system restore target is required")
	}
	backup, err := m.BackupByID(ctx, request.BackupID)
	if err != nil {
		return Operation{}, err
	}
	if backup.InstanceID != request.InstanceID {
		return Operation{}, errors.New("backup belongs to another MySQL instance")
	}
	instance, password, err := m.instanceAndPassword(ctx, request.InstanceID)
	if err != nil {
		return Operation{}, err
	}
	operation, operationContext, release, err := m.beginOperation(ctx, "restore", instance.ID, backup.Database, request.Actor)
	if err != nil {
		return Operation{}, err
	}
	defer release()
	ctx = operationContext
	operation.TargetDatabase, operation.BackupID = request.TargetDatabase, backup.ID
	if _, err := m.db.ExecContext(ctx, "UPDATE mysql_operations SET target_database=?, backup_id=?, updated_at=? WHERE id=?",
		operation.TargetDatabase, operation.BackupID, m.now().UTC().UnixNano(), operation.ID); err != nil {
		return operation, err
	}
	if err := m.updateOperation(ctx, operation.ID, "verifying", "", "", backup.SizeBytes, 0); err != nil {
		return operation, err
	}
	if err := verifyBackupFile(backup); err != nil {
		_ = m.failOperation(ctx, operation.ID, err)
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	exists, err := m.server.DatabaseExists(ctx, instance, password, request.TargetDatabase)
	if err != nil {
		_ = m.failOperation(ctx, operation.ID, err)
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	var safety Backup
	if exists {
		safety, err = m.runBackup(ctx, operation, instance, BackupRequest{
			InstanceID: instance.ID, Database: request.TargetDatabase, Kind: BackupSafety,
			ActorUserID: request.Actor.UserID, ActorUsername: request.Actor.Username,
		}, false)
		if err != nil {
			result, _ := m.Operation(ctx, operation.ID)
			return result, err
		}
		operation.SafetyBackupID = safety.ID
		if _, err := m.db.ExecContext(ctx, "UPDATE mysql_operations SET safety_backup_id=?, phase='replacing', updated_at=? WHERE id=?",
			safety.ID, m.now().UTC().UnixNano(), operation.ID); err != nil {
			return operation, err
		}
	}
	if err := m.server.ReplaceDatabase(ctx, instance, password, request.TargetDatabase); err != nil {
		_ = m.failOperation(ctx, operation.ID, err)
		result, _ := m.Operation(ctx, operation.ID)
		return result, err
	}
	_ = m.updateOperation(ctx, operation.ID, "importing", "", "", backup.SizeBytes, 0)
	if err := m.importBackup(ctx, instance, request.TargetDatabase, backup); err == nil {
		_ = m.updateOperation(ctx, operation.ID, "completed", "", "", backup.SizeBytes, backup.SizeBytes)
		m.recordAudit(AuditEvent{Action: "mysql_restore", Target: instance.ID + "/" + request.TargetDatabase, Result: "succeeded", Actor: request.Actor})
		return m.Operation(ctx, operation.ID)
	} else {
		originalError := err
		rollbackContext, cancelRollback := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancelRollback()
		if safety.ID == "" {
			rollbackError := m.server.DropDatabase(rollbackContext, instance, password, request.TargetDatabase)
			if rollbackError != nil {
				_ = m.updateOperation(rollbackContext, operation.ID, "needs_attention", originalError.Error(), rollbackError.Error(), 0, 0)
				result, _ := m.Operation(rollbackContext, operation.ID)
				m.recordAudit(AuditEvent{Action: "mysql_restore", Target: instance.ID + "/" + request.TargetDatabase, Result: "needs_attention", Actor: request.Actor})
				return result, fmt.Errorf("restore failed: %w; cleanup of the new database failed: %v", originalError, rollbackError)
			}
			_ = m.updateOperation(rollbackContext, operation.ID, "rolled_back", originalError.Error(), "", 0, 0)
			result, _ := m.Operation(rollbackContext, operation.ID)
			m.recordAudit(AuditEvent{Action: "mysql_restore", Target: instance.ID + "/" + request.TargetDatabase, Result: "rolled_back", Actor: request.Actor})
			return result, fmt.Errorf("restore failed and the new database was removed: %w", originalError)
		}
		_ = m.updateOperation(rollbackContext, operation.ID, "rolling_back", originalError.Error(), "", safety.SizeBytes, 0)
		rollbackError := m.server.ReplaceDatabase(rollbackContext, instance, password, request.TargetDatabase)
		if rollbackError == nil {
			rollbackError = m.importBackup(rollbackContext, instance, request.TargetDatabase, safety)
		}
		if rollbackError != nil {
			_ = m.updateOperation(rollbackContext, operation.ID, "needs_attention", originalError.Error(), rollbackError.Error(), safety.SizeBytes, 0)
			result, _ := m.Operation(rollbackContext, operation.ID)
			m.recordAudit(AuditEvent{Action: "mysql_restore", Target: instance.ID + "/" + request.TargetDatabase, Result: "needs_attention", Actor: request.Actor})
			return result, fmt.Errorf("restore failed: %w; automatic rollback failed: %v", originalError, rollbackError)
		}
		_ = m.updateOperation(rollbackContext, operation.ID, "rolled_back", originalError.Error(), "", safety.SizeBytes, safety.SizeBytes)
		result, _ := m.Operation(rollbackContext, operation.ID)
		m.recordAudit(AuditEvent{Action: "mysql_restore", Target: instance.ID + "/" + request.TargetDatabase, Result: "rolled_back", Actor: request.Actor})
		return result, fmt.Errorf("restore failed and the safety backup was restored: %w", originalError)
	}
}

func (m *Manager) importBackup(ctx context.Context, instance Instance, target string, backup Backup) error {
	optionPath, cleanup, err := m.clientOptionFile(instance)
	if err != nil {
		return err
	}
	defer cleanup()
	file, err := os.Open(backup.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	var input io.Reader = file
	if strings.HasSuffix(strings.ToLower(backup.Path), ".gz") {
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		input = compressed
	}
	stderr := &boundedBuffer{maximum: 64 << 10}
	if err := m.runner.Run(ctx, m.Tools().ClientExecutable, mysqlImportArguments(optionPath, target), input, io.Discard, stderr); err != nil {
		password, _ := m.instancePassword(instance.ID)
		return fmt.Errorf("mysql import failed: %w%s", err, sanitizedCommandError(stderr.String(), password, optionPath))
	}
	return nil
}

// mysqlImportArguments keeps imported SQL in non-interactive binary mode.
// Both MySQL and MariaDB document that this disables local client commands
// such as system, source, pager, and tee while preserving dump delimiters.
func mysqlImportArguments(optionPath, target string) []string {
	return []string{
		"--defaults-extra-file=" + optionPath,
		"--binary-mode",
		"--batch",
		"--skip-reconnect",
		"--default-character-set=utf8mb4",
		"--",
		target,
	}
}
