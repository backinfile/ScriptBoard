//go:build windows

package platformservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName       = "ScriptBoard"
	brokerServiceName = "ScriptBoardBroker"
	aiServiceName     = "ScriptBoardAI"
	runnerServiceName = "ScriptBoardRunner"
	webServiceAccount = `NT AUTHORITY\LocalService`
	webServiceSID     = `NT SERVICE\ScriptBoard`
	aiServiceSID      = `NT SERVICE\ScriptBoardAI`
	runnerServiceSID  = `NT SERVICE\ScriptBoardRunner`
)

func Exists() (bool, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return false, err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(serviceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	service.Close()
	return true, nil
}

func ValidateWebRuntimeIdentity() error {
	token := windows.GetCurrentProcessToken()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read managed Web token user: %w", err)
	}
	localService, err := windows.StringToSid("S-1-5-19")
	if err != nil {
		return err
	}
	serviceSID, _, _, err := windows.LookupSID("", webServiceSID)
	if err != nil {
		return fmt.Errorf("resolve managed Web service SID: %w", err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return fmt.Errorf("read managed Web token groups: %w", err)
	}
	serviceSIDEnabled := false
	for _, group := range groups.AllGroups() {
		if group.Sid != nil && group.Sid.Equals(serviceSID) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			serviceSIDEnabled = true
			break
		}
	}
	return validateWindowsWebRuntimeIdentity(tokenUser.User.Sid.Equals(localService), serviceSIDEnabled)
}

func ValidateAIRuntimeIdentity() error {
	token := windows.GetCurrentProcessToken()
	restricted, err := token.IsRestricted()
	if err != nil || !restricted {
		return errors.New("managed AI Runtime token is not restricted")
	}
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read managed AI Runtime token user: %w", err)
	}
	localService, err := windows.StringToSid("S-1-5-19")
	if err != nil {
		return err
	}
	serviceSID, _, _, err := windows.LookupSID("", aiServiceSID)
	if err != nil {
		return fmt.Errorf("resolve managed AI Runtime service SID: %w", err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return fmt.Errorf("read managed AI Runtime token groups: %w", err)
	}
	enabled := false
	for _, group := range groups.AllGroups() {
		if group.Sid != nil && group.Sid.Equals(serviceSID) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			enabled = true
			break
		}
	}
	if !tokenUser.User.Sid.Equals(localService) || !enabled {
		return errors.New("process token is not LocalService with the enabled ScriptBoardAI service SID")
	}
	return nil
}

func ValidateRunnerRuntimeIdentity() error {
	return validateWindowsServiceIdentity(runnerServiceSID, "Runner")
}

func validateWindowsServiceIdentity(serviceIdentity, label string) error {
	token := windows.GetCurrentProcessToken()
	restricted, err := token.IsRestricted()
	if err != nil || !restricted {
		return fmt.Errorf("managed %s token is not restricted", label)
	}
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	localService, err := windows.StringToSid("S-1-5-19")
	if err != nil {
		return err
	}
	serviceSID, _, _, err := windows.LookupSID("", serviceIdentity)
	if err != nil {
		return err
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return err
	}
	enabled := false
	for _, group := range groups.AllGroups() {
		if group.Sid != nil && group.Sid.Equals(serviceSID) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			enabled = true
			break
		}
	}
	if !tokenUser.User.Sid.Equals(localService) || !enabled {
		return fmt.Errorf("process token is not LocalService with the enabled ScriptBoard%s service SID", label)
	}
	return nil
}

func validateWindowsWebRuntimeIdentity(localService, serviceSIDEnabled bool) error {
	if !localService || !serviceSIDEnabled {
		return errors.New("process token is not LocalService with the enabled ScriptBoard service SID")
	}
	return nil
}

func Install(executable, configPath, _ string, stateRoot string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	brokerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-broker.exe")
	aiExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-ai-host.exe")
	runnerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-runner.exe")
	brokerConfiguration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard Privileged Broker",
		Description: "ScriptBoard fixed privileged operation broker", SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	broker, err := manager.CreateService(brokerServiceName, brokerExecutable, brokerConfiguration, "--state-root", stateRoot)
	if errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
		broker, err = manager.OpenService(brokerServiceName)
		if err != nil {
			return err
		}
		current, configErr := broker.Config()
		if configErr != nil {
			broker.Close()
			return configErr
		}
		current.BinaryPathName = windows.ComposeCommandLine([]string{brokerExecutable, "--state-root", stateRoot})
		current.StartType = mgr.StartAutomatic
		current.DisplayName = brokerConfiguration.DisplayName
		current.Description = brokerConfiguration.Description
		current.SidType = brokerConfiguration.SidType
		if err = broker.UpdateConfig(current); err != nil {
			broker.Close()
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("install Windows privileged Broker service: %w", err)
	}
	if err := broker.Close(); err != nil {
		return err
	}
	aiConfiguration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard Isolated AI Runtime Host",
		Description: "ScriptBoard isolated AI Runtime process host", ServiceStartName: webServiceAccount,
		SidType: windows.SERVICE_SID_TYPE_RESTRICTED,
	}
	aiService, err := manager.CreateService(aiServiceName, aiExecutable, aiConfiguration, "--state-root", stateRoot, "--allowed-identity", webServiceSID)
	if errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
		aiService, err = manager.OpenService(aiServiceName)
		if err != nil {
			return err
		}
		current, configErr := aiService.Config()
		if configErr != nil {
			aiService.Close()
			return configErr
		}
		current.BinaryPathName = windows.ComposeCommandLine([]string{aiExecutable, "--state-root", stateRoot, "--allowed-identity", webServiceSID})
		current.StartType = mgr.StartAutomatic
		current.DisplayName = aiConfiguration.DisplayName
		current.Description = aiConfiguration.Description
		current.ServiceStartName = aiConfiguration.ServiceStartName
		current.Password = ""
		current.SidType = aiConfiguration.SidType
		if err = aiService.UpdateConfig(current); err != nil {
			aiService.Close()
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("install Windows AI Runtime Host service: %w", err)
	}
	if err := aiService.Close(); err != nil {
		return err
	}
	runnerConfiguration := mgr.Config{StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard Isolated Runner", Description: "ScriptBoard isolated Run worker", ServiceStartName: webServiceAccount, SidType: windows.SERVICE_SID_TYPE_RESTRICTED}
	runnerService, err := manager.CreateService(runnerServiceName, runnerExecutable, runnerConfiguration, "--config", configPath, "--state-root", stateRoot, "--allowed-identity", webServiceSID)
	if errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
		runnerService, err = manager.OpenService(runnerServiceName)
		if err != nil {
			return err
		}
		current, configErr := runnerService.Config()
		if configErr != nil {
			runnerService.Close()
			return configErr
		}
		current.BinaryPathName = windows.ComposeCommandLine([]string{runnerExecutable, "--config", configPath, "--state-root", stateRoot, "--allowed-identity", webServiceSID})
		current.StartType = mgr.StartAutomatic
		current.DisplayName = runnerConfiguration.DisplayName
		current.Description = runnerConfiguration.Description
		current.ServiceStartName = webServiceAccount
		current.Password = ""
		current.SidType = runnerConfiguration.SidType
		if err = runnerService.UpdateConfig(current); err != nil {
			runnerService.Close()
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("install Windows Runner service: %w", err)
	}
	if err := runnerService.Close(); err != nil {
		return err
	}
	configuration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard",
		Description: "ScriptBoard trusted-script management service", Dependencies: []string{brokerServiceName, aiServiceName, runnerServiceName},
		ServiceStartName: webServiceAccount, SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	service, err := manager.CreateService(serviceName, executable, configuration, "serve", "--config", configPath)
	if errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
		service, err = manager.OpenService(serviceName)
		if err != nil {
			return err
		}
		current, configErr := service.Config()
		if configErr != nil {
			service.Close()
			return configErr
		}
		current.BinaryPathName = windows.ComposeCommandLine([]string{executable, "serve", "--config", configPath})
		current.StartType = mgr.StartAutomatic
		current.DisplayName = "ScriptBoard"
		current.Description = configuration.Description
		current.Dependencies = configuration.Dependencies
		current.ServiceStartName = configuration.ServiceStartName
		current.Password = ""
		current.SidType = configuration.SidType
		if err = service.UpdateConfig(current); err != nil {
			service.Close()
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("install Windows service: %w", err)
	}
	if err := service.Close(); err != nil {
		return err
	}
	versionRoot := filepath.Dir(executable)
	versionsRoot := filepath.Dir(versionRoot)
	if !strings.EqualFold(filepath.Base(versionsRoot), "versions") {
		return errors.New("Windows Web service executable is outside the managed versions directory")
	}
	installRoot := filepath.Dir(versionsRoot)
	if err := grantWindowsWebServiceAccess(installRoot, configPath, stateRoot); err != nil {
		return err
	}
	if err := grantWindowsAIServiceAccess(installRoot, stateRoot); err != nil {
		return err
	}
	return grantWindowsRunnerServiceAccess(installRoot, configPath)
}

func grantWindowsRunnerServiceAccess(installRoot, configPath string) error {
	sid, _, _, err := windows.LookupSID("", runnerServiceSID)
	if err != nil {
		return err
	}
	for _, grant := range []struct {
		path        string
		permissions windows.ACCESS_MASK
		recursive   bool
	}{{installRoot, windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE), true}, {configPath, windows.ACCESS_MASK(windows.FILE_GENERIC_READ), false}} {
		if _, statErr := os.Stat(grant.path); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if err := grantWindowsPathAccess(grant.path, sid, grant.permissions, grant.recursive); err != nil {
			return err
		}
	}
	return nil
}

func grantWindowsAIServiceAccess(installRoot, stateRoot string) error {
	sid, _, _, err := windows.LookupSID("", aiServiceSID)
	if err != nil {
		return fmt.Errorf("resolve Windows AI Runtime service SID: %w", err)
	}
	const fileDeleteChild windows.ACCESS_MASK = 0x00000040
	modify := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE | fileDeleteChild)
	for _, grant := range []struct {
		path        string
		permissions windows.ACCESS_MASK
	}{
		{installRoot, windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)},
		{filepath.Join(stateRoot, "assistant"), windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)},
		{filepath.Join(stateRoot, "assistant", "pi-home"), modify},
		{filepath.Join(stateRoot, "assistant", "sessions"), modify},
		{filepath.Join(stateRoot, "assistant", "workspaces"), modify},
	} {
		if _, statErr := os.Stat(grant.path); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if err := grantWindowsPathAccess(grant.path, sid, grant.permissions, true); err != nil {
			return fmt.Errorf("grant AI Runtime service access to %s: %w", grant.path, err)
		}
	}
	return nil
}

func grantWindowsWebServiceAccess(installRoot, configPath, stateRoot string) error {
	if !filepath.IsAbs(installRoot) || !filepath.IsAbs(configPath) || !filepath.IsAbs(stateRoot) {
		return errors.New("Windows service ACL paths must be absolute")
	}
	sid, _, _, err := windows.LookupSID("", webServiceSID)
	if err != nil {
		return fmt.Errorf("resolve Windows Web service SID: %w", err)
	}
	const fileDeleteChild windows.ACCESS_MASK = 0x00000040
	modify := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE | fileDeleteChild)
	for _, grant := range []struct {
		path        string
		permissions windows.ACCESS_MASK
		recursive   bool
	}{
		{installRoot, windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE), true},
		{configPath, windows.ACCESS_MASK(windows.FILE_GENERIC_READ), false},
		{stateRoot, modify, true},
		{filepath.Join(filepath.Dir(stateRoot), "secrets"), modify, true},
	} {
		if _, statErr := os.Stat(grant.path); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if err := grantWindowsPathAccess(grant.path, sid, grant.permissions, grant.recursive); err != nil {
			return fmt.Errorf("grant Web service access to %s: %w", grant.path, err)
		}
	}
	return nil
}

func grantWindowsPathAccess(path string, sid *windows.SID, permissions windows.ACCESS_MASK, recursive bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	currentACL, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	inheritance := uint32(0)
	if recursive {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, currentACL)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func SwitchExecutable(executable, configPath string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer service.Close()
	configuration, err := service.Config()
	if err != nil {
		return err
	}
	configuration.BinaryPathName = windows.ComposeCommandLine([]string{executable, "serve", "--config", configPath})
	if err := service.UpdateConfig(configuration); err != nil {
		return err
	}
	broker, err := manager.OpenService(brokerServiceName)
	if err != nil {
		return err
	}
	defer broker.Close()
	brokerConfiguration, err := broker.Config()
	if err != nil {
		return err
	}
	arguments, err := windows.DecomposeCommandLine(brokerConfiguration.BinaryPathName)
	if err != nil || len(arguments) != 3 || arguments[1] != "--state-root" {
		return errors.New("Windows privileged Broker service command is invalid")
	}
	brokerConfiguration.BinaryPathName = windows.ComposeCommandLine([]string{filepath.Join(filepath.Dir(executable), "scriptboard-broker.exe"), "--state-root", arguments[2]})
	if err := broker.UpdateConfig(brokerConfiguration); err != nil {
		return err
	}
	aiService, err := manager.OpenService(aiServiceName)
	if err != nil {
		return err
	}
	defer aiService.Close()
	aiConfiguration, err := aiService.Config()
	if err != nil {
		return err
	}
	aiArguments, err := windows.DecomposeCommandLine(aiConfiguration.BinaryPathName)
	if err != nil || len(aiArguments) != 5 || aiArguments[1] != "--state-root" || aiArguments[3] != "--allowed-identity" {
		return errors.New("Windows AI Runtime Host service command is invalid")
	}
	aiConfiguration.BinaryPathName = windows.ComposeCommandLine([]string{filepath.Join(filepath.Dir(executable), "scriptboard-ai-host.exe"), "--state-root", aiArguments[2], "--allowed-identity", aiArguments[4]})
	if err := aiService.UpdateConfig(aiConfiguration); err != nil {
		return err
	}
	runnerService, err := manager.OpenService(runnerServiceName)
	if err != nil {
		return err
	}
	defer runnerService.Close()
	runnerConfiguration, err := runnerService.Config()
	if err != nil {
		return err
	}
	runnerArguments, err := windows.DecomposeCommandLine(runnerConfiguration.BinaryPathName)
	if err != nil || len(runnerArguments) != 7 || runnerArguments[1] != "--config" || runnerArguments[3] != "--state-root" || runnerArguments[5] != "--allowed-identity" {
		return errors.New("Windows Runner service command is invalid")
	}
	runnerConfiguration.BinaryPathName = windows.ComposeCommandLine([]string{filepath.Join(filepath.Dir(executable), "scriptboard-runner.exe"), "--config", runnerArguments[2], "--state-root", runnerArguments[4], "--allowed-identity", runnerArguments[6]})
	return runnerService.UpdateConfig(runnerConfiguration)
}

func Uninstall() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	for _, name := range []string{serviceName, runnerServiceName, aiServiceName, brokerServiceName} {
		service, err := manager.OpenService(name)
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			continue
		}
		if err != nil {
			return err
		}
		deleteErr := service.Delete()
		closeErr := service.Close()
		if deleteErr != nil {
			return deleteErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func Start() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	if err := startWindowsService(manager, brokerServiceName); err != nil {
		return err
	}
	if err := startWindowsService(manager, aiServiceName); err != nil {
		return err
	}
	if err := startWindowsService(manager, runnerServiceName); err != nil {
		return err
	}
	return startWindowsService(manager, serviceName)
}

func Stop() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	if err := stopWindowsService(manager, serviceName); err != nil {
		return err
	}
	if err := stopWindowsService(manager, aiServiceName); err != nil {
		return err
	}
	if err := stopWindowsService(manager, runnerServiceName); err != nil {
		return err
	}
	return stopWindowsService(manager, brokerServiceName)
}

func startWindowsService(manager *mgr.Mgr, name string) error {
	service, err := manager.OpenService(name)
	if err != nil {
		return err
	}
	defer service.Close()
	status, err := service.Query()
	if err == nil && status.State == svc.Running {
		return nil
	}
	return service.Start()
}

func stopWindowsService(manager *mgr.Mgr, name string) error {
	service, err := manager.OpenService(name)
	if err != nil {
		return err
	}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		status, err = service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("Windows service %s did not stop within 45 seconds", name)
}

func Restart() error {
	if err := Stop(); err != nil {
		return err
	}
	return Start()
}

func Status() (string, error) {
	manager, service, err := openService()
	if err != nil {
		return "", err
	}
	defer manager.Disconnect()
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return "", err
	}
	name := "UNKNOWN"
	switch status.State {
	case svc.Stopped:
		name = "STOPPED"
	case svc.StartPending:
		name = "START_PENDING"
	case svc.StopPending:
		name = "STOP_PENDING"
	case svc.Running:
		name = "RUNNING"
	}
	return fmt.Sprintf("SERVICE_NAME: %s\nSTATE: %s\n", serviceName, name), nil
}

func IsRunning() (bool, error) {
	status, err := Status()
	return strings.Contains(status, "STATE: RUNNING"), err
}

func MatchesExecutable(executable, configPath string) (bool, error) {
	manager, service, err := openService()
	if err != nil {
		return false, err
	}
	defer manager.Disconnect()
	defer service.Close()
	configuration, err := service.Config()
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(configuration.ServiceStartName, webServiceAccount) || configuration.SidType == windows.SERVICE_SID_TYPE_NONE {
		return false, nil
	}
	arguments, err := windows.DecomposeCommandLine(configuration.BinaryPathName)
	if err != nil {
		return false, err
	}
	if len(arguments) != 4 || !sameWindowsPath(arguments[0], executable) ||
		arguments[1] != "serve" || arguments[2] != "--config" || !sameWindowsPath(arguments[3], configPath) {
		return false, nil
	}
	broker, err := manager.OpenService(brokerServiceName)
	if err != nil {
		return false, err
	}
	defer broker.Close()
	brokerConfiguration, err := broker.Config()
	if err != nil {
		return false, err
	}
	if brokerConfiguration.SidType == windows.SERVICE_SID_TYPE_NONE {
		return false, nil
	}
	brokerArguments, err := windows.DecomposeCommandLine(brokerConfiguration.BinaryPathName)
	if err != nil || len(brokerArguments) != 3 ||
		!sameWindowsPath(brokerArguments[0], filepath.Join(filepath.Dir(executable), "scriptboard-broker.exe")) ||
		brokerArguments[1] != "--state-root" || !filepath.IsAbs(brokerArguments[2]) {
		return false, err
	}
	aiService, err := manager.OpenService(aiServiceName)
	if err != nil {
		return false, err
	}
	defer aiService.Close()
	aiConfiguration, err := aiService.Config()
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(aiConfiguration.ServiceStartName, webServiceAccount) || aiConfiguration.SidType != windows.SERVICE_SID_TYPE_RESTRICTED {
		return false, nil
	}
	aiArguments, err := windows.DecomposeCommandLine(aiConfiguration.BinaryPathName)
	if err != nil || len(aiArguments) != 5 ||
		!sameWindowsPath(aiArguments[0], filepath.Join(filepath.Dir(executable), "scriptboard-ai-host.exe")) ||
		aiArguments[1] != "--state-root" || !filepath.IsAbs(aiArguments[2]) || aiArguments[3] != "--allowed-identity" || !strings.EqualFold(aiArguments[4], webServiceSID) {
		return false, err
	}
	runnerService, err := manager.OpenService(runnerServiceName)
	if err != nil {
		return false, err
	}
	defer runnerService.Close()
	runnerConfiguration, err := runnerService.Config()
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(runnerConfiguration.ServiceStartName, webServiceAccount) || runnerConfiguration.SidType != windows.SERVICE_SID_TYPE_RESTRICTED {
		return false, nil
	}
	runnerArguments, err := windows.DecomposeCommandLine(runnerConfiguration.BinaryPathName)
	return err == nil && len(runnerArguments) == 7 &&
		sameWindowsPath(runnerArguments[0], filepath.Join(filepath.Dir(executable), "scriptboard-runner.exe")) && runnerArguments[1] == "--config" && sameWindowsPath(runnerArguments[2], configPath) &&
		runnerArguments[3] == "--state-root" && filepath.IsAbs(runnerArguments[4]) && runnerArguments[5] == "--allowed-identity" && strings.EqualFold(runnerArguments[6], webServiceSID), err
}

func sameWindowsPath(first, second string) bool {
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)
	return firstErr == nil && secondErr == nil && strings.EqualFold(filepath.Clean(firstAbsolute), filepath.Clean(secondAbsolute))
}

func openService() (*mgr.Mgr, *mgr.Service, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	service, err := manager.OpenService(serviceName)
	if err != nil {
		manager.Disconnect()
		return nil, nil, err
	}
	return manager, service, nil
}
