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
func permissionForRequest(request *http.Request) permission {
	path := request.URL.Path
	if strings.HasPrefix(path, "/settings/users") {
		return permissionManageUsers
	}
	if strings.HasPrefix(path, "/settings/account") || path == "/logout" {
		return permissionObserve
	}
	if strings.HasPrefix(path, "/settings/") {
		return permissionManageSystem
	}
	if strings.HasPrefix(path, "/history/audit") {
		return permissionReadAudit
	}
	if strings.HasPrefix(path, "/resources/variables") {
		return permissionManageExecution
	}
	if strings.HasPrefix(path, "/resources/trash") {
		return permissionWriteFiles
	}
	if path == "/resources/directories" {
		return permissionReadFiles
	}
	if path == "/resources/files" || strings.HasPrefix(path, "/resources/files/") {
		if strings.HasPrefix(path, "/resources/files/run/") {
			return permissionExecute
		}
		if strings.HasPrefix(path, "/resources/files/quick-run/") ||
			strings.HasPrefix(path, "/resources/files/edit/") ||
			path == "/resources/files/new-directory" ||
			path == "/resources/files/upload" ||
			request.Method != http.MethodGet {
			return permissionWriteFiles
		}
		return permissionReadFiles
	}
	if strings.HasPrefix(path, "/monitor/") {
		if request.Method != http.MethodGet ||
			path == "/monitor/websites/new" ||
			path == "/monitor/websites/nginx" ||
			strings.HasSuffix(path, "/edit") {
			return permissionManageOperations
		}
		return permissionObserve
	}
	if strings.HasPrefix(path, "/config/quick-runs") {
		if request.Method == http.MethodPost && strings.HasSuffix(path, "/start") {
			return permissionExecute
		}
		if request.Method == http.MethodGet && (path == "/config/quick-runs" || path == "/config/quick-runs/") {
			return permissionObserve
		}
		return permissionManageExecution
	}
	if strings.HasPrefix(path, "/config/schedules") {
		if request.Method == http.MethodGet && (path == "/config/schedules" || path == "/config/schedules/") {
			return permissionObserve
		}
		return permissionManageExecution
	}
	if strings.HasPrefix(path, "/history/runs") {
		if strings.HasSuffix(path, "/source") || strings.HasSuffix(path, "/save-quick-run") ||
			strings.HasSuffix(path, "/quick-run") {
			return permissionManageExecution
		}
		if request.Method == http.MethodPost {
			return permissionExecute
		}
		return permissionObserve
	}
	return permissionObserve
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
