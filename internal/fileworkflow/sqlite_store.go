// Package fileworkflow owns recoverable filesystem operation persistence and
// commit coordination.
package fileworkflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/secretredaction"
)

type SQLiteOperationStore struct {
	db                          *sql.DB
	updateMovedScriptReferences func(*sql.Tx, string, string) error
}

func NewSQLiteOperationStore(db *sql.DB, updateMovedScriptReferences func(*sql.Tx, string, string) error) *SQLiteOperationStore {
	return &SQLiteOperationStore{db: db, updateMovedScriptReferences: updateMovedScriptReferences}
}

func (store *SQLiteOperationStore) Create(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO file_operations
		(id, kind, source_path, source_path_key, destination_path, destination_path_key, temporary_path, trash_path,
		 phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		operation.ID, operation.Kind, operation.SourcePath, operation.SourcePathKey, operation.DestinationPath, operation.DestinationPathKey,
		operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal, operation.BytesCompleted,
		operation.VerificationDigest, secretredaction.String(operation.Error), operation.CreatedAt.UnixNano(), operation.UpdatedAt.UnixNano())
	return err
}

func (store *SQLiteOperationStore) Update(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `UPDATE file_operations SET
		temporary_path = ?, trash_path = ?, phase = ?, bytes_total = ?, bytes_completed = ?, verification_digest = ?, error = ?, updated_at = ?
		WHERE id = ?`, operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal,
		operation.BytesCompleted, operation.VerificationDigest, secretredaction.String(operation.Error), operation.UpdatedAt.UnixNano(), operation.ID)
	return err
}

func (store *SQLiteOperationStore) Commit(ctx context.Context, operation hostfiles.FileOperation) error {
	trashInfo, err := os.Lstat(operation.TrashPath)
	if err != nil {
		return fmt.Errorf("inspect moved source trash: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO trash_entries
		(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, operation.ID, operation.SourcePath, operation.SourcePathKey,
		operation.TrashPath, hostfiles.ComparisonKey(operation.TrashPath), operation.UpdatedAt.Unix(),
		trashInfo.Size(), trashInfo.IsDir()); err != nil {
		return fmt.Errorf("register moved source trash: %w", err)
	}
	var originalPathKey, storedPathKey string
	if err := transaction.QueryRowContext(ctx, `SELECT original_path_key, stored_path_key FROM trash_entries WHERE id = ?`, operation.ID).
		Scan(&originalPathKey, &storedPathKey); err != nil {
		return fmt.Errorf("verify moved source trash registration: %w", err)
	}
	if originalPathKey != operation.SourcePathKey || storedPathKey != hostfiles.ComparisonKey(operation.TrashPath) {
		return errors.New("file operation ID belongs to a different trash entry")
	}
	if store.updateMovedScriptReferences == nil {
		return errors.New("file operation reference updater is unavailable")
	}
	if err := store.updateMovedScriptReferences(transaction, operation.SourcePath, operation.DestinationPath); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE file_operations SET phase = ?, temporary_path = ?, trash_path = ?,
		bytes_total = ?, bytes_completed = ?, verification_digest = ?, error = '', updated_at = ? WHERE id = ?`,
		hostfiles.OperationCompleted, operation.TemporaryPath, operation.TrashPath, operation.BytesTotal,
		operation.BytesCompleted, operation.VerificationDigest, operation.UpdatedAt.UnixNano(), operation.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return transaction.Commit()
}

func (store *SQLiteOperationStore) Pending(ctx context.Context) ([]hostfiles.FileOperation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, kind, source_path, source_path_key, destination_path, destination_path_key,
		temporary_path, trash_path, phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at
		FROM file_operations WHERE phase NOT IN ('completed', 'rolled_back', 'cancelled', 'failed') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var operations []hostfiles.FileOperation
	for rows.Next() {
		operation, err := scanFileOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (store *SQLiteOperationStore) CancelRequested(ctx context.Context, id string) (bool, error) {
	var requested bool
	err := store.db.QueryRowContext(ctx, "SELECT cancel_requested FROM file_operations WHERE id = ?", id).Scan(&requested)
	return requested, err
}

func (store *SQLiteOperationStore) Get(ctx context.Context, id string) (hostfiles.FileOperation, error) {
	return scanFileOperation(store.db.QueryRowContext(ctx, `SELECT id, kind, source_path, source_path_key, destination_path, destination_path_key,
		temporary_path, trash_path, phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at
		FROM file_operations WHERE id = ?`, id))
}

func (store *SQLiteOperationStore) RequestCancel(ctx context.Context, id string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE file_operations SET cancel_requested = 1, updated_at = ?
		WHERE id = ? AND phase IN ('scanning', 'copying', 'ready_to_commit')`, time.Now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return errors.New("file operation can no longer be cancelled")
	}
	return nil
}

type fileOperationScanner interface {
	Scan(...any) error
}

func scanFileOperation(scanner fileOperationScanner) (hostfiles.FileOperation, error) {
	var operation hostfiles.FileOperation
	var createdAt, updatedAt int64
	err := scanner.Scan(&operation.ID, &operation.Kind, &operation.SourcePath, &operation.SourcePathKey,
		&operation.DestinationPath, &operation.DestinationPathKey, &operation.TemporaryPath, &operation.TrashPath,
		&operation.Phase, &operation.BytesTotal, &operation.BytesCompleted, &operation.VerificationDigest,
		&operation.Error, &operation.CancelRequested, &createdAt, &updatedAt)
	if err != nil {
		return hostfiles.FileOperation{}, err
	}
	operation.CreatedAt = time.Unix(0, createdAt).UTC()
	operation.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return operation, nil
}
