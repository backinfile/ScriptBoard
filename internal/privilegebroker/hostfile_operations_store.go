package privilegebroker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/secretredaction"
)

type brokerHostFileOperationStore struct{ db *sql.DB }

func NewBrokerHostFileOperationStore(db *sql.DB) (hostfiles.OperationStore, error) {
	if db == nil {
		return nil, errors.New("Broker Host Files operation database is required")
	}
	return &brokerHostFileOperationStore{db: db}, nil
}

func (store *brokerHostFileOperationStore) Create(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO file_operations
		(id, kind, source_path, source_path_key, destination_path, destination_path_key, temporary_path, trash_path,
		 phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		operation.ID, operation.Kind, operation.SourcePath, operation.SourcePathKey, operation.DestinationPath, operation.DestinationPathKey,
		operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal, operation.BytesCompleted,
		operation.VerificationDigest, secretredaction.String(operation.Error), operation.CreatedAt.UnixNano(), operation.UpdatedAt.UnixNano())
	return err
}

func (store *brokerHostFileOperationStore) Update(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `UPDATE file_operations SET temporary_path = ?, trash_path = ?, phase = ?,
		bytes_total = ?, bytes_completed = ?, verification_digest = ?, error = ?, updated_at = ? WHERE id = ?`,
		operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal, operation.BytesCompleted,
		operation.VerificationDigest, secretredaction.String(operation.Error), operation.UpdatedAt.UnixNano(), operation.ID)
	return err
}

func (store *brokerHostFileOperationStore) Commit(ctx context.Context, operation hostfiles.FileOperation) error {
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
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, operation.ID, operation.SourcePath,
		operation.SourcePathKey, operation.TrashPath, hostfiles.ComparisonKey(operation.TrashPath), operation.UpdatedAt.Unix(),
		trashInfo.Size(), trashInfo.IsDir()); err != nil {
		return err
	}
	if err := updateBrokerMovedFileReferences(ctx, transaction, operation.SourcePath, operation.DestinationPath); err != nil {
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

func (store *brokerHostFileOperationStore) Pending(ctx context.Context) ([]hostfiles.FileOperation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, kind, source_path, source_path_key, destination_path, destination_path_key,
		temporary_path, trash_path, phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at
		FROM file_operations WHERE phase NOT IN ('completed', 'rolled_back', 'cancelled', 'failed') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var operations []hostfiles.FileOperation
	for rows.Next() {
		operation, err := scanBrokerHostFileOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (store *brokerHostFileOperationStore) CancelRequested(ctx context.Context, id string) (bool, error) {
	var requested bool
	err := store.db.QueryRowContext(ctx, "SELECT cancel_requested FROM file_operations WHERE id = ?", id).Scan(&requested)
	return requested, err
}

type brokerHostFileOperationScanner interface{ Scan(...any) error }

func scanBrokerHostFileOperation(scanner brokerHostFileOperationScanner) (hostfiles.FileOperation, error) {
	var operation hostfiles.FileOperation
	var createdAt, updatedAt int64
	err := scanner.Scan(&operation.ID, &operation.Kind, &operation.SourcePath, &operation.SourcePathKey,
		&operation.DestinationPath, &operation.DestinationPathKey, &operation.TemporaryPath, &operation.TrashPath,
		&operation.Phase, &operation.BytesTotal, &operation.BytesCompleted, &operation.VerificationDigest,
		&operation.Error, &operation.CancelRequested, &createdAt, &updatedAt)
	operation.CreatedAt, operation.UpdatedAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC()
	return operation, err
}

func updateBrokerMovedFileReferences(ctx context.Context, transaction *sql.Tx, source, destination string) error {
	for _, table := range []string{"quick_runs", "schedules"} {
		query := "SELECT id, script_path FROM " + table
		if table == "schedules" {
			query += " WHERE deleted = 0"
		}
		rows, err := transaction.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		var references [][2]string
		for rows.Next() {
			var id, path string
			if err := rows.Scan(&id, &path); err != nil {
				rows.Close()
				return err
			}
			if hostfiles.Contains(source, path) {
				references = append(references, [2]string{id, path})
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, reference := range references {
			moved, err := hostfiles.Rebase(source, destination, reference[1])
			if err != nil {
				return err
			}
			update := "UPDATE " + table + " SET script_path = ?, script_path_key = ? WHERE id = ?"
			if table == "quick_runs" {
				update = "UPDATE quick_runs SET script_path = ?, script_path_key = ?, revision = revision + 1, updated_at = unixepoch() WHERE id = ?"
			}
			if _, err := transaction.ExecContext(ctx, update, moved, hostfiles.ComparisonKey(moved), reference[0]); err != nil {
				return err
			}
		}
	}
	rows, err := transaction.QueryContext(ctx, "SELECT id, action_type, target, config_json FROM external_trigger_entries WHERE action_type IN ('log', 'upload')")
	if err != nil {
		return err
	}
	type reference struct{ id, action, target, config string }
	var references []reference
	for rows.Next() {
		var value reference
		if err := rows.Scan(&value.id, &value.action, &value.target, &value.config); err != nil {
			rows.Close()
			return err
		}
		if hostfiles.Contains(source, value.target) {
			references = append(references, value)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, reference := range references {
		moved, err := hostfiles.Rebase(source, destination, reference.target)
		if err != nil {
			return err
		}
		var normalized any
		if reference.action == string(externaltrigger.ActionUpload) {
			var config externaltrigger.UploadConfig
			if err := json.Unmarshal([]byte(reference.config), &config); err != nil {
				return err
			}
			config.Directory, normalized = moved, config
		} else {
			var config externaltrigger.LogConfig
			if err := json.Unmarshal([]byte(reference.config), &config); err != nil {
				return err
			}
			config.File, normalized = moved, config
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "UPDATE external_trigger_entries SET target = ?, config_json = ?, updated_at = unixepoch() WHERE id = ?", moved, string(encoded), reference.id); err != nil {
			return err
		}
	}
	return nil
}
