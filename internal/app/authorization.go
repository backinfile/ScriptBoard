package app

import (
	"net/http"
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

func (a *App) requirePermission(required permission, next http.Handler) http.Handler {
	protected := a.requireSession(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, ok := request.Context().Value(sessionContextKey).(session)
		if !ok || !roleAllows(current.role, required) {
			http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	}))
	return declaredRouteHandler{auth: routeAuthSession, permission: required, handler: protected}
}
