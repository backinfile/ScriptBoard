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

func TestWindowsRunnerUsesSeparateServiceSID(t *testing.T) {
	if strings.EqualFold(runnerServiceSID, webServiceSID) || strings.EqualFold(runnerServiceSID, aiServiceSID) {
		t.Fatalf("Runner service SID is not separate: %q", runnerServiceSID)
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

func TestWindowsRunnerAndAIHostAreDemandStartServices(t *testing.T) {
	source, err := os.ReadFile("service_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Count(text, "StartType: mgr.StartManual") < 2 || strings.Count(text, "current.StartType = mgr.StartManual") < 2 {
		t.Fatal("Windows Runner and AI Host are not both installed and upgraded as demand-start services")
	}
	if !strings.Contains(text, "Dependencies: []string{brokerServiceName}") {
		t.Fatal("managed Web must depend only on the resident Broker")
	}
	if !strings.Contains(text, "windows.SERVICE_START | windows.SERVICE_QUERY_STATUS") ||
		!strings.Contains(text, "[]string{aiServiceName, runnerServiceName}") {
		t.Fatal("managed Web is not granted narrow demand-start access to both isolated services")
	}
	onDemandSource, err := os.ReadFile("ondemand_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	onDemandText := string(onDemandSource)
	if !strings.Contains(onDemandText, "windows.SC_MANAGER_CONNECT") ||
		!strings.Contains(onDemandText, "windows.SERVICE_START|windows.SERVICE_QUERY_STATUS") {
		t.Fatal("demand-start client requests broader SCM or service access than its narrow grant")
	}
}

func TestWindowsManagedServicesHaveBoundedCrashRecovery(t *testing.T) {
	if len(windowsRecoveryActions) != 3 {
		t.Fatalf("recovery actions=%d, want two restarts and a terminal no-op", len(windowsRecoveryActions))
	}
	if windowsRecoveryActions[0].Type != mgr.ServiceRestart || windowsRecoveryActions[1].Type != mgr.ServiceRestart ||
		windowsRecoveryActions[2].Type != mgr.NoAction {
		t.Fatalf("unexpected recovery actions: %#v", windowsRecoveryActions)
	}
	if windowsRecoveryActions[0].Delay <= 0 || windowsRecoveryActions[1].Delay <= windowsRecoveryActions[0].Delay ||
		windowsRecoveryResetSeconds != 24*60*60 {
		t.Fatalf("recovery policy is not bounded with backoff: %#v reset=%d", windowsRecoveryActions, windowsRecoveryResetSeconds)
	}
}

func TestWindowsServiceDirectoryGrantPropagatesToExistingChildren(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(child, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	childDescriptor, err := windows.GetNamedSecurityInfo(child, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	childACL, _, err := childDescriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(child, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, childACL, nil); err != nil {
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
