//go:build windows

package platformservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestWindowsWebServiceIdentityIsNotHighlyPrivileged(t *testing.T) {
	configuration := mgr.Config{
		ServiceStartName: webServiceAccount,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	identity := strings.ToLower(configuration.ServiceStartName)
	if identity == "" || strings.Contains(identity, "localsystem") || strings.HasSuffix(identity, `\system`) {
		t.Fatalf("Web service identity is privileged: %q", configuration.ServiceStartName)
	}
	if configuration.SidType == windows.SERVICE_SID_TYPE_NONE {
		t.Fatal("Web service has no per-service SID")
	}
}

func TestWindowsServiceDirectoryGrantPropagatesToExistingChildren(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(child, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	localService, err := windows.StringToSid("S-1-5-19")
	if err != nil {
		t.Fatal(err)
	}
	if err := grantWindowsPathAccess(root, localService, windows.ACCESS_MASK(windows.FILE_GENERIC_READ), true); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(child, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(descriptor.String(), ";;;LS)") {
		t.Fatalf("existing child did not inherit LocalService grant: %s", descriptor.String())
	}
}
