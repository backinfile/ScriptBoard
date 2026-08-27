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
	"golang.org/x/sys/windows/svc/eventlog"
	"scriptboard/internal/secretredaction"
)

type RunFunc func(context.Context, []string) error

type Configuration struct {
	Name      string
	Arguments []string
	Prepare   func([]string)
	Run       RunFunc
}

func Run(configuration Configuration) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	if configuration.Prepare != nil {
		configuration.Prepare(configuration.Arguments)
	}
	return true, svc.Run(configuration.Name, handler{name: configuration.Name, arguments: configuration.Arguments, run: configuration.Run})
}

type handler struct {
	name      string
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
				message := secretredaction.String(err.Error())
				// SCM services do not have a reliable stderr stream; preserve startup
				// failures in the Application log for operators and release gates.
				if logger, openErr := eventlog.Open(service.name); openErr == nil {
					_ = logger.Error(1, message)
					_ = logger.Close()
				}
				fmt.Fprintln(os.Stderr, message)
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
