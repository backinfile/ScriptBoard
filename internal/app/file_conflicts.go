package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/managedfiles"
)

const (
	conflictActionSkip      = "skip"
	conflictActionOverwrite = "overwrite"
	conflictActionRename    = "rename"
)

type fileConflictView struct {
	Locale                     webLocale
	Action, BackURL, CSRFToken string
	ID, Source, Destination    string
	ItemPath, SuggestedName    string
	CanOverwrite               bool
}

func (a *App) renderFileConflict(response http.ResponseWriter, request *http.Request, view fileConflictView) {
	current := request.Context().Value(sessionContextKey).(session)
	view.Locale = resolveWebLocale(request)
	view.CSRFToken = current.csrfToken
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusConflict)
	_ = fileConflictTemplate.Execute(response, view)
}

func parentAndName(relative string) (string, string) {
	clean := pathpkg.Clean(filepath.ToSlash(relative))
	parent := pathpkg.Dir(clean)
	if parent == "." {
		parent = ""
	}
	return parent, pathpkg.Base(clean)
}

func childPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return pathpkg.Join(parent, name)
}

func validConflictAction(value string) bool {
	switch value {
	case "", conflictActionSkip, conflictActionOverwrite, conflictActionRename:
		return true
	default:
		return false
	}
}

type uploadConflictItem struct {
	Name         string `json:"name"`
	Suggested    string `json:"suggested"`
	CanOverwrite bool   `json:"canOverwrite"`
}

func (a *App) uploadConflicts(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "无法读取冲突检查请求", http.StatusBadRequest)
		return
	}
	relative := strings.Trim(request.FormValue("path"), "/")
	if _, err := a.managed.List(relative); err != nil {
		http.Error(response, "上传目录无效："+err.Error(), http.StatusBadRequest)
		return
	}
	names := request.Form["name"]
	if len(names) == 0 || len(names) > 100 {
		http.Error(response, "请选择 1 到 100 个文件", http.StatusBadRequest)
		return
	}
	conflicts := make([]uploadConflictItem, 0)
	for _, name := range names {
		if err := managedfiles.ValidateName(name); err != nil {
			http.Error(response, "文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		target := childPath(relative, name)
		info, err := a.managed.Info(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			http.Error(response, "无法检查同名文件："+err.Error(), http.StatusBadRequest)
			return
		}
		suggested, err := a.managed.AvailableName(relative, name)
		if err != nil {
			http.Error(response, "无法生成可用名称："+err.Error(), http.StatusBadRequest)
			return
		}
		conflicts = append(conflicts, uploadConflictItem{
			Name: name, Suggested: suggested,
			CanOverwrite: info.Mode().IsRegular() && !a.runs.ConflictsPath(target),
		})
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(struct {
		Conflicts []uploadConflictItem `json:"conflicts"`
	}{Conflicts: conflicts})
}

func (a *App) commitTrashRestore(id, stored, destination string, overwrite bool) error {
	transaction, err := a.db.Begin()
	if err != nil {
		return err
	}
	var displaced *managedfiles.Trashed
	restored := false
	rollback := func() {
		if restored {
			_, _ = a.managed.MoveToTrash(destination, stored)
		}
		if displaced != nil {
			_ = a.managed.RestoreFromTrash(displaced.StoredName, displaced.OriginalPath)
		}
		_ = transaction.Rollback()
	}
	if overwrite {
		displacedID, tokenErr := randomToken(18)
		if tokenErr != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("无法创建覆盖事务: %w", tokenErr)
		}
		moved, moveErr := a.managed.MoveToTrash(destination, displacedID)
		if moveErr != nil {
			_ = transaction.Rollback()
			return moveErr
		}
		displaced = &moved
		if _, err = transaction.Exec(
			"INSERT INTO trash_entries (id, original_path, stored_name, deleted_at, size, is_directory) VALUES (?, ?, ?, ?, ?, ?)",
			displacedID, moved.OriginalPath, moved.StoredName, time.Now().UTC().Unix(), moved.Size, moved.Directory,
		); err != nil {
			rollback()
			return fmt.Errorf("无法记录被覆盖的条目: %w", err)
		}
	}
	if err := a.managed.RestoreFromTrash(stored, destination); err != nil {
		rollback()
		return err
	}
	restored = true
	if _, err := transaction.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		rollback()
		return fmt.Errorf("无法更新回收站记录: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		rollback()
		return fmt.Errorf("无法提交回收站恢复: %w", err)
	}
	return nil
}
