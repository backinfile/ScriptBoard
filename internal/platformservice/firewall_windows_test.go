//go:build windows

package platformservice

import (
	"errors"
	"path/filepath"
	"testing"
)

type fakeServiceRestriction struct {
	restrictions []restrictionCall
	removed      []string
	allowed      []restrictionCall
	fail         string
}

type restrictionCall struct {
	service, executable string
	enabled, restricted bool
}

func TestWindowsServiceHardeningCOMIsAvailable(t *testing.T) {
	_, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		t.Fatalf("open Windows Service Hardening policy: %v", err)
	}
	closePolicy()
}

func (fake *fakeServiceRestriction) Restrict(service, executable string, enabled, restricted bool) error {
	if fake.fail == "restrict:"+service {
		return errors.New("injected restriction failure")
	}
	fake.restrictions = append(fake.restrictions, restrictionCall{service, executable, enabled, restricted})
	return nil
}

func (fake *fakeServiceRestriction) RemoveRule(name string) error {
	if fake.fail == "remove" {
		return errors.New("injected remove failure")
	}
	fake.removed = append(fake.removed, name)
	return nil
}

func (fake *fakeServiceRestriction) AddLoopbackRule(name, service, executable string) error {
	if fake.fail == "allow" {
		return errors.New("injected allow failure")
	}
	fake.allowed = append(fake.allowed, restrictionCall{service: service, executable: executable})
	return nil
}

func TestWindowsRuntimeFirewallUsesServiceHardeningDefaultDeny(t *testing.T) {
	aiExecutable := filepath.Join(t.TempDir(), "scriptboard-ai-host.exe")
	runnerExecutable := filepath.Join(t.TempDir(), "scriptboard-runner.exe")
	fake := &fakeServiceRestriction{}
	if err := applyWindowsRuntimeFirewall(fake, aiExecutable, runnerExecutable); err != nil {
		t.Fatal(err)
	}
	if len(fake.restrictions) != 2 {
		t.Fatalf("restrictions=%#v", fake.restrictions)
	}
	for _, call := range fake.restrictions {
		if !call.enabled || !call.restricted {
			t.Fatalf("service restriction is not fail closed: %#v", call)
		}
	}
	if fake.restrictions[0].service != aiServiceName || fake.restrictions[1].service != runnerServiceName {
		t.Fatalf("wrong service restrictions: %#v", fake.restrictions)
	}
	if len(fake.allowed) != 1 || fake.allowed[0].service != aiServiceName || fake.allowed[0].executable != aiExecutable {
		t.Fatalf("only AI should receive the loopback exception: %#v", fake.allowed)
	}
	if len(fake.removed) != 1 || fake.removed[0] != aiLoopbackFirewallRuleName {
		t.Fatalf("stale owned rules not replaced: %#v", fake.removed)
	}
}

func TestWindowsRuntimeFirewallFailsClosedWhenRestrictionOrAllowFails(t *testing.T) {
	aiExecutable := filepath.Join(t.TempDir(), "scriptboard-ai-host.exe")
	runnerExecutable := filepath.Join(t.TempDir(), "scriptboard-runner.exe")
	for _, failure := range []string{"restrict:" + aiServiceName, "allow", "restrict:" + runnerServiceName} {
		t.Run(failure, func(t *testing.T) {
			if err := applyWindowsRuntimeFirewall(&fakeServiceRestriction{fail: failure}, aiExecutable, runnerExecutable); err == nil {
				t.Fatal("firewall installation unexpectedly succeeded")
			}
		})
	}
}

func TestWindowsRuntimeFirewallRemovalDisablesBothRestrictions(t *testing.T) {
	fake := &fakeServiceRestriction{}
	aiExecutable := filepath.Join(t.TempDir(), "scriptboard-ai-host.exe")
	runnerExecutable := filepath.Join(t.TempDir(), "scriptboard-runner.exe")
	if err := removeWindowsRuntimeFirewallWith(fake, aiExecutable, runnerExecutable); err != nil {
		t.Fatal(err)
	}
	if len(fake.restrictions) != 2 || fake.restrictions[0].enabled || fake.restrictions[1].enabled {
		t.Fatalf("restrictions were not removed: %#v", fake.restrictions)
	}
}

func TestWindowsRuntimeFirewallRemovalHandlesPartialInstallation(t *testing.T) {
	fake := &fakeServiceRestriction{}
	aiExecutable := filepath.Join(t.TempDir(), "scriptboard-ai-host.exe")
	if err := removeWindowsRuntimeFirewallWith(fake, aiExecutable, ""); err != nil {
		t.Fatal(err)
	}
	if len(fake.restrictions) != 1 || fake.restrictions[0].service != aiServiceName || fake.restrictions[0].enabled {
		t.Fatalf("partial restriction cleanup=%#v", fake.restrictions)
	}
}

func TestWindowsRuntimeFirewallUpdateRestrictsNewPathsBeforeRetiringOldPaths(t *testing.T) {
	root := t.TempDir()
	oldAI := filepath.Join(root, "old", "scriptboard-ai-host.exe")
	oldRunner := filepath.Join(root, "old", "scriptboard-runner.exe")
	newAI := filepath.Join(root, "new", "scriptboard-ai-host.exe")
	newRunner := filepath.Join(root, "new", "scriptboard-runner.exe")
	fake := &fakeServiceRestriction{}
	if err := retireWindowsRuntimeFirewallWith(fake, oldAI, oldRunner, newAI, newRunner); err != nil {
		t.Fatal(err)
	}
	if len(fake.restrictions) != 2 || fake.restrictions[0].enabled || fake.restrictions[1].enabled {
		t.Fatalf("old restrictions were not retired: %#v", fake.restrictions)
	}
	fake.restrictions = nil
	if err := retireWindowsRuntimeFirewallWith(fake, newAI, newRunner, newAI, newRunner); err != nil {
		t.Fatal(err)
	}
	if len(fake.restrictions) != 0 {
		t.Fatalf("current restrictions were disabled: %#v", fake.restrictions)
	}
}
