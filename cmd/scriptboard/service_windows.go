//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"

	"scriptboard/internal/config"
)

func runAsWindowsService(arguments []string) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	loaded, loadErr := config.Load(serviceConfigArguments(arguments), os.Getenv)
	if loadErr == nil {
		logRoot := filepath.Join(loaded.StateRoot, "logs")
		if os.MkdirAll(logRoot, 0o700) == nil {
			logPath := filepath.Join(logRoot, "service.log")
			rotateServiceLog(logPath, 10<<20, 5)
			if file, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); openErr == nil {
				os.Stdout, os.Stderr = file, file
			}
		}
	}
	return true, svc.Run("ScriptBoard", serviceHandler{arguments: arguments})
}

func rotateServiceLog(path string, maxBytes int64, generations int) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxBytes || generations < 1 {
		return
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, generations))
	for index := generations - 1; index >= 1; index-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, index), fmt.Sprintf("%s.%d", path, index+1))
	}
	_ = os.Rename(path, path+".1")
}

func serviceConfigArguments(arguments []string) []string {
	if len(arguments) > 0 && arguments[0] == "serve" {
		return arguments[1:]
	}
	return arguments
}

type serviceHandler struct{ arguments []string }

func (handler serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	runContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveContext(runContext, serviceConfigArguments(handler.arguments)) }()
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case err := <-done:
					if err != nil {
						return true, 1
					}
				case <-time.After(35 * time.Second):
					return true, 2
				}
				return false, 0
			}
		}
	}
}
