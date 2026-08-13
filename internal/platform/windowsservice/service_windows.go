//go:build windows

// Package windowsservice owns the shared Windows SCM lifecycle used by
// ScriptBoard's managed runtime processes.
package windowsservice

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"scriptboard/internal/secretredaction"
)

type RunFunc func(context.Context, []string) error

func Run(name string, arguments []string, run RunFunc) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	return true, svc.Run(name, handler{arguments: arguments, run: run})
}

type handler struct {
	arguments []string
	run       RunFunc
}

func (service handler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.run(ctx, service.arguments) }()
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintln(os.Stderr, secretredaction.String(err.Error()))
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
