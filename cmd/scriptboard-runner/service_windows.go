//go:build windows

package main

import "scriptboard/internal/platform/windowsservice"

func runAsWindowsService(arguments []string) (bool, error) {
	return windowsservice.Run(windowsservice.Configuration{Name: "ScriptBoardRunner", Arguments: arguments, Run: runContext})
}
