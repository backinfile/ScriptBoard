package web

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/hostfiles"
)

const (
	maxBatchFileSelections = 100
	maxBatchArchiveEntries = 10_000
)

type batchFileItem struct {
	path        string
	info        os.FileInfo
	canMutate   bool
	destination string
}

// canonicalBatchFileItems resolves the whole request before mutation and drops
// descendants already covered by a selected directory, preventing double moves.
func (a *App) canonicalBatchFileItems(ctx context.Context, values []string, requireMutable bool) ([]batchFileItem, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("select at least one entry")
	}
	if len(values) > maxBatchFileSelections {
		return nil, fmt.Errorf("a batch can contain at most %d entries", maxBatchFileSelections)
	}
	items := make([]batchFileItem, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		path, err := a.hostCanonicalExisting(ctx, value)
		if err != nil {
			return nil, err
		}
		key := hostfiles.ComparisonKey(path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		info, canMutate, err := a.hostInfo(ctx, path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("restricted or special entry cannot be used in a batch")
		}
		if requireMutable && !canMutate {
			return nil, fmt.Errorf("entry cannot be changed: %s", path)
		}
		seen[key] = struct{}{}
		items = append(items, batchFileItem{path: path, info: info, canMutate: canMutate})
	}
	sort.SliceStable(items, func(left, right int) bool { return len(items[left].path) < len(items[right].path) })
	result := make([]batchFileItem, 0, len(items))
	for _, item := range items {
		covered := false
		for _, parent := range result {
			if parent.info.IsDir() && hostfiles.Contains(parent.path, item.path) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, item)
		}
	}
	return result, nil
}

type batchArchiveEntry struct {
	path string
	name string
	info os.FileInfo
}

func (a *App) batchArchiveManifest(ctx context.Context, items []batchFileItem) ([]batchArchiveEntry, error) {
	manifest := make([]batchArchiveEntry, 0, len(items))
	names := make(map[string]struct{})
	var walk func(string, string, os.FileInfo) error
	walk = func(path, name string, info os.FileInfo) error {
		if len(manifest) >= maxBatchArchiveEntries {
			return fmt.Errorf("archive contains more than %d entries", maxBatchArchiveEntries)
		}
		name = filepath.ToSlash(name)
		if info.IsDir() {
			name = strings.TrimSuffix(name, "/") + "/"
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("archive contains duplicate path %q", name)
		}
		names[name] = struct{}{}
		manifest = append(manifest, batchArchiveEntry{path: path, name: name, info: info})
		if !info.IsDir() {
			return nil
		}
		children, err := a.hostList(ctx, path)
		if err != nil {
			return err
		}
		for _, child := range children {
			childInfo, _, err := a.hostInfo(ctx, child.Path)
			if err != nil {
				return err
			}
			if !childInfo.IsDir() && !childInfo.Mode().IsRegular() {
				return fmt.Errorf("directory contains a restricted or special entry: %s", child.Path)
			}
			if err := walk(child.Path, filepath.Join(strings.TrimSuffix(name, "/"), child.Name), childInfo); err != nil {
				return err
			}
		}
		return nil
	}
	for _, item := range items {
		if err := walk(item.path, hostfiles.Base(item.path), item.info); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func (a *App) downloadBatchFiles(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	items, err := a.canonicalBatchFileItems(request.Context(), request.Form["path"], false)
	if err != nil {
		writeHostFileError(response, "无法准备批量下载", err)
		return
	}
	manifest, err := a.batchArchiveManifest(request.Context(), items)
	if err != nil {
		writeHostFileError(response, "无法准备批量下载", err)
		return
	}
	temporary, err := os.CreateTemp("", ".scriptboard-files-*.zip")
	if err != nil {
		writeHostFileError(response, "无法准备批量下载", err)
		return
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	_ = temporary.Chmod(0o600)
	// Build the complete archive before starting the response so source or ZIP
	// failures never look like a successful but truncated download.
	err = writeBatchArchive(temporary, manifest, func(path string) (io.ReadCloser, error) {
		file, _, openErr := a.hostOpenRegular(request.Context(), path)
		return file, openErr
	})
	if err != nil {
		a.recordAuditForRequest(request, "download_entries", fmt.Sprintf("%d entries", len(items)), "failed")
		writeHostFileError(response, "无法准备批量下载", err)
		return
	}
	info, err := temporary.Stat()
	if err == nil {
		_, err = temporary.Seek(0, io.SeekStart)
	}
	if err != nil {
		a.recordAuditForRequest(request, "download_entries", fmt.Sprintf("%d entries", len(items)), "failed")
		writeHostFileError(response, "无法准备批量下载", err)
		return
	}
	response.Header().Set("Content-Type", "application/zip")
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "scriptboard-files.zip"}))
	response.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(response, temporary); err != nil {
		a.recordAuditForRequest(request, "download_entries", fmt.Sprintf("%d entries", len(items)), "failed")
		return
	}
	a.recordAuditForRequest(request, "download_entries", fmt.Sprintf("%d entries", len(items)), "succeeded")
}

func writeBatchArchive(destination io.Writer, manifest []batchArchiveEntry, openFile func(string) (io.ReadCloser, error)) (err error) {
	archive := zip.NewWriter(destination)
	defer func() { err = errors.Join(err, archive.Close()) }()
	for _, entry := range manifest {
		header, headerErr := zip.FileInfoHeader(entry.info)
		if headerErr != nil {
			return headerErr
		}
		header.Name = entry.name
		if !entry.info.IsDir() {
			header.Method = zip.Deflate
		}
		target, createErr := archive.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		if entry.info.IsDir() {
			continue
		}
		file, openErr := openFile(entry.path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(target, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
	return nil
}

func (a *App) moveBatchFiles(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	items, err := a.canonicalBatchFileItems(request.Context(), request.Form["path"], true)
	if err != nil {
		writeHostFileError(response, "无法准备批量移动", err)
		return
	}
	destinationDirectory, err := a.hostCanonicalDirectory(request.Context(), request.FormValue("working_directory"))
	if err != nil {
		writeHostFileError(response, "移动目标无效", err)
		return
	}
	leasePaths := make([]string, 0, len(items)*2)
	plannedDestinations := make(map[string]struct{}, len(items))
	for index := range items {
		if a.runs.ConflictsPath(items[index].path) {
			http.Error(response, "活动运行持有所选脚本或其后代的运行租约", http.StatusConflict)
			return
		}
		items[index].destination, err = a.hostDestination(request.Context(), destinationDirectory, hostfiles.Base(items[index].path))
		if err != nil {
			writeHostFileError(response, "移动目标无效", err)
			return
		}
		if hostfiles.ComparisonKey(items[index].path) == hostfiles.ComparisonKey(items[index].destination) {
			http.Error(response, "所选条目已经位于目标目录", http.StatusConflict)
			return
		}
		if items[index].info.IsDir() && hostfiles.Contains(items[index].path, items[index].destination) {
			http.Error(response, "目录不能移动到自身或其子目录", http.StatusConflict)
			return
		}
		destinationKey := hostfiles.ComparisonKey(items[index].destination)
		if _, duplicate := plannedDestinations[destinationKey]; duplicate {
			http.Error(response, "所选条目中存在相同名称，无法移动到同一目录", http.StatusConflict)
			return
		}
		plannedDestinations[destinationKey] = struct{}{}
		if _, _, infoErr := a.hostInfo(request.Context(), items[index].destination); infoErr == nil {
			http.Error(response, "目标目录中已存在同名条目："+hostfiles.Base(items[index].destination), http.StatusConflict)
			return
		} else if !hostFileNotExist(infoErr) {
			writeHostFileError(response, "无法检查移动目标", infoErr)
			return
		}
		sameFilesystem, boundaryErr := a.hostSameFilesystem(request.Context(), items[index].path, items[index].destination)
		if boundaryErr != nil {
			writeHostFileError(response, "无法确定移动边界", boundaryErr)
			return
		}
		if !sameFilesystem {
			http.Error(response, "批量移动暂不支持跨文件系统目标，请在同一文件系统内选择目录", http.StatusBadRequest)
			return
		}
		leasePaths = append(leasePaths, items[index].path, items[index].destination)
	}
	release, err := a.acquireFileMutationLease(leasePaths...)
	if err != nil {
		http.Error(response, "所选路径正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	moved := 0
	rollback := func() error {
		var rollbackErr error
		for index := moved - 1; index >= 0; index-- {
			if err := a.hostMove(request.Context(), items[index].destination, items[index].path); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
		}
		return rollbackErr
	}
	for index := range items {
		if err := a.hostMove(request.Context(), items[index].path, items[index].destination); err != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				http.Error(response, "无法移动全部条目："+err.Error()+"；回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(response, "无法移动全部条目："+err.Error(), http.StatusConflict)
			return
		}
		moved++
	}
	transaction, err := a.db.BeginTx(request.Context(), nil)
	if err == nil {
		for _, item := range items {
			if err = updateMovedScriptReferences(transaction, item.path, item.destination); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if transaction != nil {
			_ = transaction.Rollback()
		}
		rollbackErr := rollback()
		if rollbackErr != nil {
			http.Error(response, "无法同步更新引用："+err.Error()+"；文件回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(response, "无法同步更新引用："+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, item := range items {
		a.recordAuditForRequest(request, "move_entry", item.path+" -> "+item.destination, "succeeded")
	}
	http.Redirect(response, request, filesURL(destinationDirectory), http.StatusSeeOther)
}

func (a *App) trashBatchFiles(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	items, err := a.canonicalBatchFileItems(request.Context(), request.Form["path"], true)
	if err != nil {
		writeHostFileError(response, "无法准备批量回收", err)
		return
	}
	for _, item := range items {
		if a.runs.ConflictsPath(item.path) {
			http.Error(response, "活动运行持有所选脚本或其后代的运行租约", http.StatusConflict)
			return
		}
		externalReferences, countErr := a.countExternalFileReferences(item.path)
		if countErr != nil {
			http.Error(response, "Unable to check External Interface file references", http.StatusInternalServerError)
			return
		}
		if externalReferences != 0 {
			http.Error(response, "A selected path is still referenced by an External Interface file action", http.StatusConflict)
			return
		}
		quickCount, scheduleCount, countErr := a.countScriptReferences(item.path)
		if countErr != nil {
			http.Error(response, "无法检查条目引用", http.StatusInternalServerError)
			return
		}
		if (quickCount > 0 || scheduleCount > 0) && request.FormValue("confirm_references") != "yes" {
			http.Error(response, "所选条目仍被快捷执行或计划引用，请确认后重试", http.StatusConflict)
			return
		}
	}
	paths := make([]string, len(items))
	for index := range items {
		paths[index] = items[index].path
	}
	release, err := a.acquireFileMutationLease(paths...)
	if err != nil {
		http.Error(response, "所选条目正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	ids := make([]string, len(items))
	for index := range ids {
		ids[index], err = randomToken(18)
		if err != nil {
			http.Error(response, "无法创建回收条目", http.StatusInternalServerError)
			return
		}
	}
	transaction, err := a.db.BeginTx(request.Context(), nil)
	if err != nil {
		http.Error(response, "无法开始批量回收", http.StatusInternalServerError)
		return
	}
	trashed := make([]hostfiles.Trashed, 0, len(items))
	rollback := func() error {
		_ = transaction.Rollback()
		var rollbackErr error
		for index := len(trashed) - 1; index >= 0; index-- {
			if err := a.hostRestoreFromTrash(request.Context(), trashed[index].StoredPath, trashed[index].OriginalPath); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
		}
		return rollbackErr
	}
	for index, item := range items {
		moved, moveErr := a.hostMoveToTrash(request.Context(), item.path, ids[index])
		if moveErr != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				http.Error(response, "无法回收全部条目："+moveErr.Error()+"；回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(response, "无法回收全部条目："+moveErr.Error(), http.StatusConflict)
			return
		}
		trashed = append(trashed, moved)
		_, err = transaction.ExecContext(request.Context(), `INSERT INTO trash_entries
			(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, ids[index], moved.OriginalPath, hostfiles.ComparisonKey(moved.OriginalPath), moved.StoredPath,
			hostfiles.ComparisonKey(moved.StoredPath), time.Now().UTC().Unix(), moved.Size, moved.Directory)
		if err == nil {
			err = disableScheduleReferences(transaction, moved.OriginalPath, time.Now().UTC().UnixNano())
		}
		if err != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				http.Error(response, "无法记录批量回收："+err.Error()+"；回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(response, "无法记录批量回收："+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := transaction.Commit(); err != nil {
		rollbackErr := rollback()
		if rollbackErr != nil {
			http.Error(response, "无法提交批量回收："+err.Error()+"；回滚失败："+rollbackErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(response, "无法提交批量回收："+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, moved := range trashed {
		a.recordAuditForRequest(request, "trash_entry", moved.OriginalPath, "succeeded")
	}
	returnDirectory, _ := hostPathParent(items[0].path)
	destination := filesURL(returnDirectory)
	if returnTo := safeFilesReturnTo(request.FormValue("return_to")); returnTo != "" {
		destination = returnTo
	}
	// Keep batch deletion in the originating file workspace so the list refreshes in place.
	http.Redirect(response, request, destination, http.StatusSeeOther)
}
