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

func TestWindowsAIRuntimeUsesSeparateServiceSID(t *testing.T) {
	if strings.EqualFold(aiServiceSID, webServiceSID) || !strings.HasPrefix(strings.ToLower(aiServiceSID), `nt service\`) {
		t.Fatalf("AI Runtime service SID is not separate: %q", aiServiceSID)
	}
}

func TestWindowsManagedWebRuntimeRequiresLowIdentityAndServiceSID(t *testing.T) {
	if err := validateWindowsWebRuntimeIdentity(false, true); err == nil {
		t.Fatal("non-LocalService token was accepted")
	}
	if err := validateWindowsWebRuntimeIdentity(true, false); err == nil {
		t.Fatal("token without service SID was accepted")
	}
	if err := validateWindowsWebRuntimeIdentity(true, true); err != nil {
		t.Fatalf("expected managed token rejected: %v", err)
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
