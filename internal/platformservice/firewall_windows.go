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

const legacyAILoopbackFirewallRuleName = "ScriptBoard AI Runtime loopback provider access"

type windowsServiceRestriction interface {
	Restrict(serviceName, executable string, enabled, restrictedSID bool) error
	RemoveRule(name string) error
}

func configureWindowsRunnerFirewall(runnerExecutable, runnerIdentityMode string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	return applyWindowsRunnerFirewall(policy, runnerExecutable, runnerIdentityMode)
}

func applyWindowsRunnerFirewall(policy windowsServiceRestriction, runnerExecutable, runnerIdentityMode string) error {
	if !filepath.IsAbs(runnerExecutable) {
		return errors.New("Windows Runner firewall executable path must be absolute")
	}
	// Remove the obsolete AI loopback exception while applying the current
	// Runner-only service-hardening policy during upgrades.
	if err := policy.RemoveRule(legacyAILoopbackFirewallRuleName); err != nil && !isWindowsFirewallRuleNotFound(err) {
		return fmt.Errorf("remove obsolete AI Runtime loopback rule: %w", err)
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

func removeWindowsRunnerFirewall(runnerExecutable string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	return removeWindowsRunnerFirewallWith(policy, runnerExecutable)
}

func removeWindowsRunnerFirewallWith(policy windowsServiceRestriction, runnerExecutable string) error {
	if err := policy.RemoveRule(legacyAILoopbackFirewallRuleName); err != nil && !isWindowsFirewallRuleNotFound(err) {
		return err
	}
	if runnerExecutable != "" {
		if err := policy.Restrict(runnerServiceName, runnerExecutable, false, true); err != nil {
			return fmt.Errorf("remove Windows Service Hardening restriction for %s: %w", runnerServiceName, err)
		}
	}
	return nil
}

func retireWindowsRunnerFirewall(oldRunner, newRunner string) error {
	policy, closePolicy, err := openWindowsServiceRestriction()
	if err != nil {
		return err
	}
	defer closePolicy()
	if sameWindowsPath(oldRunner, newRunner) {
		return nil
	}
	if err := policy.Restrict(runnerServiceName, oldRunner, false, true); err != nil {
		return fmt.Errorf("retire previous Windows Service Hardening restriction for %s: %w", runnerServiceName, err)
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

func isWindowsFirewallRuleNotFound(err error) bool {
	return isOLECode(err, 0x80070002)
}

func isOLECode(err error, code uint32) bool {
	var oleError *ole.OleError
	return errors.As(err, &oleError) && uint32(oleError.Code()) == code
}
