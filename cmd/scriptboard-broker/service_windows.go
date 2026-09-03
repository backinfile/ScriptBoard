//go:build windows

package main

import (
	"os"
	"path/filepath"

	"scriptboard/internal/platform/windowsservice"
)

func runAsWindowsService(arguments []string) (bool, error) {
	return windowsservice.Run(windowsservice.Configuration{Name: "ScriptBoardBroker", Arguments: arguments, Prepare: prepareWindowsBrokerService, Run: runContext})
}

func prepareWindowsBrokerService(arguments []string) {
	logPath := brokerServiceLogPath(arguments)
	if logPath == "" || os.MkdirAll(filepath.Dir(logPath), 0o700) != nil {
		return
	}
	// Keep runtime Broker failures after service startup instead of losing them
	// through the SCM process's unreliable standard-error stream.
	rotateBrokerServiceLog(logPath, 10<<20, 5)
	if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		os.Stdout, os.Stderr = file, file
	}
}
