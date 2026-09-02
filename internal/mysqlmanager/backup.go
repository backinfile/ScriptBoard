package mysqlmanager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scriptboard/internal/processlaunch"
)

type BackupKind string

const (
	BackupManual    BackupKind = "manual"
	BackupScheduled BackupKind = "scheduled"
	BackupImported  BackupKind = "imported"
	BackupSafety    BackupKind = "safety"
)

type Actor struct {
	UserID, Username string
}

type BackupRequest struct {
	InstanceID, Database, PlanID, ActorUserID, ActorUsername string
	Kind                                                     BackupKind
}

type BatchBackupRequest struct {
	InstanceID string
	Databases  []string
	Actor      Actor
}

type Backup struct {
	ID, InstanceID, Database, PlanID, SourceName, Path, SHA256, Warning string
	Kind                                                                BackupKind
	SizeBytes                                                           int64
	CreatedAt                                                           time.Time
	CreatedByUserID, CreatedByUsername                                  string
}

type Operation struct {
	ID, Kind, InstanceID, Database, TargetDatabase, BackupID, SafetyBackupID string
	Phase, Error, RollbackError                                              string
	BytesTotal, BytesCompleted                                               int64
	CancelRequested                                                          bool
	CreatedAt, UpdatedAt                                                     time.Time
	Actor                                                                    Actor
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, executable string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: ctx, Executable: executable, Arguments: args, Environment: processlaunch.EnvironmentInherit,
	})
	if err != nil {
		return err
	}
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	return command.Run()
}

func (m *Manager) Backup(ctx context.Context, request BackupRequest) (Backup, error) {
	request.Database = strings.TrimSpace(request.Database)
	if request.Database == "" || IsSystemDatabase(request.Database) {
		return Backup{}, errors.New("a non-system database is required")
	}
	if request.Kind == "" {
		request.Kind = BackupManual
	}
	if request.Kind != BackupManual && request.Kind != BackupScheduled && request.Kind != BackupSafety {
		return Backup{}, errors.New("invalid MySQL backup kind")
	}
	instance, err := m.Instance(ctx, request.InstanceID)
	if err != nil {
		return Backup{}, err
	}
	sourceName := ""
	if request.Kind == BackupScheduled {
		plan, planErr := m.Plan(ctx, request.PlanID)
		if planErr != nil || plan.InstanceID != instance.ID {
			return Backup{}, errors.New("scheduled MySQL backup requires a plan for the selected instance")
		}
		// Keep the plan name on the backup so its recorded source survives later plan edits or deletion.
		sourceName = plan.Name
	}
	operation, operationContext, release, err := m.beginOperation(ctx, "backup", instance.ID, request.Database, Actor{request.ActorUserID, request.ActorUsername})
	if err != nil {
		return Backup{}, err
	}
	defer release()
	return m.runBackup(operationContext, operation, instance, request, sourceName, true)
}

// BackupBatch deliberately reuses the single-database operation boundary so
// one instance never has more than one active client process.
func (m *Manager) BackupBatch(ctx context.Context, request BatchBackupRequest) ([]Backup, error) {
	seen := make(map[string]bool)
	var backups []Backup
	var failures []error
	for _, database := range request.Databases {
		database = strings.TrimSpace(database)
		if database == "" || IsSystemDatabase(database) || seen[database] {
			continue
		}
		seen[database] = true
		backup, err := m.Backup(ctx, BackupRequest{InstanceID: request.InstanceID, Database: database, Kind: BackupManual,
			ActorUserID: request.Actor.UserID, ActorUsername: request.Actor.Username})
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", database, err))
			continue
		}
		backups = append(backups, backup)
	}
	if len(seen) == 0 {
		return nil, errors.New("select at least one non-system database")
	}
	return backups, errors.Join(failures...)
}

func (m *Manager) runBackup(ctx context.Context, operation Operation, instance Instance, request BackupRequest, sourceName string, completeOperation bool) (Backup, error) {
	if err := m.updateOperation(ctx, operation.ID, "dumping", "", "", 0, 0); err != nil {
		return Backup{}, err
	}
	databaseDigest := sha256.Sum256([]byte(request.Database))
	directory := filepath.Join(m.BackupRoot(), instance.ID, hex.EncodeToString(databaseDigest[:8]))
	backupID := randomID()
	filename := m.now().UTC().Format("20060102T150405Z") + "-" + backupID + ".sql.gz"
	finalPath := filepath.Join(directory, filename)
	// The Broker owns the complete artifact lifecycle so a low-privilege Web
	// process never has to reopen a root-owned 0600 dump to hash or commit it.
	dumpResult, runErr := m.backend.Dump(ctx, instance, request.Database, finalPath)
	if runErr != nil {
		cause := runErr
		if errors.Is(ctx.Err(), context.Canceled) {
			_ = m.updateOperation(context.Background(), operation.ID, "cancelled", "operation cancelled before a destructive phase", "", 0, 0)
			m.recordAudit(AuditEvent{Action: "mysql_backup", Target: instance.ID + "/" + request.Database, Result: "cancelled", Actor: Actor{request.ActorUserID, request.ActorUsername}})
		} else {
			_ = m.failOperation(ctx, operation.ID, cause)
			m.recordAudit(AuditEvent{Action: "mysql_backup", Target: instance.ID + "/" + request.Database, Result: "failed", Actor: Actor{request.ActorUserID, request.ActorUsername}})
		}
		return Backup{}, cause
	}
	if err := m.updateOperation(ctx, operation.ID, "validating", "", "", 0, 0); err != nil {
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		return Backup{}, err
	}
	_, digestErr := hex.DecodeString(dumpResult.SHA256)
	if dumpResult.SizeBytes <= 0 || len(dumpResult.SHA256) != 64 || digestErr != nil {
		err := errors.New("Broker returned invalid MySQL backup artifact metadata")
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		_ = m.failOperation(ctx, operation.ID, err)
		return Backup{}, err
	}
	backup := Backup{
		ID: backupID, InstanceID: instance.ID, Database: request.Database, PlanID: request.PlanID, SourceName: sourceName, Kind: request.Kind,
		Path: finalPath, SizeBytes: dumpResult.SizeBytes, SHA256: dumpResult.SHA256, CreatedAt: m.now().UTC(),
		CreatedByUserID: request.ActorUserID, CreatedByUsername: request.ActorUsername, Warning: dumpResult.Warning,
	}
	_, err := m.db.ExecContext(ctx, `INSERT INTO mysql_backups
		(id, instance_id, database_name, plan_id, source_name, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, backup.ID, backup.InstanceID, backup.Database, backup.PlanID, backup.SourceName,
		backup.Kind, backup.Path, backup.SizeBytes, backup.SHA256, backup.Warning, backup.CreatedAt.UnixNano(),
		backup.CreatedByUserID, backup.CreatedByUsername)
	if err != nil {
		_ = m.backend.DeleteArtifact(context.WithoutCancel(ctx), finalPath)
		_ = m.failOperation(ctx, operation.ID, err)
		return Backup{}, err
	}
	if completeOperation {
		if _, err := m.db.ExecContext(ctx, `UPDATE mysql_operations SET phase='completed', backup_id=?, bytes_total=?, bytes_completed=?, updated_at=? WHERE id=?`,
			backup.ID, backup.SizeBytes, backup.SizeBytes, m.now().UTC().UnixNano(), operation.ID); err != nil {
			return Backup{}, err
		}
	}
	m.recordAudit(AuditEvent{Action: "mysql_backup", Target: instance.ID + "/" + request.Database, Result: "succeeded", Actor: Actor{request.ActorUserID, request.ActorUsername}})
	return backup, nil
}

func (m *Manager) beginOperation(ctx context.Context, kind, instanceID, database string, actor Actor) (Operation, context.Context, func(), error) {
	m.mu.Lock()
	if active := m.active[instanceID]; active != "" {
		m.mu.Unlock()
		return Operation{}, ctx, func() {}, errors.New("another MySQL operation is active for this instance")
	}
	operationContext, cancel := context.WithCancel(ctx)
	operation := Operation{ID: randomID(), Kind: kind, InstanceID: instanceID, Database: database, Phase: "preflight", CreatedAt: m.now().UTC(), UpdatedAt: m.now().UTC(), Actor: actor}
	m.active[instanceID] = operation.ID
	m.cancels[operation.ID] = cancel
	m.mu.Unlock()
	_, err := m.db.ExecContext(ctx, `INSERT INTO mysql_operations
		(id, kind, instance_id, database_name, target_database, backup_id, safety_backup_id, phase, bytes_total, bytes_completed,
		error, rollback_error, cancel_requested, created_at, updated_at, actor_user_id, actor_username)
		VALUES (?, ?, ?, ?, '', '', '', ?, 0, 0, '', '', 0, ?, ?, ?, ?)`, operation.ID, operation.Kind, operation.InstanceID,
		operation.Database, operation.Phase, operation.CreatedAt.UnixNano(), operation.UpdatedAt.UnixNano(), actor.UserID, actor.Username)
	if err != nil {
		m.mu.Lock()
		delete(m.active, instanceID)
		m.mu.Unlock()
		cancel()
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") || strings.Contains(err.Error(), "mysql_operations_one_active_idx") {
			return Operation{}, ctx, func() {}, errors.New("another MySQL operation is active for this instance")
		}
		return Operation{}, ctx, func() {}, err
	}
	return operation, operationContext, func() {
		cancel()
		m.mu.Lock()
		delete(m.active, instanceID)
		delete(m.cancels, operation.ID)
		m.mu.Unlock()
	}, nil
}

func (m *Manager) updateOperation(ctx context.Context, id, phase, errorText, rollbackError string, total, completed int64) error {
	_, err := m.db.ExecContext(ctx, `UPDATE mysql_operations SET phase=?, error=?, rollback_error=?, bytes_total=?, bytes_completed=?, updated_at=? WHERE id=?`,
		phase, errorText, rollbackError, total, completed, m.now().UTC().UnixNano(), id)
	return err
}

func (m *Manager) failOperation(ctx context.Context, id string, cause error) error {
	return m.updateOperation(ctx, id, "failed", cause.Error(), "", 0, 0)
}

func (m *Manager) RequestCancel(ctx context.Context, id string) error {
	operation, err := m.Operation(ctx, id)
	if err != nil {
		return err
	}
	safe := operation.Kind == "backup" || operation.Phase == "preflight" || operation.Phase == "verifying" || operation.Phase == "dumping" || operation.Phase == "validating"
	if !safe || mysqlOperationTerminal(operation.Phase) {
		return errors.New("MySQL operation can no longer be cancelled safely")
	}
	if _, err := m.db.ExecContext(ctx, "UPDATE mysql_operations SET cancel_requested=1, updated_at=? WHERE id=?", m.now().UTC().UnixNano(), id); err != nil {
		return err
	}
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	} else if remote, ok := m.backend.(RemoteOperationCanceller); ok {
		return remote.CancelOperation(ctx, id)
	}
	return nil
}

func mysqlOperationTerminal(phase string) bool {
	switch phase {
	case "completed", "cancelled", "failed", "rolled_back", "needs_attention", "skipped_overlap":
		return true
	default:
		return false
	}
}

func (m *Manager) Backups(ctx context.Context, instanceID, database string) ([]Backup, error) {
	items, _, err := m.BackupsPage(ctx, instanceID, database, 0, 0)
	return items, err
}

func (m *Manager) BackupDatabases(ctx context.Context, instanceID string) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT database_name FROM mysql_backups
		WHERE instance_id=? ORDER BY database_name COLLATE NOCASE, database_name`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var database string
		if err := rows.Scan(&database); err != nil {
			return nil, err
		}
		result = append(result, database)
	}
	return result, rows.Err()
}

func (m *Manager) BackupsPage(ctx context.Context, instanceID, database string, limit, offset int) ([]Backup, int, error) {
	query := `SELECT id, instance_id, database_name, plan_id, source_name, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username
		FROM mysql_backups WHERE instance_id=?`
	countQuery := `SELECT COUNT(*) FROM mysql_backups WHERE instance_id=?`
	arguments := []any{instanceID}
	if database != "" {
		query += " AND database_name=?"
		countQuery += " AND database_name=?"
		arguments = append(arguments, database)
	}
	var total int
	if err := m.db.QueryRowContext(ctx, countQuery, arguments...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		arguments = append(arguments, limit, max(offset, 0))
	}
	rows, err := m.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []Backup
	for rows.Next() {
		var item Backup
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.Database, &item.PlanID, &item.SourceName, &item.Kind, &item.Path, &item.SizeBytes,
			&item.SHA256, &item.Warning, &createdAt, &item.CreatedByUserID, &item.CreatedByUsername); err != nil {
			return nil, 0, err
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		result = append(result, item)
	}
	return result, total, rows.Err()
}

func (m *Manager) BackupByID(ctx context.Context, id string) (Backup, error) {
	var item Backup
	var createdAt int64
	err := m.db.QueryRowContext(ctx, `SELECT id, instance_id, database_name, plan_id, source_name, kind, path, size_bytes, sha256, warning, created_at, created_by_user_id, created_by_username
		FROM mysql_backups WHERE id=?`, id).Scan(&item.ID, &item.InstanceID, &item.Database, &item.PlanID, &item.SourceName, &item.Kind, &item.Path,
		&item.SizeBytes, &item.SHA256, &item.Warning, &createdAt, &item.CreatedByUserID, &item.CreatedByUsername)
	item.CreatedAt = time.Unix(0, createdAt).UTC()
	return item, err
}

func (m *Manager) Operation(ctx context.Context, id string) (Operation, error) {
	var item Operation
	var createdAt, updatedAt int64
	err := m.db.QueryRowContext(ctx, `SELECT id, kind, instance_id, database_name, target_database, backup_id, safety_backup_id,
		phase, bytes_total, bytes_completed, error, rollback_error, cancel_requested, created_at, updated_at, actor_user_id, actor_username
		FROM mysql_operations WHERE id=?`, id).Scan(&item.ID, &item.Kind, &item.InstanceID, &item.Database, &item.TargetDatabase,
		&item.BackupID, &item.SafetyBackupID, &item.Phase, &item.BytesTotal, &item.BytesCompleted, &item.Error,
		&item.RollbackError, &item.CancelRequested, &createdAt, &updatedAt, &item.Actor.UserID, &item.Actor.Username)
	item.CreatedAt, item.UpdatedAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC()
	return item, err
}

func (m *Manager) Operations(ctx context.Context, instanceID string) ([]Operation, error) {
	items, _, err := m.OperationsPage(ctx, instanceID, 0, 0)
	return items, err
}

func (m *Manager) OperationsPage(ctx context.Context, instanceID string, limit, offset int) ([]Operation, int, error) {
	var total int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql_operations WHERE instance_id=?", instanceID).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := "SELECT id FROM mysql_operations WHERE instance_id=? ORDER BY created_at DESC"
	arguments := []any{instanceID}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		arguments = append(arguments, limit, max(offset, 0))
	}
	rows, err := m.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	var result []Operation
	for _, id := range ids {
		item, err := m.Operation(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, item)
	}
	return result, total, nil
}

func IsSystemDatabase(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mysql", "information_schema", "performance_schema", "sys":
		return true
	default:
		return false
	}
}

type boundedBuffer struct {
	data    []byte
	maximum int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	remaining := b.maximum - len(b.data)
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		b.data = append(b.data, value[:remaining]...)
	}
	return len(value), nil
}

func (b *boundedBuffer) String() string { return string(b.data) }

var credentialPattern = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*([^\s;]+)`)

func sanitizedCommandError(value string, secrets ...string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = credentialPattern.ReplaceAllString(value, "$1=[REDACTED]")
	return ": " + value
}

var _ = sql.ErrNoRows
