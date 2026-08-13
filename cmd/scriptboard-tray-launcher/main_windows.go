//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"

	"scriptboard/internal/config"
	"scriptboard/internal/installation"
	"scriptboard/internal/processlaunch"
)

func main() {
	loaded, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		return
	}
	metadata, err := installation.Load(loaded.StateRoot)
	if err != nil {
		return
	}
	tray := filepath.Join(installation.VersionRoot(metadata, metadata.Current), "scriptboard-tray.exe")
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: tray, Arguments: []string{"--config", loaded.ConfigPath},
		Environment: processlaunch.EnvironmentInherit, Directory: filepath.Dir(tray),
	})
	if err != nil {
		return
	}
	if command.Start() == nil {
		_ = command.Process.Release()
	}
}
