package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/mysqlmanager"
)

type mysqlDatabasesPageData struct {
	Locale     webLocale
	CSRFToken  string
	BackupRoot string
	Instances  []mysqlmanager.Instance
	Selected   *mysqlmanager.Instance
	Status     *mysqlmanager.Status
	Databases  []mysqlmanager.Database
	Backups    []mysqlmanager.Backup
	Plans      []mysqlmanager.Plan
	Operations []mysqlmanager.Operation
	Tools      mysqlmanager.ToolSettings
	LoadError  string
}

func (a *App) mysqlDatabasesPage(response http.ResponseWriter, request *http.Request) {
	instances, err := a.mysql.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	data := mysqlDatabasesPageData{
		Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
		BackupRoot: a.mysql.BackupRoot(), Instances: instances,
		Tools: a.mysql.Tools(),
	}
	selectedID := strings.TrimSpace(request.URL.Query().Get("instance"))
	if selectedID != "" {
		for index := range instances {
			if instances[index].ID == selectedID {
				selected := instances[index]
				data.Selected = &selected
				break
			}
		}
		if data.Selected == nil {
			http.Error(response, "MySQL instance not found", http.StatusNotFound)
			return
		}
		data.Backups, _ = a.mysql.Backups(request.Context(), selectedID, "")
		data.Operations, _ = a.mysql.Operations(request.Context(), selectedID)
		allPlans, _ := a.mysql.Plans(request.Context())
		for _, plan := range allPlans {
			if plan.InstanceID == selectedID {
				data.Plans = append(data.Plans, plan)
			}
		}
		probeContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		status, statusErr := a.mysql.Status(probeContext, selectedID)
		if statusErr == nil {
			data.Status = &status
			data.Databases, statusErr = a.mysql.Databases(probeContext, selectedID)
		}
		cancel()
		if statusErr != nil {
			data.LoadError = statusErr.Error()
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = mysqlDatabasesTemplate.Execute(response, data)
}

func mysqlActor(request *http.Request) mysqlmanager.Actor {
	current := request.Context().Value(sessionContextKey).(session)
	return mysqlmanager.Actor{UserID: current.userID, Username: current.username}
}

func (a *App) testMySQLInstance(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	result, err := a.mysql.TestInstance(request.Context(), request.PathValue("id"))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		response.WriteHeader(http.StatusBadGateway)
	}
	_ = json.NewEncoder(response).Encode(result)
}

func (a *App) deleteMySQLInstance(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	instance, err := a.mysql.Instance(request.Context(), request.PathValue("id"))
	if err != nil || request.FormValue("confirmation") != instance.Name {
		http.Error(response, "enter the complete instance name to confirm removal", http.StatusBadRequest)
		return
	}
	if err := a.mysql.DeleteInstance(request.Context(), instance.ID); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_mysql_instance", instance.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases", http.StatusSeeOther)
}

func (a *App) createMySQLDatabase(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id := request.PathValue("id")
	input := mysqlmanager.CreateDatabaseInput{Name: request.FormValue("name"), Charset: request.FormValue("charset"), Collation: request.FormValue("collation")}
	if err := a.mysql.CreateDatabase(request.Context(), id, input); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "create_mysql_database", id+"/"+input.Name, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id), http.StatusSeeOther)
}

func (a *App) startMySQLBackup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id, database, actor := request.PathValue("id"), request.FormValue("database"), mysqlActor(request)
	a.mysqlWG.Add(1)
	go func() {
		defer a.mysqlWG.Done()
		_, _ = a.mysql.Backup(a.mysqlContext, mysqlmanager.BackupRequest{InstanceID: id, Database: database, Kind: mysqlmanager.BackupManual, ActorUserID: actor.UserID, ActorUsername: actor.Username})
	}()
	a.recordAuditForRequest(request, "start_mysql_backup", id+"/"+database, "accepted")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id), http.StatusSeeOther)
}

func (a *App) startMySQLBatchBackup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	id, actor, databases := request.PathValue("id"), mysqlActor(request), append([]string(nil), request.Form["databases"]...)
	if len(databases) == 0 {
		http.Error(response, "select at least one database", http.StatusBadRequest)
		return
	}
	a.mysqlWG.Add(1)
	go func() {
		defer a.mysqlWG.Done()
		_, _ = a.mysql.BackupBatch(a.mysqlContext, mysqlmanager.BatchBackupRequest{InstanceID: id, Databases: databases, Actor: actor})
	}()
	a.recordAuditForRequest(request, "start_mysql_batch_backup", id, "accepted")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id), http.StatusSeeOther)
}

func (a *App) startMySQLRestore(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	backup, err := a.mysql.BackupByID(request.Context(), request.PathValue("backup_id"))
	if err != nil {
		http.Error(response, "MySQL backup not found", http.StatusNotFound)
		return
	}
	target, actor := strings.TrimSpace(request.FormValue("target_database")), mysqlActor(request)
	if request.FormValue("confirmation") != target {
		http.Error(response, "enter the complete target database name to confirm restore", http.StatusBadRequest)
		return
	}
	a.mysqlWG.Add(1)
	go func() {
		defer a.mysqlWG.Done()
		_, _ = a.mysql.Restore(a.mysqlContext, mysqlmanager.RestoreRequest{InstanceID: backup.InstanceID, BackupID: backup.ID, TargetDatabase: target, Actor: actor})
	}()
	a.recordAuditForRequest(request, "start_mysql_restore", backup.InstanceID+"/"+target, "accepted")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(backup.InstanceID), http.StatusSeeOther)
}

func (a *App) startDropMySQLDatabase(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	id, database, actor := request.PathValue("id"), request.FormValue("database"), mysqlActor(request)
	confirmation := request.FormValue("confirmation")
	if confirmation != database {
		http.Error(response, "enter the complete database name to confirm deletion", http.StatusBadRequest)
		return
	}
	a.mysqlWG.Add(1)
	go func() {
		defer a.mysqlWG.Done()
		_, _ = a.mysql.DropDatabase(a.mysqlContext, mysqlmanager.DropDatabaseRequest{InstanceID: id, Database: database, Confirmation: confirmation, Actor: actor})
	}()
	a.recordAuditForRequest(request, "start_drop_mysql_database", id+"/"+database, "accepted")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id), http.StatusSeeOther)
}

func (a *App) importMySQLBackup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, (2<<30)+(1<<20))
	if err := request.ParseMultipartForm(32 << 20); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	file, header, err := request.FormFile("backup")
	if err != nil {
		http.Error(response, "select a .sql or .sql.gz backup", http.StatusBadRequest)
		return
	}
	defer file.Close()
	backup, err := a.mysql.ImportBackup(request.Context(), mysqlmanager.ImportRequest{
		InstanceID: request.PathValue("id"), Database: request.FormValue("database"), Filename: header.Filename,
		Actor: mysqlActor(request), Reader: file,
	})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "import_mysql_backup", backup.InstanceID+"/"+backup.Database, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(backup.InstanceID), http.StatusSeeOther)
}

func (a *App) importMySQLServerBackup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	path := strings.TrimSpace(request.FormValue("path"))
	file, _, err := a.files.OpenRegular(path)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	backup, err := a.mysql.ImportBackup(request.Context(), mysqlmanager.ImportRequest{
		InstanceID: request.PathValue("id"), Database: request.FormValue("database"), Filename: path,
		Actor: mysqlActor(request), Reader: file,
	})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "import_mysql_server_backup", backup.InstanceID+"/"+backup.Database, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(backup.InstanceID), http.StatusSeeOther)
}

func (a *App) downloadMySQLBackup(response http.ResponseWriter, request *http.Request) {
	backup, err := a.mysql.BackupByID(request.Context(), request.PathValue("backup_id"))
	if err != nil {
		http.Error(response, "MySQL backup not found", http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.sql.gz"`, sanitizeDownloadName(backup.Database), backup.ID))
	http.ServeFile(response, request, backup.Path)
	a.recordAuditForRequest(request, "download_mysql_backup", backup.ID, "succeeded")
}

func sanitizeDownloadName(value string) string {
	value = strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			return char
		}
		return '_'
	}, value)
	return strings.Trim(value, "_")
}

func (a *App) deleteMySQLBackup(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	backup, err := a.mysql.BackupByID(request.Context(), request.PathValue("backup_id"))
	if err != nil {
		http.Error(response, "MySQL backup not found", http.StatusNotFound)
		return
	}
	if request.FormValue("confirmation") != backup.ID {
		http.Error(response, "enter the complete backup ID to confirm deletion", http.StatusBadRequest)
		return
	}
	if err := a.mysql.DeleteBackup(request.Context(), backup.ID); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_mysql_backup", backup.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(backup.InstanceID), http.StatusSeeOther)
}

func (a *App) saveMySQLPlan(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	retention, _ := strconv.Atoi(request.FormValue("retention_count"))
	plan, err := a.mysql.SavePlan(request.Context(), mysqlmanager.PlanInput{Name: request.FormValue("name"), InstanceID: request.PathValue("id"),
		Databases: request.Form["databases"], Expression: request.FormValue("expression"), RetentionCount: retention, Enabled: true})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "save_mysql_backup_plan", plan.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(plan.InstanceID), http.StatusSeeOther)
}

func (a *App) deleteMySQLPlan(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	plan, err := a.mysql.Plan(request.Context(), request.PathValue("plan_id"))
	if err != nil {
		http.Error(response, "MySQL backup plan not found", http.StatusNotFound)
		return
	}
	if err := a.mysql.DeletePlan(request.Context(), plan.ID); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_mysql_backup_plan", plan.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(plan.InstanceID), http.StatusSeeOther)
}

func (a *App) mysqlOperationStatus(response http.ResponseWriter, request *http.Request) {
	operation, err := a.mysql.Operation(request.Context(), request.PathValue("operation_id"))
	if err != nil {
		http.Error(response, "MySQL operation not found", http.StatusNotFound)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(operation)
}

func (a *App) cancelMySQLOperation(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	operation, err := a.mysql.Operation(request.Context(), request.PathValue("operation_id"))
	if err != nil {
		http.Error(response, "MySQL operation not found", http.StatusNotFound)
		return
	}
	if err := a.mysql.RequestCancel(request.Context(), operation.ID); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "cancel_mysql_operation", operation.ID, "accepted")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(operation.InstanceID), http.StatusSeeOther)
}

func (a *App) mysqlOperationEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := time.Time{}
	for {
		operation, err := a.mysql.Operation(request.Context(), request.PathValue("operation_id"))
		if err != nil {
			return
		}
		if !operation.UpdatedAt.Equal(last) {
			body, _ := json.Marshal(operation)
			_, _ = fmt.Fprintf(response, "event: progress\ndata: %s\n\n", body)
			flusher.Flush()
			last = operation.UpdatedAt
		}
		if mysqlOperationTerminal(operation.Phase) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func mysqlOperationTerminal(phase string) bool {
	switch phase {
	case "completed", "cancelled", "failed", "rolled_back", "needs_attention", "skipped_overlap":
		return true
	default:
		return false
	}
}

func (a *App) saveMySQLInstance(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(request.FormValue("port")))
	if err != nil {
		http.Error(response, "invalid MySQL port", http.StatusBadRequest)
		return
	}
	instance, err := a.mysql.SaveInstance(request.Context(), mysqlmanager.InstanceInput{
		ID: request.FormValue("id"), Name: request.FormValue("name"), Host: request.FormValue("host"), Port: port,
		Username: request.FormValue("username"), Password: request.FormValue("password"),
		TLSMode: mysqlmanager.TLSMode(request.FormValue("tls_mode")), CAPath: request.FormValue("ca_path"),
	})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "save_mysql_instance", instance.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases", http.StatusSeeOther)
}

func (a *App) setMySQLBackupRoot(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	root, err := a.mysql.SetBackupRoot(request.Context(), request.FormValue("backup_root"))
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.files.Protect(root); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "set_mysql_backup_root", root, "succeeded")
	http.Redirect(response, request, "/resources/databases", http.StatusSeeOther)
}

func (a *App) setMySQLTools(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	settings := mysqlmanager.ToolSettings{DumpExecutable: request.FormValue("dump_executable"), ClientExecutable: request.FormValue("client_executable")}
	if err := a.mysql.SetTools(request.Context(), settings); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "set_mysql_tools", settings.DumpExecutable+";"+settings.ClientExecutable, "succeeded")
	http.Redirect(response, request, "/resources/databases", http.StatusSeeOther)
}
