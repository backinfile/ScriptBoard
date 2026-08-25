package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scriptboard/internal/mysqlmanager"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/secretredaction"
)

type mysqlDatabasesPageData struct {
	databaseWorkspaceData
	Locale                                        webLocale
	CSRFToken                                     string
	BackupRoot                                    string
	Instances                                     []mysqlmanager.Instance
	InstanceRows                                  []mysqlmanager.Instance
	Selected                                      *mysqlmanager.Instance
	Status                                        *mysqlmanager.Status
	Databases                                     []mysqlmanager.Database
	DatabaseRows                                  []mysqlmanager.Database
	Backups                                       []mysqlmanager.Backup
	BackupDatabases                               []string
	BackupDatabase                                string
	Plans                                         []mysqlmanager.Plan
	Operations                                    []mysqlmanager.Operation
	Tools                                         mysqlmanager.ToolSettings
	LoadError                                     string
	ActiveTab                                     string
	DatabaseCount, BackupCount, BackupResultCount int
	PlanCount, OperationCount                     int
	OperationAccepted                             bool
	Pagination                                    mysqlPagination
	InstancePagination                            mysqlPagination
	ObjectDatabases                               []mysqlmanager.Database
	ObjectRows                                    []mysqlmanager.DatabaseObject
	ObjectDetails                                 *mysqlmanager.ObjectDetails
	ObjectPreview                                 *mysqlmanager.SQLResult
	ObjectDatabase, ObjectName                    string
	ShowSystemDatabases                           bool
	SQLDatabase, SQLStatement, SQLMode            string
	SQLTimeoutSeconds, SQLMaxRows                 int
	SQLResult                                     *mysqlmanager.SQLResult
	SQLError                                      string
}

type mysqlSQLPageState struct {
	Database, Statement, Mode string
	TimeoutSeconds, MaxRows   int
	Result                    *mysqlmanager.SQLResult
	Error                     string
}

type mysqlSQLPageStateKey struct{}

type mysqlPagination struct {
	Page, Pages, Total, Previous, Next int
	HasPrevious, HasNext               bool
	PageNumbers                        []int
}

const mysqlPageSize = 12
const mysqlInstancePageSize = 8

func newMySQLPaginationWithSize(page, total, pageSize int) mysqlPagination {
	pages := max(1, (total+pageSize-1)/pageSize)
	page = min(max(page, 1), pages)
	pagination := mysqlPagination{Page: page, Pages: pages, Total: total, Previous: max(1, page-1), Next: min(pages, page+1), HasPrevious: page > 1, HasNext: page < pages}
	start, end := max(1, page-2), min(pages, page+2)
	if end-start < 4 {
		start = max(1, end-4)
		end = min(pages, start+4)
	}
	for number := start; number <= end; number++ {
		pagination.PageNumbers = append(pagination.PageNumbers, number)
	}
	return pagination
}

func newMySQLPagination(page, total int) mysqlPagination {
	return newMySQLPaginationWithSize(page, total, mysqlPageSize)
}

func mysqlRequestedNamedPage(request *http.Request, name string) int {
	page, err := strconv.Atoi(request.URL.Query().Get(name))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func mysqlRequestedPage(request *http.Request) int { return mysqlRequestedNamedPage(request, "page") }

func mysqlSlicePage[T any](items []T, page int) ([]T, mysqlPagination) {
	return mysqlSlicePageWithSize(items, page, mysqlPageSize)
}

func mysqlSlicePageWithSize[T any](items []T, page, pageSize int) ([]T, mysqlPagination) {
	pagination := newMySQLPaginationWithSize(page, len(items), pageSize)
	start := (pagination.Page - 1) * pageSize
	end := min(start+pageSize, len(items))
	return items[start:end], pagination
}

func (a *App) mysqlDatabasesPage(response http.ResponseWriter, request *http.Request) {
	instances, err := a.mysql.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	current := request.Context().Value(sessionContextKey).(session)
	redisInstances, err := a.redis.Instances(request.Context())
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	data := mysqlDatabasesPageData{
		Locale: resolveWebLocale(request), CSRFToken: current.csrfToken,
		BackupRoot: a.mysql.BackupRoot(), Instances: instances,
		Tools:             a.mysql.Tools(),
		ActiveTab:         "overview",
		OperationAccepted: request.URL.Query().Get("accepted") == "backup",
	}
	data.databaseWorkspaceData = newDatabaseWorkspaceData(request, "mysql", data.Locale, data.CSRFToken, data.BackupRoot, data.Tools, instances, redisInstances)
	if tab := strings.TrimSpace(request.URL.Query().Get("tab")); tab == "databases" || tab == "objects" || tab == "sql" || tab == "backups" || tab == "plans" || tab == "operations" {
		data.ActiveTab = tab
	}
	data.InstanceRows, data.InstancePagination = mysqlSlicePageWithSize(instances, mysqlRequestedNamedPage(request, "instance_page"), mysqlInstancePageSize)
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
		data.SelectedMySQL = data.Selected
		allPlans, _ := a.mysql.Plans(request.Context())
		for _, plan := range allPlans {
			if plan.InstanceID == selectedID {
				data.Plans = append(data.Plans, plan)
			}
		}
		data.PlanCount = len(data.Plans)
		probeContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		status, statusErr := a.mysql.Status(probeContext, selectedID)
		if statusErr != nil {
			data.Selected.ConnectionState = mysqlmanager.ConnectionFailed
		} else {
			data.Selected.ConnectionState = mysqlmanager.ConnectionConnected
			data.Status = &status
			data.Databases, statusErr = a.mysql.Databases(probeContext, selectedID)
		}
		for index := range data.InstanceRows {
			if data.InstanceRows[index].ID == selectedID {
				data.InstanceRows[index].ConnectionState = data.Selected.ConnectionState
				break
			}
		}
		for index := range data.ConnectionRows {
			if data.ConnectionRows[index].Engine == "mysql" && data.ConnectionRows[index].ID == selectedID {
				data.ConnectionRows[index].ConnectionState = string(data.Selected.ConnectionState)
				break
			}
		}
		cancel()
		if statusErr != nil {
			data.LoadError = secretredaction.String(statusErr.Error())
		}
		data.DatabaseCount = len(data.Databases)
		if data.ActiveTab == "objects" || data.ActiveTab == "sql" {
			a.loadMySQLWorkbench(request, selectedID, &data)
		}
		page := mysqlRequestedPage(request)
		if data.ActiveTab == "databases" {
			data.DatabaseRows, data.Pagination = mysqlSlicePage(data.Databases, page)
		} else {
			data.DatabaseRows, _ = mysqlSlicePage(data.Databases, 1)
		}
		backupPage := 1
		if data.ActiveTab == "backups" {
			backupPage = page
		}
		data.BackupDatabases, err = a.mysql.BackupDatabases(request.Context(), selectedID)
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		if data.ActiveTab == "backups" {
			data.BackupDatabase = strings.TrimSpace(request.URL.Query().Get("database"))
			if data.BackupDatabase != "" {
				valid := false
				for _, database := range data.BackupDatabases {
					if database == data.BackupDatabase {
						valid = true
						break
					}
				}
				if !valid {
					http.Error(response, webText(data.Locale, "mysql.invalid_backup_database_filter"), http.StatusBadRequest)
					return
				}
			}
		}
		data.Backups, data.BackupCount, err = a.mysql.BackupsPage(request.Context(), selectedID, "", mysqlPageSize, (backupPage-1)*mysqlPageSize)
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		data.BackupResultCount = data.BackupCount
		if data.BackupDatabase != "" {
			data.Backups, data.BackupResultCount, err = a.mysql.BackupsPage(request.Context(), selectedID, data.BackupDatabase, mysqlPageSize, (backupPage-1)*mysqlPageSize)
			if err != nil {
				http.Error(response, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		operationPage := 1
		if data.ActiveTab == "operations" {
			operationPage = page
		}
		data.Operations, data.OperationCount, _ = a.mysql.OperationsPage(request.Context(), selectedID, mysqlPageSize, (operationPage-1)*mysqlPageSize)
		if data.ActiveTab == "plans" {
			data.Plans, data.Pagination = mysqlSlicePage(data.Plans, page)
		} else if data.ActiveTab == "backups" {
			data.Pagination = newMySQLPagination(backupPage, data.BackupResultCount)
			if data.Pagination.Page != backupPage {
				data.Backups, _, _ = a.mysql.BackupsPage(request.Context(), selectedID, data.BackupDatabase, mysqlPageSize, (data.Pagination.Page-1)*mysqlPageSize)
			}
		} else if data.ActiveTab == "operations" {
			data.Pagination = newMySQLPagination(operationPage, data.OperationCount)
			if data.Pagination.Page != operationPage {
				data.Operations, _, _ = a.mysql.OperationsPage(request.Context(), selectedID, mysqlPageSize, (data.Pagination.Page-1)*mysqlPageSize)
			}
		}
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = mysqlDatabasesTemplate.Execute(response, data)
}

func (a *App) loadMySQLWorkbench(request *http.Request, instanceID string, data *mysqlDatabasesPageData) {
	data.SQLTimeoutSeconds = 10
	data.SQLMaxRows = 200
	data.SQLMode = string(mysqlmanager.SQLModeReadOnly)
	data.ShowSystemDatabases = request.URL.Query().Get("show_system") == "1"
	data.ObjectDatabases = data.Databases
	workbenchContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	if data.ShowSystemDatabases {
		allDatabases, err := a.mysql.DatabasesIncludingSystem(workbenchContext, instanceID)
		if err != nil {
			data.LoadError = secretredaction.String(err.Error())
			return
		}
		data.ObjectDatabases = allDatabases
	}
	requestedDatabase := strings.TrimSpace(request.URL.Query().Get("database"))
	for _, database := range data.ObjectDatabases {
		if requestedDatabase == "" || database.Name == requestedDatabase {
			data.ObjectDatabase = database.Name
			if requestedDatabase != "" || data.ObjectDatabase != "" {
				break
			}
		}
	}
	data.SQLDatabase = data.ObjectDatabase
	data.SQLStatement = strings.TrimSpace(request.URL.Query().Get("statement"))
	if state, ok := request.Context().Value(mysqlSQLPageStateKey{}).(mysqlSQLPageState); ok {
		data.SQLDatabase = state.Database
		data.SQLStatement = state.Statement
		data.SQLMode = state.Mode
		data.SQLTimeoutSeconds = state.TimeoutSeconds
		data.SQLMaxRows = state.MaxRows
		data.SQLResult = state.Result
		data.SQLError = state.Error
	}
	if data.ActiveTab != "objects" || data.ObjectDatabase == "" {
		return
	}
	objects, err := a.mysql.Objects(workbenchContext, instanceID, data.ObjectDatabase)
	if err != nil {
		data.LoadError = secretredaction.String(err.Error())
		return
	}
	data.ObjectRows = objects
	requestedObject := strings.TrimSpace(request.URL.Query().Get("object"))
	if requestedObject == "" {
		return
	}
	for _, object := range objects {
		if object.Name == requestedObject {
			data.ObjectName = object.Name
			break
		}
	}
	if data.ObjectName == "" {
		return
	}
	details, err := a.mysql.ObjectDetails(workbenchContext, instanceID, data.ObjectDatabase, data.ObjectName)
	if err != nil {
		data.LoadError = secretredaction.String(err.Error())
		return
	}
	data.ObjectDetails = &details
	if request.URL.Query().Get("preview") == "1" {
		preview, previewErr := a.mysql.PreviewRows(workbenchContext, instanceID, data.ObjectDatabase, data.ObjectName, 200)
		if previewErr != nil {
			data.LoadError = secretredaction.String(previewErr.Error())
			return
		}
		data.ObjectPreview = &preview
	}
}

func (a *App) executeMySQLReadOnlySQL(response http.ResponseWriter, request *http.Request) {
	a.executeMySQLSQL(response, request, mysqlmanager.SQLModeReadOnly)
}

func (a *App) executeMySQLWriteSQL(response http.ResponseWriter, request *http.Request) {
	a.executeMySQLSQL(response, request, mysqlmanager.SQLModeWrite)
}

func (a *App) executeMySQLSQL(response http.ResponseWriter, request *http.Request, mode mysqlmanager.SQLMode) {
	if !validSessionCSRF(request) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid SQL request", http.StatusBadRequest)
		return
	}
	instanceID := strings.TrimSpace(request.PathValue("id"))
	database := strings.TrimSpace(request.FormValue("database"))
	statement := strings.TrimSpace(request.FormValue("statement"))
	timeoutSeconds := mysqlBoundedInt(request.FormValue("timeout_seconds"), 10, 1, 60)
	maxRows := mysqlBoundedInt(request.FormValue("max_rows"), 200, 1, 1000)
	state := mysqlSQLPageState{Database: database, Statement: statement, Mode: string(mode), TimeoutSeconds: timeoutSeconds, MaxRows: maxRows}
	result, err := a.mysql.ExecuteSQL(request.Context(), instanceID, mysqlmanager.SQLRequest{
		Database: database, Statement: statement, Mode: mode,
		Timeout: time.Duration(timeoutSeconds) * time.Second, MaxRows: maxRows,
		AllowDangerous: request.FormValue("allow_dangerous") == "yes", Actor: mysqlActor(request),
	})
	if err != nil {
		state.Error = secretredaction.String(err.Error())
	} else {
		state.Result = &result
	}
	query := request.URL.Query()
	query.Set("instance", instanceID)
	query.Set("tab", "sql")
	query.Set("database", database)
	request.URL.Path = "/resources/databases"
	request.URL.RawQuery = query.Encode()
	request = request.WithContext(context.WithValue(request.Context(), mysqlSQLPageStateKey{}, state))
	a.mysqlDatabasesPage(response, request)
}

func mysqlBoundedInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func mysqlActor(request *http.Request) mysqlmanager.Actor {
	current := request.Context().Value(sessionContextKey).(session)
	return mysqlmanager.Actor{UserID: current.userID, Username: current.username}
}

func mysqlOperationContext(base context.Context, request *http.Request) context.Context {
	authorization, ok := privilegebroker.AuthorizationFromContext(request.Context())
	if !ok {
		return base
	}
	return privilegebroker.WithAuthorization(base, authorization)
}

func (a *App) startMySQLBackgroundOperation(request *http.Request, action, target string, operation func(context.Context)) {
	operationContext := mysqlOperationContext(a.mysqlContext, request)
	a.mysqlWG.Add(1)
	go func() {
		defer a.mysqlWG.Done()
		operation(operationContext)
	}()
	a.recordAuditForRequest(request, action, target, "accepted")
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
	if err != nil || request.FormValue("confirm") != "yes" {
		http.Error(response, "explicit confirmation is required to remove the instance", http.StatusBadRequest)
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
	a.startMySQLBackgroundOperation(request, "start_mysql_backup", id+"/"+database, func(operationContext context.Context) {
		_, _ = a.mysql.Backup(operationContext, mysqlmanager.BackupRequest{InstanceID: id, Database: database, Kind: mysqlmanager.BackupManual, ActorUserID: actor.UserID, ActorUsername: actor.Username})
	})
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id)+"&tab=operations&accepted=backup", http.StatusSeeOther)
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
	a.startMySQLBackgroundOperation(request, "start_mysql_batch_backup", id, func(operationContext context.Context) {
		_, _ = a.mysql.BackupBatch(operationContext, mysqlmanager.BatchBackupRequest{InstanceID: id, Databases: databases, Actor: actor})
	})
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(id)+"&tab=operations&accepted=backup", http.StatusSeeOther)
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
	a.startMySQLBackgroundOperation(request, "start_mysql_restore", backup.InstanceID+"/"+target, func(operationContext context.Context) {
		_, _ = a.mysql.Restore(operationContext, mysqlmanager.RestoreRequest{InstanceID: backup.InstanceID, BackupID: backup.ID, TargetDatabase: target, Actor: actor})
	})
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
	a.startMySQLBackgroundOperation(request, "start_drop_mysql_database", id+"/"+database, func(operationContext context.Context) {
		_, _ = a.mysql.DropDatabase(operationContext, mysqlmanager.DropDatabaseRequest{InstanceID: id, Database: database, Confirmation: confirmation, Actor: actor})
	})
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
	file, _, err := a.hostOpenRegular(request.Context(), path)
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
	if downloader, ok := a.mysql.ExecutionBackend().(interface {
		DownloadBackup(context.Context, string, io.Writer) (string, int64, error)
	}); ok {
		response.Header().Set("Content-Type", "application/gzip")
		response.Header().Set("Content-Length", strconv.FormatInt(backup.SizeBytes, 10))
		if _, _, err := downloader.DownloadBackup(request.Context(), backup.ID, response); err != nil {
			return
		}
	} else {
		http.ServeFile(response, request, backup.Path)
	}
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
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(plan.InstanceID)+"&tab=plans", http.StatusSeeOther)
}

func (a *App) updateMySQLPlan(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	current, err := a.mysql.Plan(request.Context(), request.PathValue("plan_id"))
	if err != nil {
		http.Error(response, "MySQL backup plan not found", http.StatusNotFound)
		return
	}
	retention, _ := strconv.Atoi(request.FormValue("retention_count"))
	plan, err := a.mysql.SavePlan(request.Context(), mysqlmanager.PlanInput{ID: current.ID, Name: request.FormValue("name"), InstanceID: current.InstanceID,
		Databases: request.Form["databases"], Expression: request.FormValue("expression"), RetentionCount: retention, Enabled: current.Enabled})
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.recordAuditForRequest(request, "update_mysql_backup_plan", plan.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(plan.InstanceID)+"&tab=plans", http.StatusSeeOther)
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
	if request.FormValue("confirmation") != plan.Name {
		http.Error(response, "enter the complete plan name to confirm deletion", http.StatusBadRequest)
		return
	}
	if err := a.mysql.DeletePlan(request.Context(), plan.ID); err != nil {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	a.recordAuditForRequest(request, "delete_mysql_backup_plan", plan.ID, "succeeded")
	http.Redirect(response, request, "/resources/databases?instance="+url.QueryEscape(plan.InstanceID)+"&tab=plans", http.StatusSeeOther)
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
	destination := "/resources/databases"
	if strings.TrimSpace(request.FormValue("id")) != "" {
		// Keep an edited instance selected after the drawer refreshes its workspace region.
		destination += "?instance=" + instance.ID
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
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
