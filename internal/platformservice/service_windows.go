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

const serviceName = "ScriptBoard"

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

func Install(executable, configPath, _, _ string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	configuration := mgr.Config{
		StartType: mgr.StartAutomatic, DisplayName: "ScriptBoard",
		Description: "ScriptBoard trusted-script management service",
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
	return service.UpdateConfig(configuration)
}

func Uninstall() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(serviceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	return service.Delete()
}

func Start() error {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	defer service.Close()
	return service.Start()
}

func Stop() error {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
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
	return errors.New("Windows service did not stop within 45 seconds")
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
	return true, nil
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
