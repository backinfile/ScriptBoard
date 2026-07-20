//go:build !windows

package platformservice

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/scriptboard.service"

func Install(configPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	unit := fmt.Sprintf("[Unit]\nDescription=ScriptBoard\nAfter=network.target\n\n[Service]\nType=simple\nUser=root\nExecStart=%s serve --config %s\nRestart=on-failure\nNoNewPrivileges=true\n\n[Install]\nWantedBy=multi-user.target\n", systemdEscape(executable), systemdEscape(configPath))
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return systemctl("enable", "scriptboard.service")
}

func Uninstall() error {
	_ = systemctl("disable", "--now", "scriptboard.service")
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}
func Start() error   { return systemctl("start", "scriptboard.service") }
func Stop() error    { return systemctl("stop", "scriptboard.service") }
func Restart() error { return systemctl("restart", "scriptboard.service") }
func Status() (string, error) {
	output, err := exec.Command("systemctl", "status", "--no-pager", "scriptboard.service").CombinedOutput()
	return string(output), err
}
func systemctl(arguments ...string) error {
	output, err := exec.Command("systemctl", arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
func systemdEscape(value string) string { return strings.ReplaceAll(value, " ", `\x20`) }
