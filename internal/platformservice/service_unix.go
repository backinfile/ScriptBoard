//go:build !windows

package platformservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"scriptboard/internal/processlaunch"
)

const (
	serviceName     = "ScriptBoard"
	unitPath        = "/etc/systemd/system/scriptboard.service"
	brokerUnitPath  = "/etc/systemd/system/scriptboard-broker.service"
	updaterUnitPath = "/etc/systemd/system/scriptboard-updater@.service"
	webServiceUser  = "scriptboard-web"
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
	if err := prepareLinuxWebServiceIdentity(configPath, stateRoot); err != nil {
		return err
	}
	brokerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-broker")
	unit := fmt.Sprintf(`[Unit]
Description=ScriptBoard
After=network.target
Requires=scriptboard-broker.service
After=scriptboard-broker.service

[Service]
Type=simple
User=scriptboard-web
Group=scriptboard-web
ExecStart=%s serve --config %s
Restart=on-failure
NoNewPrivileges=true
UMask=0077
CapabilityBoundingSet=
AmbientCapabilities=
PrivateTmp=true
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(executable), systemdQuote(configPath))
	brokerUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard privileged operation Broker
After=network.target

[Service]
Type=simple
User=root
ExecStart=%s --state-root %s --allowed-identity scriptboard-web
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(brokerExecutable), systemdQuote(stateRoot))
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
	if err := os.WriteFile(brokerUnitPath, []byte(brokerUnit), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(updaterUnitPath, []byte(updaterUnit), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", "scriptboard-broker.service"); err != nil {
		return err
	}
	return systemctl("enable", "scriptboard.service")
}

func prepareLinuxWebServiceIdentity(configPath, stateRoot string) error {
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) == string(filepath.Separator) {
		return errors.New("Linux service identity paths must be absolute and State Root cannot be filesystem root")
	}
	account, err := user.Lookup(webServiceUser)
	if _, unknown := err.(user.UnknownUserError); unknown {
		command, commandErr := processlaunch.Prepare(processlaunch.Spec{
			Context: context.Background(), Executable: "/usr/sbin/useradd",
			Arguments:   []string{"--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", webServiceUser},
			Environment: processlaunch.EnvironmentInherit,
		})
		if commandErr != nil {
			return fmt.Errorf("prepare Linux Web service account: %w", commandErr)
		}
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return fmt.Errorf("create Linux Web service account: %w: %s", runErr, strings.TrimSpace(string(output)))
		}
		account, err = user.Lookup(webServiceUser)
	}
	if err != nil {
		return fmt.Errorf("resolve Linux Web service account: %w", err)
	}
	group, err := user.LookupGroup(webServiceUser)
	if err != nil || group.Gid != account.Gid {
		return fmt.Errorf("Linux Web service account must use its dedicated %s group", webServiceUser)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse Linux Web service UID: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parse Linux Web service GID: %w", err)
	}
	for _, root := range []string{stateRoot, filepath.Join(filepath.Dir(stateRoot), "secrets")} {
		if _, statErr := os.Lstat(root); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			return os.Lchown(path, uid, gid)
		}); err != nil {
			return fmt.Errorf("assign %s to Linux Web service account: %w", root, err)
		}
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect Linux service config: %w", err)
	}
	if err := os.Chown(configPath, -1, gid); err != nil {
		return fmt.Errorf("assign Linux service config group: %w", err)
	}
	if err := os.Chmod(configPath, 0o640); err != nil {
		return fmt.Errorf("protect Linux service config: %w", err)
	}
	return nil
}

func SwitchExecutable(_, _ string) error {
	// The systemd service points at Install Root/current. installation.SetCurrent
	// atomically changes that symlink, so the unit never needs to be rewritten.
	return nil
}

func Uninstall() error {
	exists, err := Exists()
	if err != nil {
		return err
	}
	return uninstallService(
		func() error {
			if !exists {
				return nil
			}
			if err := systemctl("disable", "--now", "scriptboard.service"); err != nil {
				return err
			}
			return systemctl("disable", "--now", "scriptboard-broker.service")
		},
		func() error {
			for _, path := range []string{unitPath, brokerUnitPath, updaterUnitPath} {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		},
		func() error { return systemctl("daemon-reload") },
	)
}

func Start() error {
	if err := systemctl("start", "scriptboard-broker.service"); err != nil {
		return err
	}
	return systemctl("start", "scriptboard.service")
}
func Stop() error {
	if err := systemctl("stop", "scriptboard.service"); err != nil {
		return err
	}
	return systemctl("stop", "scriptboard-broker.service")
}
func Restart() error {
	if err := Stop(); err != nil {
		return err
	}
	return Start()
}

func Status() (string, error) {
	command, commandErr := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: "systemctl", Arguments: []string{"is-active", "scriptboard.service"},
		Environment: processlaunch.EnvironmentInherit,
	})
	if commandErr != nil {
		return "", commandErr
	}
	output, err := command.CombinedOutput()
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
	webMatches := false
	webUserMatches := false
	webGroupMatches := false
	webCapabilitiesCleared := false
	for _, line := range strings.Split(string(unit), "\n") {
		switch strings.TrimSpace(line) {
		case expected:
			webMatches = true
		case "User=" + webServiceUser:
			webUserMatches = true
		case "Group=" + webServiceUser:
			webGroupMatches = true
		case "CapabilityBoundingSet=":
			webCapabilitiesCleared = true
		}
	}
	if !webMatches || !webUserMatches || !webGroupMatches || !webCapabilitiesCleared {
		return false, nil
	}
	brokerUnit, err := os.ReadFile(brokerUnitPath)
	if err != nil {
		return false, err
	}
	expectedBrokerPrefix := "ExecStart=" + systemdQuote(filepath.Join(filepath.Dir(executable), "scriptboard-broker")) + " --state-root "
	for _, line := range strings.Split(string(brokerUnit), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, expectedBrokerPrefix) && strings.HasSuffix(trimmed, " --allowed-identity "+webServiceUser) {
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
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: "systemctl", Arguments: arguments,
		Environment: processlaunch.EnvironmentInherit,
	})
	if err != nil {
		return err
	}
	output, err := command.CombinedOutput()
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
