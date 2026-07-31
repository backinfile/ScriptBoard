//go:build windows

package platformservice

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const trayAutostartName = "ScriptBoardTray"

func InstallTrayAutostart(launcher, configPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	commandLine := windows.ComposeCommandLine([]string{launcher, "--config", configPath})
	if err := key.SetStringValue(trayAutostartName, commandLine); err != nil {
		return fmt.Errorf("configure tray autostart: %w", err)
	}
	return nil
}

func RemoveTrayAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	_ = key.DeleteValue(trayAutostartName)
	return nil
}
