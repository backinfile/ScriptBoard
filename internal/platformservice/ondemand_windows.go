//go:build windows

package platformservice

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func EnsureRunnerHostRunning(ctx context.Context) error {
	return ensureWindowsServiceRunning(ctx, runnerServiceName)
}

func ensureWindowsServiceRunning(ctx context.Context, name string) error {
	managerHandle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(managerHandle)
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	serviceHandle, err := windows.OpenService(managerHandle, namePointer, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	service := &mgr.Service{Name: name, Handle: serviceHandle}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		if err := service.Start(); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err = service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Running {
			return nil
		}
		if status.State == svc.Stopped {
			return fmt.Errorf("Windows service %s stopped during demand start", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
