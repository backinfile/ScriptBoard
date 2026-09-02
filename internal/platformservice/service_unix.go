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
	"scriptboard/internal/runnerhost"
)

const (
	serviceName         = "ScriptBoard"
	unitPath            = "/etc/systemd/system/scriptboard.service"
	brokerUnitPath      = "/etc/systemd/system/scriptboard-broker.service"
	retiredAIUnitPath   = "/etc/systemd/system/scriptboard-ai.service"
	retiredAISocketPath = "/etc/systemd/system/scriptboard-ai.socket"
	runnerUnitPath      = "/etc/systemd/system/scriptboard-runner.service"
	runnerSocketPath    = "/etc/systemd/system/scriptboard-runner.socket"
	updaterUnitPath     = "/etc/systemd/system/scriptboard-updater@.service"
	webServiceUser      = "scriptboard-web"
	runnerServiceUser   = "scriptboard-runner"
)

const (
	RunnerIdentityPrivileged = "privileged"
	RunnerIdentityIsolated   = "isolated"
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

func ValidateWebRuntimeIdentity() error {
	account, err := user.Lookup(webServiceUser)
	if err != nil {
		return fmt.Errorf("resolve managed Web service account: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse managed Web service UID: %w", err)
	}
	return validateLinuxWebRuntimeIdentity(os.Geteuid(), uid)
}

func ValidateRunnerRuntimeIdentity(mode string) error {
	if mode == "" || mode == RunnerIdentityPrivileged {
		if os.Geteuid() != 0 {
			return fmt.Errorf("effective UID %d is not root for privileged Runner mode", os.Geteuid())
		}
		return nil
	}
	if mode != RunnerIdentityIsolated {
		return fmt.Errorf("unknown Runner identity mode %q", mode)
	}
	account, err := user.Lookup(runnerServiceUser)
	if err != nil {
		return fmt.Errorf("resolve managed Runner service account: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse managed Runner service UID: %w", err)
	}
	if os.Geteuid() == 0 || os.Geteuid() != uid {
		return fmt.Errorf("effective UID %d is not dedicated Runner service UID %d", os.Geteuid(), uid)
	}
	return nil
}

func validateLinuxWebRuntimeIdentity(effectiveUID, expectedUID int) error {
	if effectiveUID == 0 || effectiveUID != expectedUID {
		return fmt.Errorf("effective UID %d is not dedicated Web service UID %d", effectiveUID, expectedUID)
	}
	return nil
}

func Install(executable, configPath, updaterExecutable, stateRoot, runnerIdentityMode string, webReadPaths ...string) error {
	if err := prepareLinuxWebServiceIdentity(configPath, stateRoot, webReadPaths...); err != nil {
		return err
	}
	// Upgrades from v2.3.0 must stop and remove the socket-activated AI host before installing the three-component layout.
	if err := retireLinuxAIUnits(); err != nil {
		return err
	}
	brokerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-broker")
	runnerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-runner")
	runnerEndpoint, err := runnerhost.DefaultEndpoint(stateRoot)
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=ScriptBoard
After=network.target
Requires=scriptboard-broker.service scriptboard-runner.socket
After=scriptboard-broker.service scriptboard-runner.socket

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
MemoryDenyWriteExecute=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(executable), systemdQuote(configPath))
	brokerUnit := linuxBrokerServiceUnit(brokerExecutable, configPath, stateRoot)
	runnerUser, runnerGroup := linuxRunnerServiceAccount(runnerIdentityMode)
	runnerPolicy := linuxRunnerServicePolicy(runnerIdentityMode)
	runnerUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard Run Worker
Requires=scriptboard-runner.socket
After=network.target scriptboard-runner.socket

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s --config %s --state-root %s --allowed-identity scriptboard-web
Restart=on-failure
%s`, runnerUser, runnerGroup, systemdQuote(runnerExecutable), systemdQuote(configPath), systemdQuote(stateRoot), runnerPolicy)
	runnerSocketUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard Run Worker activation socket

[Socket]
ListenStream=%s
FileDescriptorName=scriptboard-runner
Service=scriptboard-runner.service
SocketUser=scriptboard-runner
SocketGroup=scriptboard-runner
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=true

[Install]
WantedBy=sockets.target
	`, systemdSocketAddress(runnerEndpoint))
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
	if err := os.WriteFile(runnerUnitPath, []byte(runnerUnit), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(runnerSocketPath, []byte(runnerSocketUnit), 0o644); err != nil {
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
	if err := systemctl("enable", "scriptboard-runner.socket"); err != nil {
		return err
	}
	return systemctl("enable", "scriptboard.service")
}

func linuxBrokerServiceUnit(executable, configPath, stateRoot string) string {
	// Host Files is Broker-owned, so its mount namespace must retain the host view of /root and /home.
	return fmt.Sprintf(`[Unit]
Description=ScriptBoard privileged operation Broker
After=network.target

[Service]
Type=simple
User=root
ExecStart=%s --config %s --state-root %s --allowed-identity scriptboard-web
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=false
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(executable), systemdQuote(configPath), systemdQuote(stateRoot))
}

func prepareLinuxWebServiceIdentity(configPath, stateRoot string, webReadPaths ...string) error {
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
	if err := prepareLinuxStateParent(stateRoot); err != nil {
		return err
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
	if err := prepareLinuxRunnerServiceIdentity(); err != nil {
		return err
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
	for _, path := range webReadPaths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("Linux Web startup file path must be absolute: %s", path)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect Linux Web startup file %s: %w", path, statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Linux Web startup path must be a regular file without links: %s", path)
		}
		if err := os.Chown(path, uid, -1); err != nil {
			return fmt.Errorf("assign Linux Web startup file owner for %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protect Linux Web startup file %s: %w", path, err)
		}
	}
	return nil
}

// systemd socket address directives parse their value as a socket address,
// not as an ExecStart command line. Quotation marks are therefore data and
// make an otherwise valid AF_UNIX path fail unit validation.
func systemdSocketAddress(endpoint string) string { return endpoint }

func prepareLinuxStateParent(stateRoot string) error {
	parent := filepath.Dir(filepath.Clean(stateRoot))
	if parent == string(filepath.Separator) {
		return nil
	}
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect Linux State Root parent: %w", err)
	}
	if !info.IsDir() {
		return errors.New("Linux State Root parent is not a directory")
	}
	mode := info.Mode() | 0o111
	if err := os.Chmod(parent, mode); err != nil {
		return fmt.Errorf("allow managed service identities to traverse Linux State Root parent: %w", err)
	}
	return nil
}

func prepareLinuxRunnerServiceIdentity() error {
	account, err := user.Lookup(runnerServiceUser)
	if _, unknown := err.(user.UnknownUserError); unknown {
		command, commandErr := processlaunch.Prepare(processlaunch.Spec{Context: context.Background(), Executable: "/usr/sbin/useradd", Arguments: []string{"--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", runnerServiceUser}, Environment: processlaunch.EnvironmentInherit})
		if commandErr != nil {
			return commandErr
		}
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return fmt.Errorf("create Linux Runner service account: %w: %s", runErr, strings.TrimSpace(string(output)))
		}
		account, err = user.Lookup(runnerServiceUser)
	}
	if err != nil {
		return fmt.Errorf("resolve Linux Runner service account: %w", err)
	}
	group, err := user.LookupGroup(runnerServiceUser)
	if err != nil || group.Gid != account.Gid {
		return errors.New("Linux Runner service account must use its dedicated group")
	}
	command, err := processlaunch.Prepare(processlaunch.Spec{Context: context.Background(), Executable: "/usr/sbin/usermod", Arguments: []string{"--append", "--groups", runnerServiceUser, webServiceUser}, Environment: processlaunch.EnvironmentInherit})
	if err != nil {
		return err
	}
	if output, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("grant Linux Web access to Runner IPC: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	command, err = processlaunch.Prepare(processlaunch.Spec{Context: context.Background(), Executable: "/usr/sbin/usermod", Arguments: []string{"--append", "--groups", webServiceUser, runnerServiceUser}, Environment: processlaunch.EnvironmentInherit})
	if err != nil {
		return err
	}
	if output, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("grant Linux Runner read access to service config: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func SwitchExecutable(_, _, _ string) error {
	// The systemd service points at Install Root/current. installation.SetCurrent
	// atomically changes that symlink, so the unit never needs to be rewritten.
	return retireLinuxAIUnits()
}

func Uninstall() error {
	if err := retireLinuxAIUnits(); err != nil {
		return err
	}
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
			if err := systemctl("disable", "--now", "scriptboard-runner.socket"); err != nil {
				return err
			}
			_ = systemctl("stop", "scriptboard-runner.service")
			return systemctl("disable", "--now", "scriptboard-broker.service")
		},
		func() error {
			for _, path := range []string{unitPath, brokerUnitPath, runnerUnitPath, runnerSocketPath, updaterUnitPath} {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		},
		func() error { return systemctl("daemon-reload") },
	)
}

type retiredLinuxUnit struct {
	name   string
	path   string
	action []string
}

func retireLinuxAIUnits() error {
	if err := removeRetiredAIUnitDependencies(unitPath); err != nil {
		return err
	}
	units := []retiredLinuxUnit{
		{name: "scriptboard-ai.socket", path: retiredAISocketPath, action: []string{"disable", "--now"}},
		{name: "scriptboard-ai.service", path: retiredAIUnitPath, action: []string{"stop"}},
	}
	if err := retireLinuxAIUnitsWith(units, systemctl); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func removeRetiredAIUnitDependencies(path string) error {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Linux Web service unit before retiring AI Runtime: %w", err)
	}
	updated := strings.ReplaceAll(string(body), " scriptboard-ai.socket", "")
	updated = strings.ReplaceAll(updated, "scriptboard-ai.socket ", "")
	if updated == string(body) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return fmt.Errorf("remove retired AI Runtime dependency from Linux Web service: %w", err)
	}
	return nil
}

func retireLinuxAIUnitsWith(units []retiredLinuxUnit, control func(...string) error) error {
	for _, unit := range units {
		if _, err := os.Stat(unit.path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect retired Linux AI Runtime unit %s: %w", unit.name, err)
		}
		if err := control(append(unit.action, unit.name)...); err != nil {
			return fmt.Errorf("stop retired Linux AI Runtime unit %s: %w", unit.name, err)
		}
		if err := os.Remove(unit.path); err != nil {
			return fmt.Errorf("remove retired Linux AI Runtime unit %s: %w", unit.name, err)
		}
	}
	return nil
}

func Start() error {
	if err := systemctl("start", "scriptboard-broker.service"); err != nil {
		return err
	}
	if err := systemctl("start", "scriptboard-runner.socket"); err != nil {
		return err
	}
	return systemctl("start", "scriptboard.service")
}
func Stop() error {
	if err := systemctl("stop", "scriptboard.service"); err != nil {
		return err
	}
	if err := systemctl("stop", "scriptboard-runner.service"); err != nil {
		return err
	}
	if err := systemctl("stop", "scriptboard-runner.socket"); err != nil {
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

func MatchesExecutable(executable, configPath, stateRoot, runnerIdentityMode string) (bool, error) {
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
	expectedBroker := "ExecStart=" + systemdQuote(filepath.Join(filepath.Dir(executable), "scriptboard-broker")) + " --config " + systemdQuote(configPath) + " --state-root " + systemdQuote(stateRoot) + " --allowed-identity " + webServiceUser
	for _, line := range strings.Split(string(brokerUnit), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == expectedBroker {
			runnerUnit, err := os.ReadFile(runnerUnitPath)
			if err != nil {
				return false, err
			}
			runnerText := string(runnerUnit)
			expectedRunner := "ExecStart=" + systemdQuote(filepath.Join(filepath.Dir(executable), "scriptboard-runner")) + " --config " + systemdQuote(configPath) + " --state-root " + systemdQuote(stateRoot) + " --allowed-identity " + webServiceUser
			runnerUser, _ := linuxRunnerServiceAccount(runnerIdentityMode)
			runnerMatches := strings.Contains(runnerText, expectedRunner) && strings.Contains(runnerText, "User="+runnerUser)
			if runnerIdentityMode == RunnerIdentityIsolated {
				runnerMatches = runnerMatches && strings.Contains(runnerText, "IPAddressDeny=any") &&
					strings.Contains(runnerText, "RestrictAddressFamilies=AF_UNIX") &&
					strings.Contains(runnerText, "SystemCallFilter=@system-service") &&
					strings.Contains(runnerText, "SystemCallArchitectures=native")
			}
			return runnerMatches, nil
		}
	}
	return false, nil
}

func linuxRunnerServiceAccount(mode string) (string, string) {
	if mode == RunnerIdentityIsolated {
		return runnerServiceUser, runnerServiceUser
	}
	return "root", "root"
}

func linuxRunnerServicePolicy(mode string) string {
	// Trusted privileged Runs still share one bounded Runner cgroup so a script
	// cannot consume all host memory or create an unbounded process tree.
	resourcePolicy := `TasksMax=64
MemoryMax=2G
MemorySwapMax=0
`
	if mode != RunnerIdentityIsolated {
		return resourcePolicy
	}
	return resourcePolicy + `NoNewPrivileges=true
UMask=0077
CapabilityBoundingSet=
AmbientCapabilities=
PrivateTmp=true
PrivateDevices=true
ProtectHome=true
ProtectClock=true
ProtectHostname=true
ProtectKernelLogs=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictNamespaces=true
RestrictRealtime=true
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
RestrictAddressFamilies=AF_UNIX
IPAddressDeny=any
`
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
