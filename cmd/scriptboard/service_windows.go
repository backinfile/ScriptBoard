//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"scriptboard/internal/config"
	"scriptboard/internal/platform/windowsservice"
)

func runAsWindowsService(arguments []string) (bool, error) {
	return windowsservice.Run(windowsservice.Configuration{
		Name: "ScriptBoard", Arguments: arguments, Prepare: prepareWindowsService,
		Run: func(ctx context.Context, arguments []string) error {
			return serveContext(ctx, serviceConfigArguments(arguments))
		},
	})
}

func prepareWindowsService(arguments []string) {
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
