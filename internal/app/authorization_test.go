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
	application := &App{}
	application.routes()

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
		{"export monitor configurations", http.MethodGet, "/monitor/websites/export", []userRole{roleAdministrator, roleMaintainer}},
		{"import monitor configurations", http.MethodGet, "/monitor/websites/import", []userRole{roleAdministrator, roleMaintainer}},
		{"file listing", http.MethodGet, "/resources/files", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"directory json", http.MethodGet, "/resources/directories", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"file download", http.MethodGet, "/resources/files/download?path=C%3A%5Cscripts%5Cscript.ps1", []userRole{roleAdministrator, roleMaintainer, roleOperator}},
		{"file upload task", http.MethodGet, "/resources/files/upload", []userRole{roleAdministrator, roleMaintainer}},
		{"file move task", http.MethodGet, "/resources/files/move?path=C%3A%5Cscripts%5Cscript.ps1", []userRole{roleAdministrator, roleMaintainer}},
		{"file mutation", http.MethodPost, "/resources/files/delete", []userRole{roleAdministrator, roleMaintainer}},
		{"external upload inbox", http.MethodGet, "/resources/inbox", []userRole{roleAdministrator, roleMaintainer}},
		{"publish external upload", http.MethodPost, "/resources/inbox/upload-one/publish", []userRole{roleAdministrator, roleMaintainer}},
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
		{"external interface global control", http.MethodPost, "/config/external-interfaces/control", []userRole{roleAdministrator, roleMaintainer}},
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
			spec, ok := declaredSpecForRequest(application.routeSpecs, request)
			if !ok || spec.Auth != routeAuthSession {
				t.Fatalf("%s %s has no permission declaration", test.method, test.path)
			}
			for _, role := range roles {
				want := containsRole(test.allowed, role)
				if got := roleAllows(role, spec.Permission); got != want {
					t.Errorf("%s %s role=%s allowed=%v, want %v (permission=%d)", test.method, test.path, role, got, want, spec.Permission)
				}
			}
		})
	}
}

func TestUnknownProtectedRouteHasNoFallbackPermission(t *testing.T) {
	t.Parallel()
	application := &App{}
	application.routes()
	request := httptest.NewRequest(http.MethodGet, "/future/forgotten-route", nil)
	if spec, ok := declaredSpecForRequest(application.routeSpecs, request); ok {
		t.Fatalf("unknown route resolved to permission %d", spec.Permission)
	}
}

func TestEveryRouteDeclaresMethodAuthenticationAndMutationPolicy(t *testing.T) {
	t.Parallel()
	application := &App{}
	application.routes()
	if len(application.routeSpecs) < 200 {
		t.Fatalf("route inventory unexpectedly small: %d", len(application.routeSpecs))
	}
	seen := make(map[string]bool, len(application.routeSpecs))
	for _, spec := range application.routeSpecs {
		if spec.Method == "" || spec.Path == "" || spec.Auth == "" {
			t.Errorf("incomplete route declaration: %+v", spec)
		}
		if seen[spec.Pattern] {
			t.Errorf("duplicate route declaration: %s", spec.Pattern)
		}
		seen[spec.Pattern] = true
		if spec.Auth == routeAuthSession {
			if !roleAllows(roleAdministrator, spec.Permission) {
				t.Errorf("administrator cannot access declared route %s", spec.Pattern)
			}
			mutating := spec.Method != http.MethodGet && spec.Method != http.MethodHead
			if mutating && spec.CSRF != routeCSRFRequired {
				t.Errorf("mutating session route has no CSRF policy: %s", spec.Pattern)
			}
		}
	}
}

func TestHighRiskRoutesDeclareRecentAuthentication(t *testing.T) {
	t.Parallel()
	application := &App{}
	application.routes()
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/settings/users"},
		{http.MethodPost, "/settings/users/user-one/reset-password"},
		{http.MethodPost, "/config/external-interfaces/control"},
		{http.MethodPost, "/config/external-interfaces/keys/key-one/rotate"},
		{http.MethodPost, "/monitor/security/firewall/draft/apply"},
		{http.MethodPost, "/settings/updates/apply"},
		{http.MethodPost, "/resources/databases/backups/backup-one/restore"},
		{http.MethodPost, "/resources/inbox/upload-one/publish"},
		{http.MethodPost, "/monitor/websites/remotes"},
		{http.MethodPost, "/monitor/websites/remotes/source-one/delete"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		spec, ok := declaredSpecForRequest(application.routeSpecs, request)
		if !ok || !spec.StepUp {
			t.Errorf("%s %s does not declare step-up authentication", route.method, route.path)
		}
	}
}

func TestRouteMuxRejectsUndeclaredHandlersAndMethodlessPatterns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func()
	}{
		{"undeclared handler", func() {
			newDeclaredRouteMux().Handle("GET /unsafe", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		}},
		{"methodless public route", func() { newDeclaredRouteMux().Public("/unsafe", func(http.ResponseWriter, *http.Request) {}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("route registration did not fail closed")
				}
			}()
			test.call()
		})
	}
}

func declaredSpecForRequest(specs []RouteSpec, request *http.Request) (RouteSpec, bool) {
	mux := http.NewServeMux()
	var matched RouteSpec
	var found bool
	for _, route := range specs {
		spec := route
		mux.HandleFunc(spec.Pattern, func(http.ResponseWriter, *http.Request) {
			matched, found = spec, true
		})
	}
	mux.ServeHTTP(httptest.NewRecorder(), request)
	return matched, found
}

func containsRole(roles []userRole, target userRole) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}
