//go:build windows

package platformservice

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const aiLoopbackFirewallRuleName = "ScriptBoard AI Runtime loopback provider access"

type windowsServiceRestriction interface {
	Restrict(serviceName, executable string, enabled, restrictedSID bool) error
	RemoveRule(name string) error
	AddLoopbackRule(name, serviceName, executable string) error
}

func configureWindowsRuntimeFirewall(aiExecutable, runnerExecutable, runnerIdentityMode string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	return applyWindowsRuntimeFirewall(policy, aiExecutable, runnerExecutable, runnerIdentityMode)
}

func applyWindowsRuntimeFirewall(policy windowsServiceRestriction, aiExecutable, runnerExecutable, runnerIdentityMode string) error {
	if !filepath.IsAbs(aiExecutable) || !filepath.IsAbs(runnerExecutable) {
		return errors.New("Windows runtime firewall executable paths must be absolute")
	}
	// Windows Service Hardening installs kernel-enforced block-all inbound and
	// outbound filters. AI receives one exact outbound loopback exception for
	// the Web-owned Provider proxy. Runner is restricted only in isolated mode;
	// privileged mode is the full host-control default.
	if err := policy.Restrict(aiServiceName, aiExecutable, true, true); err != nil {
		return fmt.Errorf("restrict Windows AI Runtime network: %w", err)
	}
	if err := policy.RemoveRule(aiLoopbackFirewallRuleName); err != nil && !isWindowsFirewallRuleNotFound(err) {
		return fmt.Errorf("replace Windows AI Runtime loopback rule: %w", err)
	}
	if err := policy.AddLoopbackRule(aiLoopbackFirewallRuleName, aiServiceName, aiExecutable); err != nil {
		return fmt.Errorf("allow Windows AI Runtime loopback proxy: %w", err)
	}
	if runnerIdentityMode != RunnerIdentityIsolated {
		if err := policy.Restrict(runnerServiceName, runnerExecutable, false, true); err != nil {
			return fmt.Errorf("remove Windows Runner network restriction: %w", err)
		}
		return nil
	}
	if err := policy.Restrict(runnerServiceName, runnerExecutable, true, true); err != nil {
		return fmt.Errorf("restrict Windows Runner network: %w", err)
	}
	return nil
}

func removeWindowsRuntimeFirewall(aiExecutable, runnerExecutable string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	return removeWindowsRuntimeFirewallWith(policy, aiExecutable, runnerExecutable)
}

func removeWindowsRuntimeFirewallWith(policy windowsServiceRestriction, aiExecutable, runnerExecutable string) error {
	if err := policy.RemoveRule(aiLoopbackFirewallRuleName); err != nil && !isWindowsFirewallRuleNotFound(err) {
		return err
	}
	for _, runtimeService := range []struct{ name, executable string }{
		{aiServiceName, aiExecutable}, {runnerServiceName, runnerExecutable},
	} {
		if runtimeService.executable == "" {
			continue
		}
		if err := policy.Restrict(runtimeService.name, runtimeService.executable, false, true); err != nil {
			return fmt.Errorf("remove Windows Service Hardening restriction for %s: %w", runtimeService.name, err)
		}
	}
	return nil
}

func retireWindowsRuntimeFirewall(oldAI, oldRunner, newAI, newRunner string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	return retireWindowsRuntimeFirewallWith(policy, oldAI, oldRunner, newAI, newRunner)
}

func retireWindowsRuntimeFirewallWith(policy windowsServiceRestriction, oldAI, oldRunner, newAI, newRunner string) error {
	for _, runtimeService := range []struct{ name, oldExecutable, newExecutable string }{
		{aiServiceName, oldAI, newAI}, {runnerServiceName, oldRunner, newRunner},
	} {
		if sameWindowsPath(runtimeService.oldExecutable, runtimeService.newExecutable) {
			continue
		}
		if err := policy.Restrict(runtimeService.name, runtimeService.oldExecutable, false, true); err != nil {
			return fmt.Errorf("retire previous Windows Service Hardening restriction for %s: %w", runtimeService.name, err)
		}
	}
	return nil
}

type oleWindowsServiceRestriction struct {
	serviceRestriction *ole.IDispatch
	rules              *ole.IDispatch
}

func openWindowsServiceRestriction() (*oleWindowsServiceRestriction, func(), error) {
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil && !isOLECode(err, 1) {
		runtime.UnlockOSThread()
		return nil, func() {}, fmt.Errorf("initialize Windows firewall COM: %w", err)
	}
	cleanup := func() {
		ole.CoUninitialize()
		runtime.UnlockOSThread()
	}
	unknown, err := oleutil.CreateObject("HNetCfg.FwPolicy2")
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("create Windows firewall policy: %w", err)
	}
	defer unknown.Release()
	policy, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	restrictionVariant, err := oleutil.GetProperty(policy, "ServiceRestriction")
	policy.Release()
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("get Windows Service Hardening policy: %w", err)
	}
	serviceRestriction := restrictionVariant.ToIDispatch()
	if serviceRestriction == nil {
		restrictionVariant.Clear()
		cleanup()
		return nil, func() {}, errors.New("Windows Service Hardening policy is unavailable")
	}
	rulesVariant, err := oleutil.GetProperty(serviceRestriction, "Rules")
	if err != nil {
		restrictionVariant.Clear()
		cleanup()
		return nil, func() {}, fmt.Errorf("get Windows Service Hardening rules: %w", err)
	}
	rules := rulesVariant.ToIDispatch()
	if rules == nil {
		rulesVariant.Clear()
		restrictionVariant.Clear()
		cleanup()
		return nil, func() {}, errors.New("Windows Service Hardening rule collection is unavailable")
	}
	closePolicy := func() {
		rulesVariant.Clear()
		restrictionVariant.Clear()
		cleanup()
	}
	return &oleWindowsServiceRestriction{serviceRestriction: serviceRestriction, rules: rules}, closePolicy, nil
}

func (policy *oleWindowsServiceRestriction) Restrict(serviceName, executable string, enabled, restrictedSID bool) error {
	result, err := oleutil.CallMethod(policy.serviceRestriction, "RestrictService", serviceName, executable, enabled, restrictedSID)
	if result != nil {
		result.Clear()
	}
	return err
}

func (policy *oleWindowsServiceRestriction) RemoveRule(name string) error {
	result, err := oleutil.CallMethod(policy.rules, "Remove", name)
	if result != nil {
		result.Clear()
	}
	return err
}

func (policy *oleWindowsServiceRestriction) AddLoopbackRule(name, serviceName, executable string) error {
	unknown, err := oleutil.CreateObject("HNetCfg.FWRule")
	if err != nil {
		return err
	}
	defer unknown.Release()
	rule, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer rule.Release()
	for _, property := range []struct {
		name  string
		value any
	}{
		{"Name", name}, {"Description", "Only permit the isolated AI Runtime to reach ScriptBoard loopback proxies"},
		{"ApplicationName", executable}, {"ServiceName", serviceName},
		// The Provider proxy is deliberately tcp4-only. Including ::1 makes the
		// COM API reject the entire rule on hosts where IPv6 is disabled.
		{"Protocol", 6}, {"RemoteAddresses", "127.0.0.1"},
		{"Action", 1}, {"Direction", 2}, {"Enabled", true}, {"Profiles", 0x7fffffff},
	} {
		result, err := oleutil.PutProperty(rule, property.name, property.value)
		if result != nil {
			result.Clear()
		}
		if err != nil {
			return fmt.Errorf("set Windows firewall rule %s: %w", property.name, err)
		}
	}
	result, err := oleutil.CallMethod(policy.rules, "Add", rule)
	if result != nil {
		result.Clear()
	}
	return err
}

func isWindowsFirewallRuleNotFound(err error) bool {
	return isOLECode(err, 0x80070002)
}

func isOLECode(err error, code uint32) bool {
	var oleError *ole.OleError
	return errors.As(err, &oleError) && uint32(oleError.Code()) == code
}
