package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"scriptboard/internal/hostfiles"
)

type sqliteFileOperationStore struct {
	db *sql.DB
}

func newSQLiteFileOperationStore(db *sql.DB) *sqliteFileOperationStore {
	return &sqliteFileOperationStore{db: db}
}

func (store *sqliteFileOperationStore) Create(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO file_operations
		(id, kind, source_path, source_path_key, destination_path, destination_path_key, temporary_path, trash_path,
		 phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		operation.ID, operation.Kind, operation.SourcePath, operation.SourcePathKey, operation.DestinationPath, operation.DestinationPathKey,
		operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal, operation.BytesCompleted,
		operation.VerificationDigest, operation.Error, operation.CreatedAt.UnixNano(), operation.UpdatedAt.UnixNano())
	return err
}

func (store *sqliteFileOperationStore) Update(ctx context.Context, operation hostfiles.FileOperation) error {
	_, err := store.db.ExecContext(ctx, `UPDATE file_operations SET
		temporary_path = ?, trash_path = ?, phase = ?, bytes_total = ?, bytes_completed = ?, verification_digest = ?, error = ?, updated_at = ?
		WHERE id = ?`, operation.TemporaryPath, operation.TrashPath, operation.Phase, operation.BytesTotal,
		operation.BytesCompleted, operation.VerificationDigest, operation.Error, operation.UpdatedAt.UnixNano(), operation.ID)
	return err
}

func (store *sqliteFileOperationStore) Commit(ctx context.Context, operation hostfiles.FileOperation) error {
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
	if err := updateMovedScriptReferences(transaction, operation.SourcePath, operation.DestinationPath); err != nil {
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

func (store *sqliteFileOperationStore) Pending(ctx context.Context) ([]hostfiles.FileOperation, error) {
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

func (store *sqliteFileOperationStore) CancelRequested(ctx context.Context, id string) (bool, error) {
	var requested bool
	err := store.db.QueryRowContext(ctx, "SELECT cancel_requested FROM file_operations WHERE id = ?", id).Scan(&requested)
	return requested, err
}

func (store *sqliteFileOperationStore) Get(ctx context.Context, id string) (hostfiles.FileOperation, error) {
	return scanFileOperation(store.db.QueryRowContext(ctx, `SELECT id, kind, source_path, source_path_key, destination_path, destination_path_key,
		temporary_path, trash_path, phase, bytes_total, bytes_completed, verification_digest, error, cancel_requested, created_at, updated_at
		FROM file_operations WHERE id = ?`, id))
}

func (store *sqliteFileOperationStore) RequestCancel(ctx context.Context, id string) error {
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

func (a *App) fileOperationPage(response http.ResponseWriter, request *http.Request) {
	operation, err := a.fileOperations.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "文件操作不存在", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	destination, _ := hostfiles.Parent(operation.DestinationPath)
	if info, infoErr := a.files.Info(operation.DestinationPath); infoErr == nil && info.IsDir() {
		destination = operation.DestinationPath
	}
	canCancel := operation.Phase == hostfiles.OperationScanning || operation.Phase == hostfiles.OperationCopying || operation.Phase == hostfiles.OperationReadyToCommit
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fileOperationTemplate.Execute(response, struct {
		Operation      hostfiles.FileOperation
		DestinationURL string
		CSRFToken      string
		CanCancel      bool
		Locale         webLocale
	}{operation, filesURL(destination), current.csrfToken, canCancel, resolveWebLocale(request)})
}

func (a *App) fileOperationStatus(response http.ResponseWriter, request *http.Request) {
	operation, err := a.fileOperations.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "file operation not found", http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(operation)
}

func (a *App) fileOperationEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("X-Accel-Buffering", "no")
	var lastUpdate time.Time
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := a.fileOperations.Get(request.Context(), request.PathValue("id"))
		if err != nil {
			fmt.Fprint(response, "event: error\ndata: {\"error\":\"file operation not found\"}\n\n")
			flusher.Flush()
			return
		}
		if !operation.UpdatedAt.Equal(lastUpdate) {
			payload, _ := json.Marshal(operation)
			fmt.Fprintf(response, "event: progress\ndata: %s\n\n", payload)
			flusher.Flush()
			lastUpdate = operation.UpdatedAt
		}
		if operation.Phase.Terminal() {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) cancelFileOperation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	if err := a.fileOperations.RequestCancel(request.Context(), id); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "cancel_file_operation", id, "accepted")
	http.Redirect(response, request, "/resources/files/operations/"+id, http.StatusSeeOther)
}
