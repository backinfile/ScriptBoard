// Package identity owns ScriptBoard's local identity and authorization rules.
package identity

import "time"

type Role string

const (
	RoleAdministrator Role = "administrator"
	RoleMaintainer    Role = "maintainer"
	RoleOperator      Role = "operator"
	RoleViewer        Role = "viewer"
)

type Permission uint8

const (
	PermissionObserve Permission = iota
	PermissionManageOperations
	PermissionReadFiles
	PermissionWriteFiles
	PermissionExecute
	PermissionManageExecution
	PermissionReadAudit
	PermissionManageSystem
	PermissionManageUsers
	PermissionManageDatabases
	PermissionConfigureDockerEngine
)

const RecentAuthenticationWindow = 10 * time.Minute

func ValidAssignableRole(role Role) bool {
	return role == RoleMaintainer || role == RoleOperator || role == RoleViewer
}

func Allows(role Role, required Permission) bool {
	switch role {
	case RoleAdministrator:
		return true
	case RoleMaintainer:
		return required != PermissionManageUsers && required != PermissionConfigureDockerEngine
	case RoleOperator:
		return required == PermissionObserve || required == PermissionReadFiles || required == PermissionExecute
	case RoleViewer:
		return required == PermissionObserve
	default:
		return false
	}
}

func RecentAuthenticationValid(timestamp int64, now time.Time) bool {
	if timestamp <= 0 {
		return false
	}
	authenticatedAt := time.Unix(timestamp, 0)
	return !authenticatedAt.After(now.Add(time.Minute)) && now.Sub(authenticatedAt) <= RecentAuthenticationWindow
}
