package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// recordGroup is the shared organization seam used by every grouped page.
// quick_run_groups remains the storage baseline so schema-60 data keeps its
// stable IDs and ordering while other collections can adopt the same groups.
type recordGroup struct {
	ID        string
	Name      string
	SortOrder int
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (a *App) loadRecordGroups() ([]recordGroup, error) {
	rows, err := a.db.Query(`SELECT id, name, sort_order FROM quick_run_groups ORDER BY sort_order, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []recordGroup
	for rows.Next() {
		var group recordGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

type recordGroupQueryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

func resolveRecordGroupIDWith(queryer recordGroupQueryRower, value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var exists int
	if err := queryer.QueryRow(`SELECT EXISTS(SELECT 1 FROM quick_run_groups WHERE id=?)`, value).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, sql.ErrNoRows
	}
	return &value, nil
}

func (a *App) resolveRecordGroupID(value string) (*string, error) {
	return resolveRecordGroupIDWith(a.db, value)
}

func recordGroupReturnTo(request *http.Request) string {
	value := strings.TrimSpace(request.URL.Query().Get("return_to"))
	switch value {
	case "/config/quick-runs", "/config/schedules", "/resources/variables", "/resources/files", "/resources/documents", "/monitor/websites":
		return value
	}
	switch {
	case strings.HasPrefix(request.URL.Path, "/config/schedules"):
		return "/config/schedules"
	case strings.HasPrefix(request.URL.Path, "/resources/documents"):
		return "/resources/documents"
	case strings.HasPrefix(request.URL.Path, "/resources/files"):
		return "/resources/files"
	case strings.HasPrefix(request.URL.Path, "/resources/variables"):
		return "/resources/variables"
	case strings.HasPrefix(request.URL.Path, "/monitor/websites"):
		return "/monitor/websites"
	default:
		return "/config/quick-runs"
	}
}

type recordGroupImpact struct {
	QuickRuns int
	Schedules int
	Variables int
	Files     int
	Documents int
	Websites  int
}

func (a *App) deleteRecordGroupTask(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var name string
	if err := a.db.QueryRow(`SELECT name FROM quick_run_groups WHERE id=?`, id).Scan(&name); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	impact := recordGroupImpact{}
	for _, count := range []struct {
		query string
		value *int
	}{
		{`SELECT COUNT(*) FROM quick_runs WHERE group_id=?`, &impact.QuickRuns},
		{`SELECT COUNT(*) FROM schedules WHERE group_id=? AND deleted=0`, &impact.Schedules},
		{`SELECT COUNT(*) FROM variables WHERE group_id=?`, &impact.Variables},
		{`SELECT COUNT(*) FROM file_quick_access_pins WHERE group_id=?`, &impact.Files},
		{`SELECT COUNT(*) FROM documents WHERE group_id=?`, &impact.Documents},
		{`SELECT COUNT(*) FROM website_monitors WHERE group_id=? AND deleted_at IS NULL`, &impact.Websites},
	} {
		if err := a.db.QueryRow(count.query, id).Scan(count.value); err != nil {
			http.Error(response, webText(resolveWebLocale(request), "groups.delete_failed"), http.StatusInternalServerError)
			return
		}
	}
	locale := resolveWebLocale(request)
	backURL := recordGroupReturnTo(request)
	a.renderTaskPage(response, request, taskPageData{
		Kind: "record-group-delete", Title: webText(locale, "task.record_group_delete.title"),
		Description: webText(locale, "task.record_group_delete.description"), Name: name,
		BackURL: backURL, Action: recordGroupAction("/config/groups/"+url.PathEscape(id)+"/delete", backURL),
		GroupImpact: impact,
	})
}

func recordGroupAction(path, returnTo string) string {
	return path + "?" + url.Values{"return_to": {returnTo}}.Encode()
}

func (a *App) newRecordGroupTask(response http.ResponseWriter, request *http.Request) {
	locale := resolveWebLocale(request)
	backURL := recordGroupReturnTo(request)
	kind, title, action := "record-group-new", webText(locale, "task.record_group_new.title"), recordGroupAction("/config/groups", backURL)
	if strings.HasPrefix(request.URL.Path, "/config/quick-runs/groups") {
		kind, title, action = "quick-group-new", webText(locale, "task.quick_group_new.title"), "/config/quick-runs/groups"
	} else if strings.HasPrefix(request.URL.Path, "/config/schedules/groups") {
		kind, title, action = "schedule-group-new", webText(locale, "task.schedule_group_new.title"), "/config/schedules/groups"
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: kind, Title: title,
		Description: webText(locale, "task.record_group.description"), BackURL: backURL,
		Action: action,
	})
}

func (a *App) editRecordGroupTask(response http.ResponseWriter, request *http.Request) {
	var name string
	id := request.PathValue("id")
	if err := a.db.QueryRow(`SELECT name FROM quick_run_groups WHERE id=?`, id).Scan(&name); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	locale := resolveWebLocale(request)
	backURL := recordGroupReturnTo(request)
	kind, title, action := "record-group-edit", webText(locale, "task.record_group_edit.title"), recordGroupAction("/config/groups/"+url.PathEscape(id)+"/update", backURL)
	if strings.HasPrefix(request.URL.Path, "/config/quick-runs/groups") {
		kind, title, action = "quick-group-edit", webText(locale, "task.quick_group_edit.title"), "/config/quick-runs/groups/"+url.PathEscape(id)+"/update"
	} else if strings.HasPrefix(request.URL.Path, "/config/schedules/groups") {
		kind, title, action = "schedule-group-edit", webText(locale, "task.schedule_group_edit.title"), "/config/schedules/groups/"+url.PathEscape(id)+"/update"
	}
	a.renderTaskPage(response, request, taskPageData{
		Kind: kind, Title: title,
		Description: webText(locale, "task.record_group.description"), BackURL: backURL,
		Action: action, Name: name,
	})
}

func (a *App) createRecordGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, webText(resolveWebLocale(request), "groups.invalid_name"), http.StatusBadRequest)
		return
	}
	id, err := randomToken(18)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var order int
	if err = tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM quick_run_groups`).Scan(&order); err == nil {
		now := time.Now().UTC().Unix()
		_, err = tx.Exec(`INSERT INTO quick_run_groups(id,name,sort_order,created_at,updated_at) VALUES(?,?,?,?,?)`, id, name, order, now, now)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO schedule_groups(id,name,sort_order,created_at,updated_at) VALUES(?,?,?,?,?)`, id, name, order, now, now)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		status := http.StatusInternalServerError
		key := "groups.save_failed"
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			status, key = http.StatusConflict, "groups.duplicate"
		}
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	a.recordAuditForRequest(request, "create_record_group", id, "succeeded")
	http.Redirect(response, request, recordGroupReturnTo(request), http.StatusSeeOther)
}

func (a *App) updateRecordGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" || len([]byte(name)) > 256 {
		http.Error(response, webText(resolveWebLocale(request), "groups.invalid_name"), http.StatusBadRequest)
		return
	}
	id := request.PathValue("id")
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	result, err := tx.Exec(`UPDATE quick_run_groups SET name=?,updated_at=? WHERE id=?`, name, now, id)
	changed := int64(0)
	if err == nil {
		changed, _ = result.RowsAffected()
	}
	if err == nil && changed > 0 {
		_, err = tx.Exec(`UPDATE schedule_groups SET name=?,updated_at=? WHERE id=?`, name, now, id)
	}
	if err == nil && changed > 0 {
		_, err = tx.Exec(`UPDATE schedules SET group_name=? WHERE group_id=? AND deleted=0`, name, id)
	}
	if err == nil && changed > 0 {
		err = tx.Commit()
	}
	if err != nil {
		status := http.StatusInternalServerError
		key := "groups.save_failed"
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			status, key = http.StatusConflict, "groups.duplicate"
		}
		http.Error(response, webText(resolveWebLocale(request), key), status)
		return
	}
	if changed == 0 {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	a.recordAuditForRequest(request, "update_record_group", id, "succeeded")
	http.Redirect(response, request, recordGroupReturnTo(request), http.StatusSeeOther)
}

func (a *App) moveRecordGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	direction := request.FormValue("direction")
	if direction == "" && strings.HasSuffix(request.URL.Path, "/move-top") {
		direction = "top"
	}
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM quick_run_groups ORDER BY sort_order,created_at`)
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	var ids []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			break
		}
		ids = append(ids, value)
	}
	_ = rows.Close()
	index := -1
	for candidate := range ids {
		if ids[candidate] == id {
			index = candidate
			break
		}
	}
	if index < 0 {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	switch direction {
	case "top":
		ids = append([]string{id}, append(ids[:index], ids[index+1:]...)...)
	case "up":
		if index > 0 {
			ids[index-1], ids[index] = ids[index], ids[index-1]
		}
	case "down":
		if index < len(ids)-1 {
			ids[index+1], ids[index] = ids[index], ids[index+1]
		}
	default:
		http.Error(response, webText(resolveWebLocale(request), "groups.invalid_order"), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Unix()
	for order, groupID := range ids {
		if _, err = tx.Exec(`UPDATE quick_run_groups SET sort_order=?,updated_at=? WHERE id=?`, order+1, now, groupID); err == nil {
			_, err = tx.Exec(`UPDATE schedule_groups SET sort_order=?,updated_at=? WHERE id=?`, order+1, now, groupID)
		}
		if err != nil {
			break
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_record_group", id, "succeeded")
	http.Redirect(response, request, recordGroupReturnTo(request), http.StatusSeeOther)
}

func (a *App) deleteRecordGroup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "groups.confirm_required"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.delete_failed"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM quick_run_groups WHERE id=?)`, id).Scan(&exists); err != nil || exists == 0 {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	// Deleting a shared group preserves every record and appends ordered cards
	// to each collection's Ungrouped ordering domain.
	for _, target := range []struct {
		table, key, updated, filter string
	}{
		{"quick_runs", "id", "updated_at", ""},
		{"schedules", "id", "updated_at", " AND deleted=0"},
		{"variables", "name", "updated_at", ""},
		{"file_quick_access_pins", "path_key", "", ""},
		{"documents", "path_key", "", ""},
		{"website_monitors", "id", "updated_at", " AND deleted_at IS NULL"},
	} {
		var ungroupedOrder int
		if err = tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM ` + target.table + ` WHERE group_id IS NULL` + target.filter).Scan(&ungroupedOrder); err != nil {
			break
		}
		rows, queryErr := tx.Query(`SELECT `+target.key+` FROM `+target.table+` WHERE group_id=?`+target.filter+` ORDER BY sort_order,created_at`, id)
		if queryErr != nil {
			err = queryErr
			break
		}
		var keys []string
		for rows.Next() {
			var key string
			if scanErr := rows.Scan(&key); scanErr != nil {
				err = scanErr
				break
			}
			keys = append(keys, key)
		}
		_ = rows.Close()
		for index, key := range keys {
			query := `UPDATE ` + target.table + ` SET group_id=NULL,sort_order=?`
			args := []any{ungroupedOrder + index + 1}
			if target.updated != "" {
				query += `,` + target.updated + `=?`
				args = append(args, time.Now().UTC().UnixNano())
			}
			if target.table == "schedules" {
				query += `,group_name=''`
			}
			query += ` WHERE ` + target.key + `=?`
			args = append(args, key)
			if _, err = tx.Exec(query, args...); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM schedule_groups WHERE id=?`, id)
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM quick_run_groups WHERE id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(response, webText(resolveWebLocale(request), "groups.delete_failed"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "delete_record_group", id, "succeeded")
	http.Redirect(response, request, recordGroupReturnTo(request), http.StatusSeeOther)
}

func (a *App) moveVariable(response http.ResponseWriter, request *http.Request) {
	a.moveGroupedRecord(response, request, "variables", "name", request.PathValue("name"), "", "/resources/variables")
}

func (a *App) moveSchedule(response http.ResponseWriter, request *http.Request) {
	a.moveGroupedRecord(response, request, "schedules", "id", request.PathValue("id"), " AND deleted=0", "/config/schedules")
}

func (a *App) moveGroupedRecord(response http.ResponseWriter, request *http.Request, table, key, id, filter, returnTo string) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	direction := request.FormValue("direction")
	if direction != "up" && direction != "down" {
		http.Error(response, webText(resolveWebLocale(request), "groups.invalid_order"), http.StatusBadRequest)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var groupID sql.NullString
	if err = tx.QueryRow(`SELECT group_id FROM `+table+` WHERE `+key+`=?`+filter, id).Scan(&groupID); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.not_found"), http.StatusNotFound)
		return
	}
	operator, order := "<", "DESC"
	if direction == "down" {
		operator, order = ">", "ASC"
	}
	var otherID string
	var currentOrder, otherOrder int
	if err = tx.QueryRow(`SELECT sort_order FROM `+table+` WHERE `+key+`=?`, id).Scan(&currentOrder); err == nil {
		err = tx.QueryRow(`SELECT `+key+`,sort_order FROM `+table+` WHERE group_id IS ? AND sort_order `+operator+` ?`+filter+` ORDER BY sort_order `+order+`,created_at `+order+` LIMIT 1`, groupID, currentOrder).Scan(&otherID, &otherOrder)
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(response, request, returnTo, http.StatusSeeOther)
		return
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE `+table+` SET sort_order=? WHERE `+key+`=?`, otherOrder, id)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE `+table+` SET sort_order=? WHERE `+key+`=?`, currentOrder, otherID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "groups.save_failed"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "move_grouped_record", table+":"+id, "succeeded")
	http.Redirect(response, request, returnTo, http.StatusSeeOther)
}
