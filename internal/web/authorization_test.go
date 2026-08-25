package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scriptboard/internal/identity"
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
		allowed []identity.Role
	}{
		{"operations html", http.MethodGet, "/monitor", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"operations json", http.MethodGet, "/monitor/data", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"application log sse", http.MethodGet, "/monitor/applications/docker:one/logs/events", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"application log download", http.MethodGet, "/monitor/applications/docker:one/logs/download", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"Kubernetes log download", http.MethodGet, "/monitor/kubernetes/clusters/cluster-one/workloads/default/Deployment/api/logs/download", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"run log sse", http.MethodGet, "/history/runs/run-one/events", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"run log history", http.MethodGet, "/history/runs/run-one/history", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"run text download", http.MethodGet, "/history/runs/run-one/download", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"manage monitor", http.MethodPost, "/monitor/websites/site-one/check", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"export monitor configurations", http.MethodGet, "/monitor/websites/export", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"import monitor configurations", http.MethodGet, "/monitor/websites/import", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"file listing", http.MethodGet, "/resources/files", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"directory json", http.MethodGet, "/resources/directories", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"file download", http.MethodGet, "/resources/files/download?path=C%3A%5Cscripts%5Cscript.ps1", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"file log download", http.MethodGet, "/resources/files/log/download?path=C%3A%5Clogs%5Cservice.log", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"file upload task", http.MethodGet, "/resources/files/upload", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"file move task", http.MethodGet, "/resources/files/move?path=C%3A%5Cscripts%5Cscript.ps1", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"file mutation", http.MethodPost, "/resources/files/delete", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"external upload inbox", http.MethodGet, "/resources/inbox", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"publish external upload", http.MethodPost, "/resources/inbox/upload-one/publish", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"file quick access pin", http.MethodPost, "/resources/files/quick-access", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"file script task", http.MethodGet, "/resources/files/run?path=C%3A%5Cscripts%5Cscript.ps1", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"quick run start", http.MethodPost, "/config/quick-runs/quick-one/start", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator}},
		{"one time source", http.MethodPost, "/config/quick-runs/one-time", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"schedule list", http.MethodGet, "/config/schedules", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"schedule run now", http.MethodPost, "/config/schedules/schedule-one/run", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"variables", http.MethodGet, "/resources/variables", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"variable version increment", http.MethodPost, "/resources/variables/RELEASE_VERSION/increment", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"mysql databases", http.MethodGet, "/resources/databases", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"mysql backup mutation", http.MethodPost, "/resources/databases/instances/instance-one/backup", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"external interfaces", http.MethodGet, "/config/external-interfaces", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"external interface global control", http.MethodPost, "/config/external-interfaces/control", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"external interface mutation", http.MethodPost, "/config/external-interfaces/keys/key-one/toggle", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"audit html", http.MethodGet, "/history/audit", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"audit download", http.MethodGet, "/history/audit.csv", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"audit service logs", http.MethodGet, "/history/audit/service-logs", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"audit service logs download", http.MethodGet, "/history/audit/service-logs.csv", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"audit service logs text download", http.MethodGet, "/history/audit/service-logs.txt", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"system settings", http.MethodGet, "/settings/updates/status", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"Docker insecure Registry configuration", http.MethodPost, "/config/dashboard-cards/card-one/registry/insecure", []identity.Role{identity.RoleAdministrator}},
		{"restart service", http.MethodPost, "/settings/updates/restart", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer}},
		{"user management", http.MethodGet, "/settings/users", []identity.Role{identity.RoleAdministrator}},
		{"user edit task", http.MethodGet, "/settings/users/user-one/edit", []identity.Role{identity.RoleAdministrator}},
		{"own account", http.MethodPost, "/settings/account", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"own password task", http.MethodGet, "/settings/account/password", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
		{"own username task", http.MethodGet, "/settings/account/username", []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}},
	}

	roles := []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			spec, ok := declaredSpecForRequest(application.routeSpecs, request)
			if !ok || spec.Auth != routeAuthSession {
				t.Fatalf("%s %s has no permission declaration", test.method, test.path)
			}
			for _, role := range roles {
				want := containsRole(test.allowed, role)
				if got := identity.Allows(role, spec.Permission); got != want {
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
			if !identity.Allows(identity.RoleAdministrator, spec.Permission) {
				t.Errorf("administrator cannot access declared route %s", spec.Pattern)
			}
			mutating := spec.Method != http.MethodGet && spec.Method != http.MethodHead
			if mutating && spec.CSRF != routeCSRFRequired {
				t.Errorf("mutating session route has no CSRF policy: %s", spec.Pattern)
			}
		}
	}
}

func TestExternalTriggerRoutesShareOneExplicitAbsoluteBodyLimit(t *testing.T) {
	t.Parallel()
	application := &App{}
	application.routes()
	for _, path := range []string{"/trigger", "/trigger/example", "/trigger/group/example"} {
		spec, ok := declaredSpecForRequest(application.routeSpecs, httptest.NewRequest(http.MethodPost, path, nil))
		if !ok || spec.Auth != routeAuthExternal || spec.MaxBodyBytes != maxExternalRequestBytes {
			t.Fatalf("POST %s spec=%+v declared=%v", path, spec, ok)
		}
	}
}

func TestSessionRouteRejectsChunkedFormThatExceedsItsBodyLimit(t *testing.T) {
	t.Parallel()

	const maximum = int64(64)
	body := "csrf_token=valid&name=node&padding=" + strings.Repeat("x", 128)
	request := httptest.NewRequest(http.MethodPost, "http://scriptboard.test/config", strings.NewReader(body))
	request.ContentLength = -1 // Exercise the streaming limit instead of the header fast path.
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	called := false
	handler := enforceRouteRequestPolicy(RouteSpec{
		Method: http.MethodPost, Path: "/config", Auth: routeAuthSession, MaxBodyBytes: maximum,
	}, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called = request.FormValue("csrf_token") == "valid" && request.FormValue("name") == "node"
		response.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(recorder, request)

	if called {
		t.Fatal("oversized chunked form reached the state-changing handler with its leading fields intact")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunked form status=%d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
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
		{http.MethodPost, "/settings/ai/llms"},
		{http.MethodPost, "/settings/ai/llms/model-one/delete"},
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

func containsRole(roles []identity.Role, target identity.Role) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}
