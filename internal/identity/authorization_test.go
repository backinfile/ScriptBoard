package identity

import (
	"testing"
	"time"
)

func TestRoleMatrix(t *testing.T) {
	if !Allows(RoleAdministrator, PermissionManageUsers) {
		t.Fatal("administrator must manage users")
	}
	if Allows(RoleMaintainer, PermissionManageUsers) {
		t.Fatal("maintainer must not manage users")
	}
	if !Allows(RoleOperator, PermissionExecute) || Allows(RoleOperator, PermissionWriteFiles) {
		t.Fatal("operator permissions changed")
	}
	if !Allows(RoleViewer, PermissionObserve) || Allows(RoleViewer, PermissionReadFiles) {
		t.Fatal("viewer permissions changed")
	}
}

func TestRecentAuthenticationRejectsFutureAndExpiredValues(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if !RecentAuthenticationValid(now.Add(-time.Minute).Unix(), now) {
		t.Fatal("recent authentication should be valid")
	}
	if RecentAuthenticationValid(now.Add(2*time.Minute).Unix(), now) {
		t.Fatal("future authentication should be invalid")
	}
	if RecentAuthenticationValid(now.Add(-RecentAuthenticationWindow-time.Second).Unix(), now) {
		t.Fatal("expired authentication should be invalid")
	}
}
