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

func TestLinuxRunnerDefaultsToPrivilegedRoot(t *testing.T) {
	user, group := linuxRunnerServiceAccount(RunnerIdentityPrivileged)
	if user != "root" || group != "root" {
		t.Fatalf("privileged Runner account = %s:%s", user, group)
	}
	user, group = linuxRunnerServiceAccount(RunnerIdentityIsolated)
	if user != runnerServiceUser || group != runnerServiceUser {
		t.Fatalf("isolated Runner account = %s:%s", user, group)
	}
	if policy := linuxRunnerServicePolicy(RunnerIdentityPrivileged); policy != "" {
		t.Fatalf("privileged Runner should not receive isolated systemd policy: %q", policy)
	}
	if policy := linuxRunnerServicePolicy(RunnerIdentityIsolated); !strings.Contains(policy, "IPAddressDeny=any") {
		t.Fatalf("isolated Runner policy is missing network denial: %q", policy)
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
		"Requires=scriptboard-broker.service scriptboard-ai.socket scriptboard-runner.socket",
		"FileDescriptorName=scriptboard-ai", "FileDescriptorName=scriptboard-runner",
		"systemctl(\"enable\", \"scriptboard-ai.socket\")", "systemctl(\"enable\", \"scriptboard-runner.socket\")",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Linux runtime units are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"systemctl(\"enable\", \"scriptboard-ai.service\")",
		"systemctl(\"enable\", \"scriptboard-runner.service\")",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Linux runtime service is still enabled eagerly: %q", forbidden)
		}
	}
	if count := strings.Count(text, "MemorySwapMax=0"); count != 2 {
		t.Fatalf("Linux runtime units require two swap limits, found %d", count)
	}
}

func TestLinuxWebStartupSecretsAreNotSharedWithRunnerGroup(t *testing.T) {
	source, err := os.ReadFile("service_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "os.Chown(path, uid, -1)") || !strings.Contains(text, "os.Chmod(path, 0o600)") {
		t.Fatal("Linux Web startup files are not assigned to the Web identity with owner-only access")
	}
	if strings.Contains(text, "assign Linux Web startup file group") {
		t.Fatal("Linux Web startup files are still shared through the Web group used by Runner")
	}
}
