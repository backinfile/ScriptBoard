package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scriptboard/internal/identity"
)

func TestSettingsCategoriesPreserveRoleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		role              identity.Role
		count             int
		integrationStatus int
	}{
		{identity.RoleAdministrator, 5, 200}, {identity.RoleMaintainer, 4, 200}, {identity.RoleOperator, 1, 403}, {identity.RoleViewer, 1, 403},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			current := session{role: tc.role}
			nav := newSettingsNavigation(current, webLocale("en-US"), "embedding")
			var output bytes.Buffer
			if err := settingsHubTemplate.ExecuteTemplate(&output, "settings-navigation", nav); err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(output.String(), "<a href="); got != tc.count {
				t.Fatalf("links=%d want=%d: %s", got, tc.count, output.String())
			}
			request := httptest.NewRequest(http.MethodGet, "/settings/integrations", nil)
			request = request.WithContext(context.WithValue(request.Context(), sessionContextKey, current))
			response := httptest.NewRecorder()
			(&App{}).settingsHubPage(response, request)
			if response.Code != tc.integrationStatus {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestBrowserPreferencesAreAvailableToEveryRole(t *testing.T) {
	application := &App{}
	application.routes()
	spec, ok := declaredSpecForRequest(application.routeSpecs, httptest.NewRequest(http.MethodGet, "/settings/display", nil))
	if !ok {
		t.Fatal("missing display route")
	}
	for _, role := range []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer} {
		if !identity.Allows(role, spec.Permission) {
			t.Errorf("display denied to %s", role)
		}
	}
}

func TestPersonalMCPAccessRemainsAvailableWithoutManagementPermission(t *testing.T) {
	for _, role := range []identity.Role{identity.RoleAdministrator, identity.RoleMaintainer, identity.RoleOperator, identity.RoleViewer} {
		t.Run(string(role), func(t *testing.T) {
			nav := newSettingsNavigation(session{role: role}, webLocale("en-US"), "account")
			var output bytes.Buffer
			if err := accountTemplate.Execute(&output, map[string]any{"Locale": webLocale("en-US"), "SettingsNavigation": nav}); err != nil {
				t.Fatal(err)
			}
			visible := strings.Contains(output.String(), webText(webLocale("en-US"), "settings.agent_connections"))
			if visible == nav.CanManageExecution {
				t.Fatalf("personal MCP section visible=%v, management=%v", visible, nav.CanManageExecution)
			}
		})
	}
}
