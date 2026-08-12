//go:build !windows

package platformservice

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxWebServiceIdentityIsDedicated(t *testing.T) {
	if webServiceUser == "" || webServiceUser == "root" || webServiceUser == "nobody" {
		t.Fatalf("Web service identity is not dedicated: %q", webServiceUser)
	}
}

func TestLinuxAIRuntimeIdentityIsDedicatedAndSeparate(t *testing.T) {
	if aiServiceUser == "" || aiServiceUser == "root" || aiServiceUser == "nobody" || aiServiceUser == webServiceUser {
		t.Fatalf("AI Runtime service identity is not dedicated: %q", aiServiceUser)
	}
}

func TestLinuxRunnerIdentityIsDedicatedAndSeparate(t *testing.T) {
	if runnerServiceUser == "" || runnerServiceUser == "root" || runnerServiceUser == webServiceUser || runnerServiceUser == aiServiceUser {
		t.Fatalf("Runner service identity is not dedicated: %q", runnerServiceUser)
	}
}

func TestLinuxManagedWebRuntimeRejectsRootAndOtherUsers(t *testing.T) {
	if err := validateLinuxWebRuntimeIdentity(0, 1200); err == nil {
		t.Fatal("root identity was accepted")
	}
	if err := validateLinuxWebRuntimeIdentity(1201, 1200); err == nil {
		t.Fatal("different identity was accepted")
	}
	if err := validateLinuxWebRuntimeIdentity(1200, 1200); err != nil {
		t.Fatalf("dedicated identity rejected: %v", err)
	}
}

func TestLinuxRuntimeUnitsRequireSeccompAndNetworkIsolation(t *testing.T) {
	source, err := os.ReadFile("service_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"MemoryDenyWriteExecute=true",
		"MemorySwapMax=0",
		"SystemCallArchitectures=native", "SystemCallFilter=@system-service", "SystemCallErrorNumber=EPERM",
		"PrivateDevices=true", "ProtectKernelLogs=true", "RestrictRealtime=true",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "IPAddressAllow=localhost",
		"RestrictAddressFamilies=AF_UNIX", "IPAddressDeny=any",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Linux runtime units are missing %q", required)
		}
	}
	if count := strings.Count(text, "MemorySwapMax=0"); count != 2 {
		t.Fatalf("Linux runtime units require two swap limits, found %d", count)
	}
}
