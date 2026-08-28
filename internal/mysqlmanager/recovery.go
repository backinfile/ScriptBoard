package mysqlmanager

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RecoverInterrupted resolves operations whose child process supervision was
// lost. Destructive operations prefer restoring their safety backup over
// guessing how far the server mutation progressed.
func (m *Manager) RecoverInterrupted(ctx context.Context) error {
	_ = filepath.WalkDir(m.BackupRoot(), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".partial") || strings.HasPrefix(entry.Name(), ".mysql-backup-")) {
			_ = os.Remove(path)
		}
		return nil
	})
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM mysql_operations WHERE phase NOT IN
		('completed','cancelled','failed','rolled_back','needs_attention','skipped_overlap') ORDER BY created_at`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		operation, err := m.Operation(ctx, id)
		if err != nil {
			continue
		}
		if operation.Phase == "preflight" || operation.Phase == "verifying" || operation.Phase == "dumping" || operation.Phase == "validating" {
			_ = m.updateOperation(ctx, id, "failed", "service restarted before the destructive phase", "", operation.BytesTotal, operation.BytesCompleted)
			continue
		}
		if operation.SafetyBackupID == "" {
			_ = m.updateOperation(ctx, id, "needs_attention", "service restarted during a destructive MySQL operation", "safety backup metadata is unavailable", operation.BytesTotal, operation.BytesCompleted)
			continue
		}
		safety, err := m.BackupByID(ctx, operation.SafetyBackupID)
		if err != nil {
			_ = m.updateOperation(ctx, id, "needs_attention", "service restarted during a destructive MySQL operation", "safety backup is unavailable", operation.BytesTotal, operation.BytesCompleted)
			continue
		}
		if err := verifyBackupFile(safety); err != nil {
			_ = m.updateOperation(ctx, id, "needs_attention", "service restarted during a destructive MySQL operation", err.Error(), operation.BytesTotal, operation.BytesCompleted)
			m.recordAudit(AuditEvent{Action: "mysql_restore_recovery", Target: operation.InstanceID + "/" + operation.TargetDatabase, Result: "needs_attention", Actor: operation.Actor})
			continue
		}
		instance, err := m.Instance(ctx, operation.InstanceID)
		if err == nil && operation.Kind == "backup_and_clear_database" {
			err = m.backend.ClearDatabase(ctx, instance, operation.TargetDatabase)
		} else if err == nil {
			err = m.backend.ReplaceDatabase(ctx, instance, operation.TargetDatabase)
		}
		if err == nil {
			err = m.importBackup(ctx, instance, operation.TargetDatabase, safety)
		}
		if err != nil {
			_ = m.updateOperation(ctx, id, "needs_attention", "service restarted during a destructive MySQL operation", err.Error(), safety.SizeBytes, 0)
			m.recordAudit(AuditEvent{Action: "mysql_restore_recovery", Target: operation.InstanceID + "/" + operation.TargetDatabase, Result: "needs_attention", Actor: operation.Actor})
			continue
		}
		_ = m.updateOperation(ctx, id, "rolled_back", "service restarted during the destructive phase", "", safety.SizeBytes, safety.SizeBytes)
		m.recordAudit(AuditEvent{Action: "mysql_restore_recovery", Target: operation.InstanceID + "/" + operation.TargetDatabase, Result: "rolled_back", Actor: operation.Actor})
	}
	return nil
}

func (m *Manager) SetBackupRoot(ctx context.Context, root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("MySQL backup root must be an absolute path")
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO mysql_settings(key,value,updated_at) VALUES ('backup_root',?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, root, m.now().UTC().UnixNano()); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.backupRoot = root
	m.mu.Unlock()
	return root, nil
}

func loadBackupRoot(ctx context.Context, database *sql.DB, fallback string) string {
	var value string
	if err := database.QueryRowContext(ctx, "SELECT value FROM mysql_settings WHERE key='backup_root'").Scan(&value); err == nil && filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return fallback
}
