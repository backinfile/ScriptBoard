package app

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
)

type quickRunGroup struct {
	ID            string
	Name          string
	SortOrder     int
	QuickRunCount int
	Items         []quickRunView
	Ungrouped     bool
}

type quickRunRecord struct {
	quickRunView
	SourceRunID sql.NullString
	SortOrder   int
}

func (a *App) loadQuickRun(id string) (quickRunRecord, error) {
	var quick quickRunRecord
	var groupID sql.NullString
	err := a.db.QueryRow(`SELECT id, name, script_path, arguments_template, timeout_seconds,
		source_run_id, sort_order, group_id, locked
		FROM quick_runs WHERE id = ?`, id).Scan(
		&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds,
		&quick.SourceRunID, &quick.SortOrder, &groupID, &quick.Locked,
	)
	if groupID.Valid {
		quick.GroupID = groupID.String
	}
	return quick, err
}

func (a *App) parseQuickRunEditableRequest(request *http.Request) (string, string, int, error) {
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		return "", "", 0, errors.New("快捷执行名称无效")
	}
	timeoutSeconds := 0
	if value := request.FormValue("timeout_seconds"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 24*60*60 {
			return "", "", 0, errors.New("超时必须是 0 到 86400 秒")
		}
		timeoutSeconds = parsed
	}
	arguments := request.FormValue("arguments")
	variables, err := a.loadVariables()
	if err != nil {
		return "", "", 0, fmt.Errorf("读取变量: %w", err)
	}
	if err := runmanager.ValidateArgumentsTemplate(arguments, variables); err != nil {
		return "", "", 0, fmt.Errorf("参数无效: %w", err)
	}
	return name, arguments, timeoutSeconds, nil
}

func (a *App) resolveQuickRunGroupID(value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var exists int
	if err := a.db.QueryRow("SELECT EXISTS(SELECT 1 FROM quick_run_groups WHERE id = ?)", value).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, sql.ErrNoRows
	}
	return &value, nil
}

func (a *App) loadQuickRunGroups() ([]quickRunGroup, error) {
	rows, err := a.db.Query(`SELECT g.id, g.name, g.sort_order, COUNT(q.id)
		FROM quick_run_groups g
		LEFT JOIN quick_runs q ON q.group_id = g.id
		GROUP BY g.id, g.name, g.sort_order, g.created_at
		ORDER BY g.sort_order, g.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []quickRunGroup
	for rows.Next() {
		var group quickRunGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder, &group.QuickRunCount); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (a *App) newQuickRunGroupTask(response http.ResponseWriter, request *http.Request) {
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "quick-group-new",
		Title:       webText(resolveWebLocale(request), "task.quick_group_new.title"),
		Description: webText(resolveWebLocale(request), "task.quick_group.description"),
		BackURL:     "/config/quick-runs",
		Action:      "/config/quick-runs/groups",
	})
}

func (a *App) editQuickRunGroupTask(response http.ResponseWriter, request *http.Request) {
	var name string
	id := request.PathValue("id")
	if err := a.db.QueryRow("SELECT name FROM quick_run_groups WHERE id = ?", id).Scan(&name); err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusNotFound)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "quick-group-edit",
		Title:       webText(resolveWebLocale(request), "task.quick_group_edit.title"),
		Description: webText(resolveWebLocale(request), "task.quick_group.description"),
		BackURL:     "/config/quick-runs",
		Action:      "/config/quick-runs/groups/" + id + "/update",
		Name:        name,
	})
}

func (a *App) createQuickRunGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行分组名称无效", http.StatusBadRequest)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建快捷执行分组", http.StatusInternalServerError)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法创建快捷执行分组", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	var sortOrder int
	if err = transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_run_groups").Scan(&sortOrder); err == nil {
		now := time.Now().UTC().Unix()
		_, err = transaction.Exec(`INSERT INTO quick_run_groups (id, name, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, id, name, sortOrder, now, now)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "快捷执行分组名称已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法创建快捷执行分组", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_quick_run_group", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) updateQuickRunGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "快捷执行分组名称无效", http.StatusBadRequest)
		return
	}
	id := request.PathValue("id")
	result, err := a.db.Exec(`UPDATE quick_run_groups SET name = ?, updated_at = ? WHERE id = ?`,
		name, time.Now().UTC().Unix(), id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "快捷执行分组名称已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法更新快捷执行分组", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(response, "快捷执行分组不存在", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "update_quick_run_group", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) moveQuickRunGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	operator, order := "<", "DESC"
	switch request.FormValue("direction") {
	case "up":
	case "down":
		operator, order = ">", "ASC"
	default:
		http.Error(response, "排序方向无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法调整快捷执行分组顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	var currentOrder int
	if err := transaction.QueryRow("SELECT sort_order FROM quick_run_groups WHERE id = ?", id).Scan(&currentOrder); err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusNotFound)
		return
	}
	var neighborID string
	var neighborOrder int
	query := "SELECT id, sort_order FROM quick_run_groups WHERE sort_order " + operator + " ? ORDER BY sort_order " + order + " LIMIT 1"
	if scanErr := transaction.QueryRow(query, currentOrder).Scan(&neighborID, &neighborOrder); scanErr == nil {
		_, err = transaction.Exec(`UPDATE quick_run_groups
			SET sort_order = CASE id WHEN ? THEN ? WHEN ? THEN ? END, updated_at = ?
			WHERE id IN (?, ?)`,
			id, neighborOrder, neighborID, currentOrder, time.Now().UTC().Unix(), id, neighborID)
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		err = scanErr
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法调整快捷执行分组顺序", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_quick_run_group", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) deleteQuickRunGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除快捷执行分组需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法删除快捷执行分组", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	var exists int
	if err := transaction.QueryRow("SELECT EXISTS(SELECT 1 FROM quick_run_groups WHERE id = ?)", id).Scan(&exists); err != nil || exists == 0 {
		http.Error(response, "快捷执行分组不存在", http.StatusNotFound)
		return
	}
	var ungroupedOrder int
	if err := transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) FROM quick_runs WHERE group_id IS NULL").Scan(&ungroupedOrder); err != nil {
		http.Error(response, "无法迁移快捷执行", http.StatusInternalServerError)
		return
	}
	rows, err := transaction.Query("SELECT id FROM quick_runs WHERE group_id = ? ORDER BY sort_order, created_at", id)
	if err != nil {
		http.Error(response, "无法迁移快捷执行", http.StatusInternalServerError)
		return
	}
	var quickRunIDs []string
	for rows.Next() {
		var quickRunID string
		if err := rows.Scan(&quickRunID); err != nil {
			_ = rows.Close()
			http.Error(response, "无法迁移快捷执行", http.StatusInternalServerError)
			return
		}
		quickRunIDs = append(quickRunIDs, quickRunID)
	}
	_ = rows.Close()
	now := time.Now().UTC().Unix()
	for index, quickRunID := range quickRunIDs {
		if _, err = transaction.Exec(`UPDATE quick_runs SET group_id = NULL, sort_order = ?, updated_at = ? WHERE id = ?`,
			ungroupedOrder+index+1, now, quickRunID); err != nil {
			http.Error(response, "无法迁移快捷执行", http.StatusInternalServerError)
			return
		}
	}
	if _, err = transaction.Exec("DELETE FROM quick_run_groups WHERE id = ?", id); err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法删除快捷执行分组", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "delete_quick_run_group", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) moveQuickRunToGroupTask(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var name string
	var groupID sql.NullString
	if err := a.db.QueryRow("SELECT name, group_id FROM quick_runs WHERE id = ?", id).Scan(&name, &groupID); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "无法读取快捷执行分组", http.StatusInternalServerError)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "quick-move-group",
		Title:       webText(resolveWebLocale(request), "task.quick_move_group.title"),
		Description: webText(resolveWebLocale(request), "task.quick_move_group.description"),
		BackURL:     "/config/quick-runs",
		Action:      "/config/quick-runs/" + id + "/move-group",
		Name:        name,
		GroupID:     groupID.String,
		Groups:      groups,
	})
}

func (a *App) moveQuickRunToGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法移动快捷执行", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	var currentGroupID sql.NullString
	if err := transaction.QueryRow("SELECT group_id FROM quick_runs WHERE id = ?", id).Scan(&currentGroupID); err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	sameGroup := !currentGroupID.Valid && groupID == nil
	if currentGroupID.Valid && groupID != nil {
		sameGroup = currentGroupID.String == *groupID
	}
	if sameGroup {
		http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
		return
	}
	var sortOrder int
	if err = transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM quick_runs WHERE group_id IS ?", groupID).Scan(&sortOrder); err == nil {
		_, err = transaction.Exec(`UPDATE quick_runs SET group_id = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
			groupID, sortOrder, time.Now().UTC().Unix(), id)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法移动快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_quick_run_group_membership", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) editQuickRunTask(response http.ResponseWriter, request *http.Request) {
	quick, err := a.loadQuickRun(request.PathValue("id"))
	if err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	if quick.Locked {
		http.Error(response, "快捷执行已锁定，请先解锁", http.StatusConflict)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:           "quick-edit",
		Title:          webText(resolveWebLocale(request), "task.quick_edit.title"),
		Description:    webText(resolveWebLocale(request), "task.quick_edit.description"),
		BackURL:        "/config/quick-runs",
		Action:         "/config/quick-runs/" + quick.ID + "/update",
		Path:           quick.ScriptPath,
		Name:           quick.Name,
		Arguments:      quick.ArgumentsTemplate,
		TimeoutSeconds: quick.TimeoutSeconds,
	})
}

func (a *App) updateQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name, arguments, timeoutSeconds, err := a.parseQuickRunEditableRequest(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	id := request.PathValue("id")
	result, err := a.db.Exec(`UPDATE quick_runs
		SET name = ?, arguments_template = ?, timeout_seconds = ?, updated_at = ?
		WHERE id = ? AND locked = 0`,
		name, arguments, timeoutSeconds, time.Now().UTC().Unix(), id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil {
		http.Error(response, "无法更新快捷执行", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		if quick, loadErr := a.loadQuickRun(id); loadErr == nil && quick.Locked {
			http.Error(response, "快捷执行已锁定，请先解锁", http.StatusConflict)
			return
		}
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "update_quick_run", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func quickRunCopyName(name string, locale webLocale) string {
	suffix := " copy"
	if locale == localeSimplifiedChinese {
		suffix = "（副本）"
	}
	maxNameBytes := 256 - len([]byte(suffix))
	for len([]byte(name)) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name + suffix
}

func (a *App) copyQuickRunTask(response http.ResponseWriter, request *http.Request) {
	quick, err := a.loadQuickRun(request.PathValue("id"))
	if err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	groups, err := a.loadQuickRunGroups()
	if err != nil {
		http.Error(response, "无法读取快捷执行分组", http.StatusInternalServerError)
		return
	}
	locale := resolveWebLocale(request)
	a.renderTaskPage(response, request, taskPageData{
		Kind:           "quick-copy",
		Title:          webText(locale, "task.quick_copy.title"),
		Description:    webText(locale, "task.quick_copy.description"),
		BackURL:        "/config/quick-runs",
		Action:         "/config/quick-runs/" + quick.ID + "/copy",
		Path:           quick.ScriptPath,
		Name:           quickRunCopyName(quick.Name, locale),
		Arguments:      quick.ArgumentsTemplate,
		TimeoutSeconds: quick.TimeoutSeconds,
		GroupID:        quick.GroupID,
		Groups:         groups,
	})
}

func (a *App) createQuickRunCopy(source quickRunRecord, name, arguments string, timeoutSeconds int, groupID *string) (string, error) {
	id, err := randomToken(18)
	if err != nil {
		return "", err
	}
	transaction, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	var sourceGroup any
	if source.GroupID != "" {
		sourceGroup = source.GroupID
	}
	var targetGroup any
	if groupID != nil {
		targetGroup = *groupID
	}
	sortOrder := 0
	if sourceGroup == targetGroup {
		sortOrder = source.SortOrder + 1
		if _, err = transaction.Exec(`UPDATE quick_runs SET sort_order = sort_order + 1
			WHERE group_id IS ? AND sort_order > ?`, targetGroup, source.SortOrder); err != nil {
			return "", err
		}
	} else if err = transaction.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) + 1
		FROM quick_runs WHERE group_id IS ?`, targetGroup).Scan(&sortOrder); err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	if _, err = transaction.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id,
		sort_order, created_at, group_id, locked, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, name, source.ScriptPath, hostfiles.ComparisonKey(source.ScriptPath), arguments, timeoutSeconds, source.SourceRunID,
		sortOrder, now, targetGroup, now); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (a *App) copyQuickRun(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	source, err := a.loadQuickRun(request.PathValue("id"))
	if err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	name, arguments, timeoutSeconds, err := a.parseQuickRunEditableRequest(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRunCopy(source, name, arguments, timeoutSeconds, groupID)
	if err != nil {
		http.Error(response, "无法复制快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "copy_quick_run", id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}

func (a *App) setQuickRunLocked(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	value := request.FormValue("locked")
	if value != "0" && value != "1" {
		http.Error(response, "锁定状态无效", http.StatusBadRequest)
		return
	}
	locked := value == "1"
	id := request.PathValue("id")
	result, err := a.db.Exec(`UPDATE quick_runs SET locked = ?, updated_at = ? WHERE id = ?`,
		locked, time.Now().UTC().Unix(), id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err != nil {
		http.Error(response, "无法更改快捷执行锁定状态", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	action := "unlock_quick_run"
	if locked {
		action = "lock_quick_run"
	}
	a.recordAuditForRequest(request, action, id, "succeeded")
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}
