package runmanager

import (
	"slices"
	"testing"
)

func TestRunEnvironmentInheritsParentAndOverwritesScriptBoardBindings(t *testing.T) {
	t.Parallel()

	parent := []string{
		"PATH=/attacker/bin",
		"HTTP_PROXY=http://proxy.internal",
		"AWS_SECRET_ACCESS_KEY=secret",
		"LD_PRELOAD=/tmp/hook.so",
		"LANG=zh_CN.UTF-8",
		"SCRIPTBOARD_RUN_ID=old-run",
		"SCRIPTBOARD_SCRIPT_PATH=/old/script.sh",
	}
	environment := runEnvironmentFromParent(parent, "run-123", "/srv/jobs/backup.sh")

	for _, required := range []string{
		"PATH=/attacker/bin",
		"HTTP_PROXY=http://proxy.internal",
		"AWS_SECRET_ACCESS_KEY=secret",
		"LD_PRELOAD=/tmp/hook.so",
		"LANG=zh_CN.UTF-8",
		"SCRIPTBOARD_RUN_ID=run-123",
		"SCRIPTBOARD_SCRIPT_PATH=/srv/jobs/backup.sh",
	} {
		if !slices.Contains(environment, required) {
			t.Fatalf("run environment missing %q: %#v", required, environment)
		}
	}
	if slices.Contains(environment, "SCRIPTBOARD_RUN_ID=old-run") || slices.Contains(environment, "SCRIPTBOARD_SCRIPT_PATH=/old/script.sh") {
		t.Fatalf("run environment did not overwrite ScriptBoard bindings: %#v", environment)
	}
	parent[0] = "PATH=/changed"
	if slices.Contains(environment, "PATH=/changed") {
		t.Fatalf("run environment retained caller slice backing array: %#v", environment)
	}
}

func TestWindowsRunEnvironmentPreservesSystemAndUserProfileEnvironment(t *testing.T) {
	t.Parallel()

	environment := runEnvironmentFromParent([]string{
		`SystemRoot=C:\Windows`,
		`ProgramData=D:\ProgramData`,
		`PATH=C:\Python314;C:\Windows\System32;C:\Windows`,
		`TEMP=C:\Service\Temp`,
		`USERPROFILE=C:\Users\Service`,
		`AZURE_CLIENT_SECRET=secret`,
	}, "run-456", `C:\jobs\backup.ps1`)

	for _, required := range []string{
		`SystemRoot=C:\Windows`,
		`ProgramData=D:\ProgramData`,
		`PATH=C:\Python314;C:\Windows\System32;C:\Windows`,
		`TEMP=C:\Service\Temp`,
		`USERPROFILE=C:\Users\Service`,
		`AZURE_CLIENT_SECRET=secret`,
		`SCRIPTBOARD_RUN_ID=run-456`,
		`SCRIPTBOARD_SCRIPT_PATH=C:\jobs\backup.ps1`,
	} {
		if !slices.Contains(environment, required) {
			t.Fatalf("Windows run environment is missing %q: %#v", required, environment)
		}
	}
}
