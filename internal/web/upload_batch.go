package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/secretredaction"
)

// uploadBatchFiles is deliberately separate from the legacy per-file upload
// route. It parses and validates the complete multipart request before asking
// Host Files to perform one all-or-nothing commit.
func (a *App) uploadBatchFiles(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	request.Body = http.MaxBytesReader(response, request.Body, 2<<30)
	if err := request.ParseMultipartForm(32 << 20); err != nil {
		http.Error(response, "无法读取批量上传请求："+secretredaction.String(err.Error()), http.StatusBadRequest)
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	relative := strings.TrimSpace(request.FormValue("path"))
	if relative == "" {
		http.Error(response, "上传目录不能为空", http.StatusBadRequest)
		return
	}
	if _, err := a.hostList(request.Context(), relative); err != nil {
		writeHostFileError(response, "上传目录无效", err)
		return
	}
	action := request.FormValue("conflict_action")
	if !validConflictAction(action) {
		http.Error(response, webText(locale, "upload_results.invalid_conflict_action"), http.StatusBadRequest)
		return
	}
	files := request.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(response, "未选择上传文件", http.StatusBadRequest)
		return
	}
	if len(files) > 100 {
		http.Error(response, "单批最多上传 100 个文件", http.StatusRequestEntityTooLarge)
		return
	}
	relativePaths := request.MultipartForm.Value["relative_path"]
	if len(relativePaths) == 0 {
		relativePaths = make([]string, len(files))
		for index, header := range files {
			relativePaths[index] = header.Filename
		}
	} else if len(relativePaths) != len(files) {
		http.Error(response, "批量上传未执行：文件路径元数据不完整", http.StatusBadRequest)
		return
	}

	inputs := make([]hostfiles.UploadBatchInput, 0, len(files))
	opened := make([]interface{ Close() error }, 0, len(files))
	defer func() {
		for _, file := range opened {
			_ = file.Close()
		}
	}()
	seen := make(map[string]struct{}, len(files))
	for index, header := range files {
		filename := header.Filename
		relativePath := relativePaths[index]
		if err := hostfiles.ValidateRelativePath(relativePath); err != nil || path.Base(relativePath) != filename {
			a.recordAuditForRequest(request, "upload_batch", relativePath, "rejected")
			http.Error(response, fmt.Sprintf("批量上传未执行：文件路径 %q 无效", relativePath), http.StatusBadRequest)
			return
		}
		if _, duplicate := seen[hostfiles.ComparisonKey(relativePath)]; duplicate {
			http.Error(response, fmt.Sprintf("批量上传未执行：重复文件路径 %q", relativePath), http.StatusConflict)
			return
		}
		seen[hostfiles.ComparisonKey(relativePath)] = struct{}{}
		targetPath := filepath.Join(relative, filepath.FromSlash(relativePath))
		targetInfo, _, targetErr := a.hostInfo(request.Context(), targetPath)
		targetExists := targetErr == nil
		// Broker-backed Info wraps a missing destination; unwrap it so a new upload target remains valid.
		if targetErr != nil && !errors.Is(targetErr, os.ErrNotExist) {
			writeHostFileError(response, "无法检查同名文件", targetErr)
			return
		}
		uploadName := relativePath
		if targetExists {
			switch action {
			case conflictActionRename:
				renamed, err := a.hostAvailableName(request.Context(), filepath.Dir(targetPath), filename)
				if err != nil {
					writeHostFileError(response, "无法生成可用名称", err)
					return
				}
				if parent := path.Dir(relativePath); parent != "." {
					uploadName = path.Join(parent, renamed)
				} else {
					uploadName = renamed
				}
				targetPath = filepath.Join(relative, filepath.FromSlash(uploadName))
			case conflictActionOverwrite:
				if !targetInfo.Mode().IsRegular() || a.runs.ConflictsPath(targetPath) {
					http.Error(response, fmt.Sprintf("批量上传未执行：%q 不能被覆盖", filename), http.StatusConflict)
					return
				}
			default:
				http.Error(response, fmt.Sprintf("批量上传未执行：%q 已存在", filename), http.StatusConflict)
				return
			}
		}
		storedID, err := randomToken(18)
		if err != nil {
			http.Error(response, "无法创建批量上传事务", http.StatusInternalServerError)
			return
		}
		file, err := header.Open()
		if err != nil {
			http.Error(response, fmt.Sprintf("无法打开上传文件 %q", filename), http.StatusBadRequest)
			return
		}
		opened = append(opened, file)
		inputs = append(inputs, hostfiles.UploadBatchInput{Name: uploadName, Source: file, MaxBytes: 1 << 30, StoredName: storedID})
	}

	synchronizeQuickRuns := request.FormValue("sync_quick_runs") == "1" && action == conflictActionOverwrite
	results, err := a.hostUploadBatch(request.Context(), relative, inputs, action == conflictActionOverwrite, synchronizeQuickRuns)
	if err != nil {
		a.recordAuditForRequest(request, "upload_batch", fmt.Sprintf("%d files", len(inputs)), "failed")
		http.Error(response, "批量上传失败，目标目录未保留本批次修改："+secretredaction.String(err.Error()), http.StatusConflict)
		return
	}
	type uploadResult struct {
		Name, Result, Detail string
		Succeeded            bool
	}
	views := make([]uploadResult, 0, len(results))
	for index, result := range results {
		a.recordAuditForRequest(request, "upload_file", result.Name, "succeeded")
		detail := webText(locale, "upload_results.saved")
		if result.QuickRunsSynchronized > 0 {
			detail = fmt.Sprintf(webText(locale, "upload_results.saved_quick_runs"), result.QuickRunsSynchronized)
			a.recordAuditResourceForRequest(request, "sync_quick_runs_after_upload", result.Path, "succeeded", "", result.ScriptSHA256)
		} else if result.Name != relativePaths[index] {
			detail = fmt.Sprintf(webText(locale, "upload_results.renamed"), result.Name)
		}
		views = append(views, uploadResult{Name: result.Name, Result: webText(locale, "upload_results.succeeded"), Detail: detail, Succeeded: true})
	}
	a.recordAuditForRequest(request, "upload_batch", fmt.Sprintf("%d files", len(results)), "succeeded")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uploadResultsTemplate.Execute(response, struct {
		Link    string
		Results []uploadResult
		Locale  webLocale
	}{Link: filesURL(relative), Results: views, Locale: locale}); err != nil {
		http.Error(response, "文件已上传，但无法呈现结果："+err.Error(), http.StatusInternalServerError)
	}
}
