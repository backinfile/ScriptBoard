package runmanager

import (
	"slices"
	"strings"
	"testing"
)

func TestRunEnvironmentDoesNotInheritServiceSecretsOrProcessHooks(t *testing.T) {
	t.Parallel()

	environment := minimalRunEnvironment("linux", []string{
		"PATH=/attacker/bin",
		"HTTP_PROXY=http://proxy.internal",
		"AWS_SECRET_ACCESS_KEY=secret",
		"LD_PRELOAD=/tmp/hook.so",
		"LANG=zh_CN.UTF-8",
	}, "run-123", "/srv/jobs/backup.sh")

	for _, forbidden := range []string{"/attacker/bin", "HTTP_PROXY=", "AWS_SECRET_ACCESS_KEY=", "LD_PRELOAD="} {
		if slices.ContainsFunc(environment, func(value string) bool { return strings.Contains(value, forbidden) }) {
			t.Fatalf("run environment inherited forbidden value %q: %#v", forbidden, environment)
		}
	}
	for _, required := range []string{"SCRIPTBOARD_RUN_ID=run-123", "SCRIPTBOARD_SCRIPT_PATH=/srv/jobs/backup.sh"} {
		if !slices.Contains(environment, required) {
			t.Fatalf("run environment is missing %q: %#v", required, environment)
		}
	}
}

func TestWindowsRunEnvironmentKeepsOnlyRequiredSystemLocations(t *testing.T) {
	t.Parallel()

	environment := minimalRunEnvironment("windows", []string{
		`SystemRoot=C:\Windows`,
		`TEMP=C:\Service\Temp`,
		`USERPROFILE=C:\Users\Service`,
		`AZURE_CLIENT_SECRET=secret`,
	}, "run-456", `C:\jobs\backup.ps1`)

	for _, required := range []string{`SystemRoot=C:\Windows`, `PATH=C:\Windows\System32;C:\Windows`, `SCRIPTBOARD_RUN_ID=run-456`} {
		if !slices.Contains(environment, required) {
			t.Fatalf("Windows run environment is missing %q: %#v", required, environment)
		}
	}
	for _, forbidden := range []string{"USERPROFILE=", "AZURE_CLIENT_SECRET="} {
		if slices.ContainsFunc(environment, func(value string) bool { return strings.Contains(value, forbidden) }) {
			t.Fatalf("Windows run environment inherited %q: %#v", forbidden, environment)
		}
	}
}
