package app

import (
	"net/http"
	"strings"
)

type userRole string

const (
	roleAdministrator userRole = "administrator"
	roleMaintainer    userRole = "maintainer"
	roleOperator      userRole = "operator"
	roleViewer        userRole = "viewer"
)

type permission uint8

const (
	permissionObserve permission = iota
	permissionManageOperations
	permissionReadFiles
	permissionWriteFiles
	permissionExecute
	permissionManageExecution
	permissionReadAudit
	permissionManageSystem
	permissionManageUsers
	permissionManageDatabases
)

func validAssignableRole(role userRole) bool {
	return role == roleMaintainer || role == roleOperator || role == roleViewer
}

func roleAllows(role userRole, required permission) bool {
	switch role {
	case roleAdministrator:
		return true
	case roleMaintainer:
		return required != permissionManageUsers
	case roleOperator:
		return required == permissionObserve || required == permissionReadFiles || required == permissionExecute
	case roleViewer:
		return required == permissionObserve
	default:
		return false
	}
}

// permissionForRequest is the authorization seam for the protected web
// surface. Route handlers remain responsible for domain-specific constraints
// such as an Operator stopping only a Run they initiated.
func permissionForRequest(request *http.Request) (permission, bool) {
	path := request.URL.Path
	if path == "/login" || path == "/settings/locale" {
		return permissionObserve, true
	}
	if strings.HasPrefix(path, "/settings/users") {
		return permissionManageUsers, true
	}
	if strings.HasPrefix(path, "/monitor/security") {
		if request.Method == http.MethodGet {
			return permissionObserve, true
		}
		return permissionManageSystem, true
	}
	if strings.HasPrefix(path, "/settings/account") || path == "/logout" {
		return permissionObserve, true
	}
	if strings.HasPrefix(path, "/settings/") {
		return permissionManageSystem, true
	}
	if strings.HasPrefix(path, "/history/audit") {
		return permissionReadAudit, true
	}
	if strings.HasPrefix(path, "/resources/variables") {
		return permissionManageExecution, true
	}
	if strings.HasPrefix(path, "/resources/databases") {
		return permissionManageDatabases, true
	}
	if strings.HasPrefix(path, "/resources/trash") {
		return permissionWriteFiles, true
	}
	if path == "/resources/directories" {
		return permissionReadFiles, true
	}
	if path == "/resources/files" || strings.HasPrefix(path, "/resources/files/") {
		if path == "/resources/files/quick-access" {
			return permissionReadFiles, true
		}
		if path == "/resources/files/run" {
			return permissionExecute, true
		}
		if path == "/resources/files/quick-run" ||
			path == "/resources/files/edit" ||
			path == "/resources/files/move" ||
			path == "/resources/files/new-directory" ||
			path == "/resources/files/upload" ||
			request.Method != http.MethodGet {
			return permissionWriteFiles, true
		}
		return permissionReadFiles, true
	}
	if path == "/monitor" || strings.HasPrefix(path, "/monitor/") {
		if request.Method != http.MethodGet ||
			path == "/monitor/websites/new" ||
			path == "/monitor/websites/export" ||
			path == "/monitor/websites/import" ||
			path == "/monitor/websites/nginx" ||
			strings.HasSuffix(path, "/edit") {
			return permissionManageOperations, true
		}
		return permissionObserve, true
	}
	if path == "/ai" || strings.HasPrefix(path, "/ai/") {
		return permissionObserve, true
	}
	if path == "/config/dashboards" || strings.HasPrefix(path, "/config/dashboards/") ||
		strings.HasPrefix(path, "/config/dashboard-cards/") {
		if request.Method == http.MethodGet && path == "/config/dashboards" {
			return permissionObserve, true
		}
		return permissionManageOperations, true
	}
	if strings.HasPrefix(path, "/config/quick-runs") {
		if request.Method == http.MethodPost && strings.HasSuffix(path, "/start") {
			return permissionExecute, true
		}
		if request.Method == http.MethodGet && (path == "/config/quick-runs" || path == "/config/quick-runs/") {
			return permissionObserve, true
		}
		return permissionManageExecution, true
	}
	if strings.HasPrefix(path, "/config/schedules") {
		if request.Method == http.MethodGet && (path == "/config/schedules" || path == "/config/schedules/") {
			return permissionObserve, true
		}
		return permissionManageExecution, true
	}
	if strings.HasPrefix(path, "/config/external-interfaces") {
		return permissionManageExecution, true
	}
	if strings.HasPrefix(path, "/history/runs") {
		if strings.HasSuffix(path, "/source") || strings.HasSuffix(path, "/save-quick-run") ||
			strings.HasSuffix(path, "/quick-run") {
			return permissionManageExecution, true
		}
		if request.Method == http.MethodPost {
			return permissionExecute, true
		}
		return permissionObserve, true
	}
	if path == "/" {
		return permissionObserve, true
	}
	return 0, false
}

func declaredPermissionForRequest(request *http.Request) permission {
	required, ok := permissionForRequest(request)
	if !ok {
		panic("protected route has no permission declaration: " + request.Method + " " + request.URL.Path)
	}
	return required
}

func (a *App) requirePermission(required permission, next http.Handler) http.Handler {
	return a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !roleAllows(current.role, required) {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	}))
}
