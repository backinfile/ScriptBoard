//go:build !windows

package platformservice

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	serviceName     = "ScriptBoard"
	unitPath        = "/etc/systemd/system/scriptboard.service"
	updaterUnitPath = "/etc/systemd/system/scriptboard-updater@.service"
)

func Exists() (bool, error) {
	_, err := os.Stat(unitPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func Install(executable, configPath, updaterExecutable, stateRoot string) error {
	unit := fmt.Sprintf(`[Unit]
Description=ScriptBoard
After=network.target

[Service]
Type=simple
User=root
ExecStart=%s serve --config %s
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(executable), systemdQuote(configPath))
	updaterUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard update operation %%i
After=network.target

[Service]
Type=oneshot
User=root
ExecStart=%s apply --state-root %s --operation %%i
TimeoutStartSec=0
`, systemdQuote(updaterExecutable), systemdQuote(stateRoot))
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(updaterUnitPath, []byte(updaterUnit), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return systemctl("enable", "scriptboard.service")
}

func SwitchExecutable(_, _ string) error {
	// The systemd service points at Install Root/current. installation.SetCurrent
	// atomically changes that symlink, so the unit never needs to be rewritten.
	return nil
}

func Uninstall() error {
	_ = systemctl("disable", "--now", "scriptboard.service")
	for _, path := range []string{unitPath, updaterUnitPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return systemctl("daemon-reload")
}

func Start() error   { return systemctl("start", "scriptboard.service") }
func Stop() error    { return systemctl("stop", "scriptboard.service") }
func Restart() error { return systemctl("restart", "scriptboard.service") }

func Status() (string, error) {
	output, err := exec.Command("systemctl", "is-active", "scriptboard.service").CombinedOutput()
	state := strings.TrimSpace(string(output))
	if err != nil && state != "inactive" && state != "failed" {
		return string(output), err
	}
	if state == "active" {
		return "SERVICE_NAME: ScriptBoard\nSTATE: RUNNING\n", nil
	}
	return "SERVICE_NAME: ScriptBoard\nSTATE: STOPPED\n", nil
}

func IsRunning() (bool, error) {
	status, err := Status()
	return strings.Contains(status, "STATE: RUNNING"), err
}

func MatchesExecutable(executable, configPath string) (bool, error) {
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		return false, err
	}
	expected := "ExecStart=" + systemdQuote(executable) + " serve --config " + systemdQuote(configPath)
	for _, line := range strings.Split(string(unit), "\n") {
		if strings.TrimSpace(line) == expected {
			return true, nil
		}
	}
	return false, nil
}

func StartUpdater(_, _, operationID string) error {
	if operationID == "" || strings.ContainsAny(operationID, "/\\ \t\r\n") {
		return errors.New("invalid update operation ID")
	}
	return systemctl("start", "--no-block", "scriptboard-updater@"+operationID+".service")
}

func systemctl(arguments ...string) error {
	output, err := exec.Command("systemctl", arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, `%`, `%%`)
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	return `"` + value + `"`
}
