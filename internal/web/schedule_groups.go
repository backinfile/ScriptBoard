package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"scriptboard/internal/scheduler"
)

type scheduleGroup struct {
	ID            string
	Name          string
	SortOrder     int
	ScheduleCount int
	Items         []scheduler.Schedule
	Ungrouped     bool
}

func (a *App) loadScheduleGroups() ([]scheduleGroup, error) {
	rows, err := a.db.Query(`SELECT g.id, g.name, g.sort_order, COUNT(s.id)
		FROM quick_run_groups g
		LEFT JOIN schedules s ON s.group_id = g.id AND s.deleted = 0
		GROUP BY g.id, g.name, g.sort_order, g.created_at
		ORDER BY g.sort_order, g.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []scheduleGroup
	for rows.Next() {
		var group scheduleGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder, &group.ScheduleCount); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (a *App) resolveScheduleGroup(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", nil
	}
	var name string
	if err := a.db.QueryRow("SELECT name FROM quick_run_groups WHERE id = ?", value).Scan(&name); err != nil {
		return "", "", err
	}
	return value, name, nil
}

func organizeScheduleGroups(groups []scheduleGroup, schedules []scheduler.Schedule, locale webLocale) []scheduleGroup {
	groupIndexes := make(map[string]int, len(groups))
	for index := range groups {
		groupIndexes[groups[index].ID] = index
	}
	var ungrouped []scheduler.Schedule
	for _, schedule := range schedules {
		if index, ok := groupIndexes[schedule.GroupID]; ok {
			groups[index].Items = append(groups[index].Items, schedule)
		} else {
			ungrouped = append(ungrouped, schedule)
		}
	}
	if len(ungrouped) > 0 {
		groups = append(groups, scheduleGroup{
			ID: "ungrouped", Name: webText(locale, "schedules.ungrouped"),
			ScheduleCount: len(ungrouped), Items: ungrouped, Ungrouped: true,
		})
	}
	return groups
}

func (a *App) newScheduleGroupTask(response http.ResponseWriter, request *http.Request) {
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "schedule-group-new",
		Title:       webText(resolveWebLocale(request), "task.schedule_group_new.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_group.description"),
		BackURL:     "/config/schedules",
		Action:      "/config/schedules/groups",
	})
}

func (a *App) editScheduleGroupTask(response http.ResponseWriter, request *http.Request) {
	var name string
	id := request.PathValue("id")
	if err := a.db.QueryRow("SELECT name FROM schedule_groups WHERE id = ?", id).Scan(&name); err != nil {
		http.Error(response, "计划分组不存在", http.StatusNotFound)
		return
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind:        "schedule-group-edit",
		Title:       webText(resolveWebLocale(request), "task.schedule_group_edit.title"),
		Description: webText(resolveWebLocale(request), "task.schedule_group.description"),
		BackURL:     "/config/schedules",
		Action:      "/config/schedules/groups/" + id + "/update",
		Name:        name,
	})
}

func (a *App) createScheduleGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "计划分组名称无效", http.StatusBadRequest)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, "无法创建计划分组", http.StatusInternalServerError)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法创建计划分组", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	var sortOrder int
	if err = transaction.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM schedule_groups").Scan(&sortOrder); err == nil {
		now := time.Now().UTC().Unix()
		_, err = transaction.Exec(`INSERT INTO schedule_groups (id, name, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, id, name, sortOrder, now, now)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "计划分组名称已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法创建计划分组", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "create_schedule_group", id, "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) updateScheduleGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, "计划分组名称无效", http.StatusBadRequest)
		return
	}
	id := request.PathValue("id")
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法更新计划分组", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec(`UPDATE schedule_groups SET name = ?, updated_at = ? WHERE id = ?`,
		name, time.Now().UTC().Unix(), id)
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if err == nil && count > 0 {
		_, err = transaction.Exec("UPDATE schedules SET group_name = ? WHERE group_id = ?", name, id)
	}
	if err == nil && count > 0 {
		err = transaction.Commit()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(response, "计划分组名称已存在", http.StatusConflict)
			return
		}
		http.Error(response, "无法更新计划分组", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(response, "计划分组不存在", http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "update_schedule_group", id, "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) moveScheduleGroup(response http.ResponseWriter, request *http.Request) {
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
		http.Error(response, "无法调整计划分组顺序", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	var currentOrder int
	if err := transaction.QueryRow("SELECT sort_order FROM schedule_groups WHERE id = ?", id).Scan(&currentOrder); err != nil {
		http.Error(response, "计划分组不存在", http.StatusNotFound)
		return
	}
	var neighborID string
	var neighborOrder int
	query := "SELECT id, sort_order FROM schedule_groups WHERE sort_order " + operator + " ? ORDER BY sort_order " + order + " LIMIT 1"
	if scanErr := transaction.QueryRow(query, currentOrder).Scan(&neighborID, &neighborOrder); scanErr == nil {
		_, err = transaction.Exec(`UPDATE schedule_groups
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
		http.Error(response, "无法调整计划分组顺序", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_schedule_group", id, "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}

func (a *App) deleteScheduleGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, "删除计划分组需要页面安全令牌和明确确认", http.StatusForbidden)
		return
	}
	transaction, err := a.db.Begin()
	if err != nil {
		http.Error(response, "无法删除计划分组", http.StatusInternalServerError)
		return
	}
	defer transaction.Rollback()
	id := request.PathValue("id")
	var exists int
	if err := transaction.QueryRow("SELECT EXISTS(SELECT 1 FROM schedule_groups WHERE id = ?)", id).Scan(&exists); err != nil || exists == 0 {
		http.Error(response, "计划分组不存在", http.StatusNotFound)
		return
	}
	if _, err = transaction.Exec(`UPDATE schedules SET group_id = NULL, group_name = ''
		WHERE group_id = ? AND deleted = 0`, id); err == nil {
		_, err = transaction.Exec("DELETE FROM schedule_groups WHERE id = ?", id)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		http.Error(response, "无法删除计划分组", http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "delete_schedule_group", id, "succeeded")
	http.Redirect(response, request, "/config/schedules", http.StatusSeeOther)
}
