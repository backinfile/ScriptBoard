//go:build windows

package platformservice

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName       = "ScriptBoard"
	brokerServiceName = "ScriptBoardBroker"
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

func Install(executable, configPath, _ string, stateRoot string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	brokerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-broker.exe")
	brokerConfiguration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard Privileged Broker",
		Description: "ScriptBoard fixed privileged operation broker",
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
	configuration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard",
		Description: "ScriptBoard trusted-script management service", Dependencies: []string{brokerServiceName},
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
		if err = service.UpdateConfig(current); err != nil {
			service.Close()
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("install Windows service: %w", err)
	}
	return service.Close()
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
	return broker.UpdateConfig(brokerConfiguration)
}

func Uninstall() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	for _, name := range []string{serviceName, brokerServiceName} {
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
	brokerArguments, err := windows.DecomposeCommandLine(brokerConfiguration.BinaryPathName)
	return err == nil && len(brokerArguments) == 3 &&
		sameWindowsPath(brokerArguments[0], filepath.Join(filepath.Dir(executable), "scriptboard-broker.exe")) &&
		brokerArguments[1] == "--state-root" && filepath.IsAbs(brokerArguments[2]), err
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
