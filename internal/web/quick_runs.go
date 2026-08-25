package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/externaltrigger"
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

func (a *App) recordQuickRunAuditForRequest(request *http.Request, action, id, result string) {
	quick, err := a.loadQuickRun(id)
	if err != nil {
		a.recordAuditForRequest(request, action, id, result)
		return
	}
	a.recordAuditResourceForRequest(request, action, id, result, strconv.FormatInt(quick.Revision, 10), quick.ScriptSHA256)
}

type quickRunRecord struct {
	quickRunView
	SourceRunID sql.NullString
	SortOrder   int
}

func (a *App) loadQuickRunHistory(quickRuns []quickRunView, locale webLocale) error {
	if len(quickRuns) == 0 {
		return nil
	}
	byID := make(map[string]*quickRunView, len(quickRuns))
	for index := range quickRuns {
		byID[quickRuns[index].ID] = &quickRuns[index]
	}
	rows, err := a.db.Query(`
		SELECT source_id, id, status, started_at, finished_at
		FROM (
			SELECT runs.source_id AS source_id, runs.id AS id, runs.status AS status,
				runs.started_at AS started_at, runs.finished_at AS finished_at,
				ROW_NUMBER() OVER (PARTITION BY runs.source_id ORDER BY runs.created_at DESC, runs.id DESC) AS position
			FROM runs
			JOIN quick_runs ON quick_runs.id = runs.source_id
			WHERE source_type IN ('admin/quick-run', 'quick_run')
		)
		WHERE position <= 5
		ORDER BY source_id, position`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loadedAt := time.Now().UnixNano()
	for rows.Next() {
		var sourceID, runID, status string
		var startedAt, finishedAt sql.NullInt64
		if err := rows.Scan(&sourceID, &runID, &status, &startedAt, &finishedAt); err != nil {
			return err
		}
		quick := byID[sourceID]
		if quick == nil {
			continue
		}
		history := quickRunHistoryView{ID: runID, Status: status, Icon: quickRunHistoryIcon(status)}
		if startedAt.Valid {
			history.StartedAt = time.Unix(0, startedAt.Int64).UTC()
			durationEnd := finishedAt
			if !durationEnd.Valid && quickRunHistoryActive(status) {
				durationEnd = sql.NullInt64{Int64: loadedAt, Valid: true}
			}
			if durationEnd.Valid && durationEnd.Int64 >= startedAt.Int64 {
				history.Duration = quickRunDuration(locale, time.Duration(durationEnd.Int64-startedAt.Int64))
				history.HasDuration = true
			}
		}
		quick.RecentRuns = append(quick.RecentRuns, history)
		if len(quick.RecentRuns) == 1 {
			quick.LastStartedAt = history.StartedAt
			quick.LastDuration = history.Duration
			quick.HasLastDuration = history.HasDuration
		}
	}
	return rows.Err()
}

func quickRunHistoryActive(status string) bool {
	switch status {
	case "starting", "running", "stopping", "timing_out":
		return true
	default:
		return false
	}
}

func quickRunHistoryIcon(status string) string {
	switch status {
	case "succeeded":
		return "check"
	case "starting", "running", "stopping", "timing_out":
		return "loader-circle"
	case "cancelled", "stopped":
		return "circle-stop"
	default:
		return "x"
	}
}

func quickRunDuration(locale webLocale, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Second {
		milliseconds := duration.Round(time.Millisecond) / time.Millisecond
		if milliseconds < 1 {
			milliseconds = 1
		}
		return strconv.FormatInt(int64(milliseconds), 10) + " ms"
	}
	if duration < time.Minute {
		seconds := float64(duration.Round(100*time.Millisecond)) / float64(time.Second)
		unit := "s"
		if locale == localeSimplifiedChinese {
			unit = " 秒"
		}
		return strconv.FormatFloat(seconds, 'f', -1, 64) + unit
	}
	minutes := int(duration / time.Minute)
	seconds := int(duration/time.Second) % 60
	if duration < time.Hour {
		if locale == localeSimplifiedChinese {
			return fmt.Sprintf("%d 分 %02d 秒", minutes, seconds)
		}
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	hours := int(duration / time.Hour)
	minutes %= 60
	if locale == localeSimplifiedChinese {
		return fmt.Sprintf("%d 小时 %02d 分", hours, minutes)
	}
	return fmt.Sprintf("%dh %02dm", hours, minutes)
}

func (a *App) loadQuickRun(id string) (quickRunRecord, error) {
	var quick quickRunRecord
	var groupID sql.NullString
	err := a.db.QueryRow(`SELECT id, name, script_path, arguments_template, timeout_seconds,
		source_run_id, sort_order, group_id, locked, script_sha256, revision
		FROM quick_runs WHERE id = ?`, id).Scan(
		&quick.ID, &quick.Name, &quick.ScriptPath, &quick.ArgumentsTemplate, &quick.TimeoutSeconds,
		&quick.SourceRunID, &quick.SortOrder, &groupID, &quick.Locked, &quick.ScriptSHA256, &quick.Revision,
	)
	if groupID.Valid {
		quick.GroupID = groupID.String
	}
	return quick, err
}

func (a *App) quickRunSourceSnapshot(quick quickRunRecord) string {
	if quick.GroupID == "" {
		return quick.Name
	}
	var groupName string
	if err := a.db.QueryRow(`SELECT name FROM quick_run_groups WHERE id = ?`, quick.GroupID).Scan(&groupName); err != nil || groupName == "" {
		return quick.Name
	}
	return groupName + " / " + quick.Name
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

func (a *App) canonicalQuickRunScript(value string) (string, error) {
	scriptPath, err := a.files.CanonicalExisting(value)
	if err != nil {
		return "", err
	}
	info, err := a.files.Info(scriptPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || !isScriptExtension(scriptPath) {
		return "", errors.New("path is not a runnable script")
	}
	return scriptPath, nil
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
	response.Header().Set(assistantResourceIDHeader, id)
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

func (a *App) reorderQuickRuns(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "快捷执行顺序无效", http.StatusBadRequest)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()

	groupIDs, err := collectQuickRunOrderIDs(transaction, "SELECT id FROM quick_run_groups", request.Form["group_id"])
	if err != nil {
		http.Error(response, "分组已发生变化，请刷新后重试", http.StatusConflict)
		return
	}
	quickRunIDs, err := collectQuickRunOrderIDs(transaction, "SELECT id FROM quick_runs", request.Form["quick_run_id"])
	if err != nil {
		http.Error(response, "快捷执行已发生变化，请刷新后重试", http.StatusConflict)
		return
	}

	// 批量校验完整清单后再写入，确保拖动排序不会因并发增删而部分覆盖。
	now := time.Now().UTC().Unix()
	for index, id := range groupIDs {
		if _, err = transaction.Exec("UPDATE quick_run_groups SET sort_order = ?, updated_at = ? WHERE id = ?", index+1, now, id); err != nil {
			break
		}
	}
	groupPositions := map[string]int{}
	for _, id := range quickRunIDs {
		var groupID sql.NullString
		if err = transaction.QueryRow("SELECT group_id FROM quick_runs WHERE id = ?", id).Scan(&groupID); err != nil {
			break
		}
		groupKey := groupID.String
		groupPositions[groupKey]++
		if _, err = transaction.Exec("UPDATE quick_runs SET sort_order = ?, updated_at = ? WHERE id = ?", groupPositions[groupKey], now, id); err != nil {
			break
		}
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法调整快捷执行顺序", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "reorder_quick_runs", fmt.Sprintf("%d groups, %d quick runs", len(groupIDs), len(quickRunIDs)), "succeeded")
	response.WriteHeader(http.StatusNoContent)
}

func collectQuickRunOrderIDs(transaction *sql.Tx, query string, submitted []string) ([]string, error) {
	rows, err := transaction.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	existing := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(existing) != len(submitted) {
		return nil, errors.New("incomplete order")
	}
	seen := make(map[string]struct{}, len(submitted))
	for _, id := range submitted {
		if _, exists := existing[id]; !exists {
			return nil, errors.New("unknown id")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("duplicate id")
		}
		seen[id] = struct{}{}
	}
	return submitted, nil
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
	quick, err := a.loadQuickRun(id)
	if err != nil {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	if quick.Locked {
		http.Error(response, "快捷执行已锁定，请先解锁", http.StatusConflict)
		return
	}
	prepared, err := a.hostPrepareScript(request.Context(), quick.ScriptPath)
	if err != nil {
		http.Error(response, "快捷执行脚本不可用", http.StatusConflict)
		return
	}
	now := time.Now().UTC()
	transaction, err := a.db.BeginTx(request.Context(), nil)
	if err != nil {
		http.Error(response, "无法更新快捷执行", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	newRevision := quick.Revision + 1
	result, err := transaction.ExecContext(request.Context(), `UPDATE quick_runs
		SET name = ?, arguments_template = ?, timeout_seconds = ?, script_sha256 = ?, revision = ?, updated_at = ?
		WHERE id = ? AND locked = 0 AND revision = ?`,
		name, arguments, timeoutSeconds, prepared.Digest, newRevision, now.Unix(), id, quick.Revision)
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
	synchronizeExternal := request.FormValue("sync_external_interfaces") == "1"
	if synchronizeExternal {
		if _, err = externaltrigger.RebindQuickRunEntries(request.Context(), transaction, id, newRevision, prepared.Digest, now); err != nil {
			http.Error(response, "无法同步外部接口", http.StatusInternalServerError)
			return
		}
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "无法更新快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordAuditResourceForRequest(request, "update_quick_run", id, "succeeded", strconv.FormatInt(newRevision, 10), prepared.Digest)
	if synchronizeExternal {
		a.recordAuditResourceForRequest(request, "sync_quick_run_external_interfaces", id, "succeeded", strconv.FormatInt(newRevision, 10), prepared.Digest)
	}
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

func (a *App) createQuickRunCopy(ctx context.Context, source quickRunRecord, scriptPath, name, arguments string, timeoutSeconds int, groupID *string) (string, error) {
	prepared, err := a.hostPrepareScript(ctx, scriptPath)
	if err != nil {
		return "", err
	}
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
	var sourceRunID any
	if hostfiles.ComparisonKey(source.ScriptPath) == hostfiles.ComparisonKey(scriptPath) && source.SourceRunID.Valid {
		sourceRunID = source.SourceRunID.String
	}
	if _, err = transaction.Exec(`INSERT INTO quick_runs
		(id, name, script_path, script_path_key, arguments_template, timeout_seconds, source_run_id,
		sort_order, created_at, group_id, locked, script_sha256, revision, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 1, ?)`,
		id, name, scriptPath, hostfiles.ComparisonKey(scriptPath), arguments, timeoutSeconds, sourceRunID,
		sortOrder, now, targetGroup, prepared.Digest, now); err != nil {
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
	scriptValue := request.FormValue("script")
	if scriptValue == "" {
		scriptValue = source.ScriptPath
	}
	scriptPath, err := a.canonicalQuickRunScript(scriptValue)
	if err != nil {
		writeHostFileError(response, "脚本不存在或不可运行", err)
		return
	}
	groupID, err := a.resolveQuickRunGroupID(request.FormValue("group_id"))
	if err != nil {
		http.Error(response, "快捷执行分组不存在", http.StatusConflict)
		return
	}
	id, err := a.createQuickRunCopy(request.Context(), source, scriptPath, name, arguments, timeoutSeconds, groupID)
	if err != nil {
		http.Error(response, "无法复制快捷执行", http.StatusInternalServerError)
		return
	}
	a.recordQuickRunAuditForRequest(request, "copy_quick_run", id, "succeeded")
	response.Header().Set(assistantResourceIDHeader, id)
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
	var revision int64
	var scriptSHA256 string
	err := a.db.QueryRowContext(request.Context(), `UPDATE quick_runs SET locked = ?, updated_at = ? WHERE id = ? RETURNING revision, script_sha256`,
		locked, time.Now().UTC().Unix(), id).Scan(&revision, &scriptSHA256)
	if err != nil && err != sql.ErrNoRows {
		http.Error(response, "无法更改快捷执行锁定状态", http.StatusInternalServerError)
		return
	}
	if err == sql.ErrNoRows {
		http.Error(response, "快捷执行不存在", http.StatusNotFound)
		return
	}
	action := "unlock_quick_run"
	if locked {
		action = "lock_quick_run"
	}
	a.recordAuditResourceForRequest(request, action, id, "succeeded", strconv.FormatInt(revision, 10), scriptSHA256)
	http.Redirect(response, request, "/config/quick-runs", http.StatusSeeOther)
}
