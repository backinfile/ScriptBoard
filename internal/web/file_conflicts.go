package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"scriptboard/internal/hostfiles"
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

// restoreTrackedTrash compensates a temporary overwrite or delete. The
// database row is deliberately removed only after the filesystem entry is
// back at its original path. If removing the row fails, the entry is moved
// back to the same owned trash path so the database and filesystem still
// describe a recoverable state.
func (a *App) restoreTrackedTrash(ctx context.Context, id string, trashed hostfiles.Trashed) error {
	if err := a.hostRestoreFromTrash(ctx, trashed.StoredPath, trashed.OriginalPath); err != nil {
		return err
	}
	if _, err := a.db.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		rolledBack, rollbackErr := a.hostMoveToTrash(ctx, trashed.OriginalPath, id)
		if rollbackErr != nil {
			return fmt.Errorf("remove restored trash record: %w; move restored entry back to trash: %v", err, rollbackErr)
		}
		if hostfiles.ComparisonKey(rolledBack.StoredPath) != hostfiles.ComparisonKey(trashed.StoredPath) {
			return fmt.Errorf("remove restored trash record: %w; entry returned to unexpected trash path %s", err, rolledBack.StoredPath)
		}
		return fmt.Errorf("remove restored trash record: %w", err)
	}
	return nil
}

func parentAndName(path string) (string, string) {
	parent, _ := hostfiles.Parent(path)
	return parent, hostfiles.Base(path)
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
	relative := request.FormValue("path")
	if _, err := a.hostList(request.Context(), relative); err != nil {
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
		if err := hostfiles.ValidateName(name); err != nil {
			http.Error(response, "文件名无效："+err.Error(), http.StatusBadRequest)
			return
		}
		target, err := a.hostDestination(request.Context(), relative, name)
		if err != nil {
			http.Error(response, "上传目标无效："+err.Error(), http.StatusBadRequest)
			return
		}
		info, _, err := a.hostInfo(request.Context(), target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			http.Error(response, "无法检查同名文件："+err.Error(), http.StatusBadRequest)
			return
		}
		suggested, err := a.hostAvailableName(request.Context(), relative, name)
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

func (a *App) commitTrashRestore(ctx context.Context, id, stored, destination string, overwrite bool) error {
	transaction, err := a.db.Begin()
	if err != nil {
		return err
	}
	var displaced *hostfiles.Trashed
	restored := false
	rollback := func() error {
		var rollbackErr error
		if restored {
			if _, err := a.hostMoveToTrash(ctx, destination, id); err != nil {
				rollbackErr = fmt.Errorf("return restored entry to trash: %w", err)
			}
		}
		if displaced != nil && rollbackErr == nil {
			if err := a.hostRestoreFromTrash(ctx, displaced.StoredPath, displaced.OriginalPath); err != nil {
				rollbackErr = fmt.Errorf("restore overwritten entry: %w", err)
			}
		}
		_ = transaction.Rollback()
		return rollbackErr
	}
	if overwrite {
		displacedID, tokenErr := randomToken(18)
		if tokenErr != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("无法创建覆盖事务: %w", tokenErr)
		}
		moved, moveErr := a.hostMoveToTrash(ctx, destination, displacedID)
		if moveErr != nil {
			_ = transaction.Rollback()
			return moveErr
		}
		displaced = &moved
		if _, err = transaction.Exec(
			`INSERT INTO trash_entries
				(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			displacedID, moved.OriginalPath, hostfiles.ComparisonKey(moved.OriginalPath), moved.StoredPath,
			hostfiles.ComparisonKey(moved.StoredPath), time.Now().UTC().Unix(), moved.Size, moved.Directory,
		); err != nil {
			_ = rollback()
			return fmt.Errorf("无法记录被覆盖的条目: %w", err)
		}
	}
	if err := a.hostRestoreFromTrash(ctx, stored, destination); err != nil {
		_ = rollback()
		return err
	}
	restored = true
	if _, err := transaction.Exec("DELETE FROM trash_entries WHERE id = ?", id); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return fmt.Errorf("无法更新回收站记录: %w; 回滚失败: %v", err, rollbackErr)
		}
		return fmt.Errorf("无法更新回收站记录: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return fmt.Errorf("无法提交回收站恢复: %w; 回滚失败: %v", err, rollbackErr)
		}
		return fmt.Errorf("无法提交回收站恢复: %w", err)
	}
	return nil
}
