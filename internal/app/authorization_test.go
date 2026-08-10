package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCancellingUserAccessEndsOnlyThatUsersActiveConnections(t *testing.T) {
	t.Parallel()

	application := &App{}
	userContext, userCancel := context.WithCancel(context.Background())
	otherContext, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	userRequestID := application.registerAuthenticatedRequest("user-one", userCancel)
	otherRequestID := application.registerAuthenticatedRequest("user-two", otherCancel)

	application.cancelAuthenticatedRequests("user-one")
	select {
	case <-userContext.Done():
	default:
		t.Fatal("revoked user's active connection was not cancelled")
	}
	select {
	case <-otherContext.Done():
		t.Fatal("another user's active connection was cancelled")
	default:
	}

	application.unregisterAuthenticatedRequest("user-one", userRequestID)
	application.unregisterAuthenticatedRequest("user-two", otherRequestID)
}

func TestFixedRolesCoverEveryProtectedRouteClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		method  string
		path    string
		allowed []userRole
	}{
		{"operations html", http.MethodGet, "/monitor", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"operations json", http.MethodGet, "/monitor/data", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"application log sse", http.MethodGet, "/monitor/applications/docker:one/logs/events", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"run log sse", http.MethodGet, "/history/runs/run-one/events", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"manage monitor", http.MethodPost, "/monitor/websites/site-one/check", []userRole{roleAdministrator, roleMaintainer}},
		{"file listing", http.MethodGet, "/resources/files", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"directory json", http.MethodGet, "/resources/directories", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"file download", http.MethodGet, "/resources/files/download?path=C%3A%5Cscripts%5Cscript.ps1", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"file upload task", http.MethodGet, "/resources/files/upload", []userRole{roleAdministrator, roleMaintainer}},
		{"file move task", http.MethodGet, "/resources/files/move?path=C%3A%5Cscripts%5Cscript.ps1", []userRole{roleAdministrator, roleMaintainer}},
		{"file mutation", http.MethodPost, "/resources/files/delete", []userRole{roleAdministrator, roleMaintainer}},
		{"file quick access pin", http.MethodPost, "/resources/files/quick-access", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"file script task", http.MethodGet, "/resources/files/run?path=C%3A%5Cscripts%5Cscript.ps1", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"quick run start", http.MethodPost, "/config/quick-runs/quick-one/start", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"one time source", http.MethodPost, "/config/quick-runs/one-time", []userRole{roleAdministrator, roleMaintainer}},
		{"schedule list", http.MethodGet, "/config/schedules", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"schedule run now", http.MethodPost, "/config/schedules/schedule-one/run", []userRole{roleAdministrator, roleMaintainer}},
		{"variables", http.MethodGet, "/resources/variables", []userRole{roleAdministrator, roleMaintainer}},
		{"mysql databases", http.MethodGet, "/resources/databases", []userRole{roleAdministrator, roleMaintainer}},
		{"mysql backup mutation", http.MethodPost, "/resources/databases/instances/instance-one/backup", []userRole{roleAdministrator, roleMaintainer}},
		{"external interfaces", http.MethodGet, "/config/external-interfaces", []userRole{roleAdministrator, roleMaintainer}},
		{"copy external interface key", http.MethodGet, "/config/external-interfaces/keys/key-one/copy", []userRole{roleAdministrator, roleMaintainer}},
		{"external interface mutation", http.MethodPost, "/config/external-interfaces/keys/key-one/toggle", []userRole{roleAdministrator, roleMaintainer}},
		{"audit html", http.MethodGet, "/history/audit", []userRole{roleAdministrator, roleMaintainer}},
		{"audit download", http.MethodGet, "/history/audit.csv", []userRole{roleAdministrator, roleMaintainer}},
		{"system settings", http.MethodGet, "/settings/updates/status", []userRole{roleAdministrator, roleMaintainer}},
		{"restart service", http.MethodPost, "/settings/updates/restart", []userRole{roleAdministrator, roleMaintainer}},
		{"user management", http.MethodGet, "/settings/users", []userRole{roleAdministrator}},
		{"user edit task", http.MethodGet, "/settings/users/user-one/edit", []userRole{roleAdministrator}},
		{"own account", http.MethodPost, "/settings/account", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"own password task", http.MethodGet, "/settings/account/password", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
		{"own username task", http.MethodGet, "/settings/account/username", []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}},
	}

	roles := []userRole{roleAdministrator, roleMaintainer, roleOperator, roleViewer}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			required := permissionForRequest(request)
			for _, role := range roles {
				want := containsRole(test.allowed, role)
				if got := roleAllows(role, required); got != want {
					t.Errorf("%s %s role=%s allowed=%v, want %v (permission=%d)", test.method, test.path, role, got, want, required)
				}
			}
		})
	}
}

func containsRole(roles []userRole, target userRole) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}
