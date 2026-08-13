//go:build windows

package main

import "scriptboard/internal/platform/windowsservice"

func runAsWindowsService(arguments []string) (bool, error) {
	return windowsservice.Run("ScriptBoardBroker", arguments, runContext)
}
