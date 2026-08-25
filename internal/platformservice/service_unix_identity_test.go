//go:build !windows

package platformservice

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemoveLegacyAIUnitsStopsAndDeletesRetiredUnits(t *testing.T) {
	root := t.TempDir()
	servicePath := filepath.Join(root, "scriptboard-ai.service")
	socketPath := filepath.Join(root, "scriptboard-ai.socket")
	for _, path := range []string{servicePath, socketPath} {
		if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	if err := removeLegacyAIUnits(func(arguments ...string) error {
		calls = append(calls, append([]string(nil), arguments...))
		return nil
	}, servicePath, socketPath); err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{{"disable", "--now", "scriptboard-ai.socket"}, {"stop", "scriptboard-ai.service"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", calls, wantCalls)
	}
	for _, path := range []string{servicePath, socketPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy unit %q still exists: %v", path, err)
		}
	}
}

func TestLinuxWebServiceIdentityIsDedicated(t *testing.T) {
	if webServiceUser == "" || webServiceUser == "root" || webServiceUser == "nobody" {
		t.Fatalf("Web service identity is not dedicated: %q", webServiceUser)
	}
}

func TestLinuxRunnerIdentityIsDedicatedAndSeparate(t *testing.T) {
	if runnerServiceUser == "" || runnerServiceUser == "root" || runnerServiceUser == webServiceUser {
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
	if policy := linuxRunnerServicePolicy(RunnerIdentityPrivileged); !strings.Contains(policy, "MemoryMax=2G") || !strings.Contains(policy, "MemorySwapMax=0") || !strings.Contains(policy, "TasksMax=64") {
		t.Fatalf("privileged Runner is missing host resource limits: %q", policy)
	} else if strings.Contains(policy, "IPAddressDeny=any") {
		t.Fatalf("privileged Runner unexpectedly received isolated network policy: %q", policy)
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

func TestLinuxRunnerUnitRequiresSeccompAndNetworkIsolation(t *testing.T) {
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
		"RestrictAddressFamilies=AF_UNIX", "IPAddressDeny=any",
		"Requires=scriptboard-broker.service scriptboard-runner.socket",
		"FileDescriptorName=scriptboard-runner",
		"systemctl(\"enable\", \"scriptboard-runner.socket\")",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Linux runtime units are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"systemctl(\"enable\", \"scriptboard-runner.service\")",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Linux runtime service is still enabled eagerly: %q", forbidden)
		}
	}
	if count := strings.Count(text, "MemorySwapMax=0"); count != 1 {
		t.Fatalf("Linux Runner unit requires a swap limit, found %d", count)
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

func TestSystemdSocketAddressIsNotShellQuoted(t *testing.T) {
	endpoint := "/run/scriptboard-runner/runtime-test.sock"
	if got := systemdSocketAddress(endpoint); got != endpoint {
		t.Fatalf("systemd socket address = %q, want %q", got, endpoint)
	}
}

func TestLinuxStateParentAllowsServiceIdentityTraversal(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "scriptboard")
	stateRoot := filepath.Join(parent, "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareLinuxStateParent(stateRoot); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o711 {
		t.Fatalf("state parent mode = %#o, want 0711", got)
	}
}
