//go:build windows

package platformservice

import (
	"path/filepath"
	"testing"
)

type fakeWindowsServiceRestriction struct {
	restrictions []struct {
		service, executable string
		enabled             bool
	}
}

func (fake *fakeWindowsServiceRestriction) Restrict(service, executable string, enabled, _ bool) error {
	fake.restrictions = append(fake.restrictions, struct {
		service, executable string
		enabled             bool
	}{service, executable, enabled})
	return nil
}

func TestWindowsRunnerFirewallMatchesIdentityMode(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "scriptboard-runner.exe")
	for _, test := range []struct {
		name    string
		mode    string
		enabled bool
	}{{"isolated", RunnerIdentityIsolated, true}, {"privileged", RunnerIdentityPrivileged, false}} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeWindowsServiceRestriction{}
			if err := applyWindowsRunnerFirewall(fake, executable, test.mode); err != nil {
				t.Fatal(err)
			}
			if len(fake.restrictions) != 1 || fake.restrictions[0].service != runnerServiceName || fake.restrictions[0].enabled != test.enabled {
				t.Fatalf("restrictions=%#v", fake.restrictions)
			}
		})
	}
}

func TestWindowsRunnerFirewallRequiresAbsoluteExecutable(t *testing.T) {
	if err := applyWindowsRunnerFirewall(&fakeWindowsServiceRestriction{}, "scriptboard-runner.exe", RunnerIdentityIsolated); err == nil {
		t.Fatal("relative Runner executable was accepted")
	}
}
