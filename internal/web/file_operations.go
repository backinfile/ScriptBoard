package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"scriptboard/internal/fileworkflow"
	"scriptboard/internal/hostfiles"
)

type sqliteFileOperationStore = fileworkflow.SQLiteOperationStore

func newSQLiteFileOperationStore(db *sql.DB) *sqliteFileOperationStore {
	return fileworkflow.NewSQLiteOperationStore(db, updateMovedScriptReferences)
}

func (a *App) fileOperationPage(response http.ResponseWriter, request *http.Request) {
	operation, err := a.fileOperations.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		http.Error(response, "文件操作不存在", http.StatusNotFound)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	destination, _ := hostfiles.Parent(operation.DestinationPath)
	if info, _, infoErr := a.hostInfo(request.Context(), operation.DestinationPath); infoErr == nil && info.IsDir() {
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
