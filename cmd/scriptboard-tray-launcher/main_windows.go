//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"scriptboard/internal/config"
	"scriptboard/internal/installation"
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
	command := exec.Command(tray, "--config", loaded.ConfigPath)
	command.Dir = filepath.Dir(tray)
	if command.Start() == nil {
		_ = command.Process.Release()
	}
}
