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

	"scriptboard/internal/assistant/runtimehost"
	"scriptboard/internal/processlaunch"
	"scriptboard/internal/runnerhost"
)

const (
	serviceName       = "ScriptBoard"
	unitPath          = "/etc/systemd/system/scriptboard.service"
	brokerUnitPath    = "/etc/systemd/system/scriptboard-broker.service"
	aiUnitPath        = "/etc/systemd/system/scriptboard-ai.service"
	aiSocketUnitPath  = "/etc/systemd/system/scriptboard-ai.socket"
	runnerUnitPath    = "/etc/systemd/system/scriptboard-runner.service"
	runnerSocketPath  = "/etc/systemd/system/scriptboard-runner.socket"
	updaterUnitPath   = "/etc/systemd/system/scriptboard-updater@.service"
	webServiceUser    = "scriptboard-web"
	aiServiceUser     = "scriptboard-ai"
	runnerServiceUser = "scriptboard-runner"
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

func ValidateAIRuntimeIdentity() error {
	account, err := user.Lookup(aiServiceUser)
	if err != nil {
		return fmt.Errorf("resolve managed AI Runtime service account: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse managed AI Runtime service UID: %w", err)
	}
	if os.Geteuid() == 0 || os.Geteuid() != uid {
		return fmt.Errorf("effective UID %d is not dedicated AI Runtime service UID %d", os.Geteuid(), uid)
	}
	return nil
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
	brokerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-broker")
	aiExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-ai-host")
	runnerExecutable := filepath.Join(filepath.Dir(executable), "scriptboard-runner")
	aiEndpoint, err := runtimehost.DefaultEndpoint(stateRoot)
	if err != nil {
		return err
	}
	runnerEndpoint, err := runnerhost.DefaultEndpoint(stateRoot)
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=ScriptBoard
After=network.target
Requires=scriptboard-broker.service scriptboard-ai.socket scriptboard-runner.socket
After=scriptboard-broker.service scriptboard-ai.socket scriptboard-runner.socket

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
	brokerUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard privileged operation Broker
After=network.target

[Service]
Type=simple
User=root
ExecStart=%s --config %s --state-root %s --allowed-identity scriptboard-web
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, systemdQuote(brokerExecutable), systemdQuote(configPath), systemdQuote(stateRoot))
	aiUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard isolated AI Runtime Host
Requires=scriptboard-ai.socket
After=network.target scriptboard-ai.socket

[Service]
Type=simple
User=scriptboard-ai
Group=scriptboard-ai
SupplementaryGroups=scriptboard-ai
ExecStart=%s --state-root %s --allowed-identity scriptboard-web
Restart=on-failure
NoNewPrivileges=true
UMask=0007
CapabilityBoundingSet=
AmbientCapabilities=
PrivateTmp=true
PrivateDevices=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=%s
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
TasksMax=64
MemoryMax=1G
MemorySwapMax=0
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
IPAddressDeny=any
IPAddressAllow=localhost
`, systemdQuote(aiExecutable), systemdQuote(stateRoot), systemdQuote(filepath.Join(stateRoot, "assistant")))
	aiSocketUnit := fmt.Sprintf(`[Unit]
Description=ScriptBoard AI Runtime Host activation socket

[Socket]
ListenStream=%s
FileDescriptorName=scriptboard-ai
Service=scriptboard-ai.service
SocketUser=scriptboard-ai
SocketGroup=scriptboard-ai
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=true

[Install]
WantedBy=sockets.target
`, systemdQuote(aiEndpoint))
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
`, systemdQuote(runnerEndpoint))
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
	if err := os.WriteFile(aiUnitPath, []byte(aiUnit), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(aiSocketUnitPath, []byte(aiSocketUnit), 0o644); err != nil {
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
	if err := systemctl("enable", "scriptboard-ai.socket"); err != nil {
		return err
	}
	if err := systemctl("enable", "scriptboard-runner.socket"); err != nil {
		return err
	}
	return systemctl("enable", "scriptboard.service")
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
	if err := prepareLinuxAIServiceIdentity(stateRoot); err != nil {
		return err
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
	command, err = processlaunch.Prepare(processlaunch.Spec{Context: context.Background(), Executable: "/usr/sbin/usermod", Arguments: []string{"--append", "--groups", webServiceUser + "," + aiServiceUser, runnerServiceUser}, Environment: processlaunch.EnvironmentInherit})
	if err != nil {
		return err
	}
	if output, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("grant Linux Runner read access to service config: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func prepareLinuxAIServiceIdentity(stateRoot string) error {
	webAccount, err := user.Lookup(webServiceUser)
	if err != nil {
		return fmt.Errorf("resolve Linux Web service account for AI Runtime ACL: %w", err)
	}
	webUID, err := strconv.Atoi(webAccount.Uid)
	if err != nil {
		return fmt.Errorf("parse Linux Web service UID for AI Runtime ACL: %w", err)
	}
	account, err := user.Lookup(aiServiceUser)
	if _, unknown := err.(user.UnknownUserError); unknown {
		command, commandErr := processlaunch.Prepare(processlaunch.Spec{
			Context: context.Background(), Executable: "/usr/sbin/useradd",
			Arguments:   []string{"--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", aiServiceUser},
			Environment: processlaunch.EnvironmentInherit,
		})
		if commandErr != nil {
			return fmt.Errorf("prepare Linux AI Runtime service account: %w", commandErr)
		}
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return fmt.Errorf("create Linux AI Runtime service account: %w: %s", runErr, strings.TrimSpace(string(output)))
		}
		account, err = user.Lookup(aiServiceUser)
	}
	if err != nil {
		return fmt.Errorf("resolve Linux AI Runtime service account: %w", err)
	}
	group, err := user.LookupGroup(aiServiceUser)
	if err != nil || group.Gid != account.Gid {
		return fmt.Errorf("Linux AI Runtime service account must use its dedicated %s group", aiServiceUser)
	}
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: "/usr/sbin/usermod",
		Arguments:   []string{"--append", "--groups", aiServiceUser, webServiceUser},
		Environment: processlaunch.EnvironmentInherit,
	})
	if err != nil {
		return fmt.Errorf("prepare Linux Web access to Runtime Host IPC: %w", err)
	}
	if output, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("grant Linux Web access to Runtime Host IPC: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse Linux AI Runtime UID: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parse Linux AI Runtime GID: %w", err)
	}
	assistantRoot := filepath.Join(stateRoot, "assistant")
	stateInfo, err := os.Stat(stateRoot)
	if err != nil {
		return err
	}
	// Web remains the State Root owner; the AI group receives traverse only so
	// it can reach its Assistant subtree without reading the database/secrets.
	if err := os.Lchown(stateRoot, webUID, gid); err != nil {
		return err
	}
	if err := os.Chmod(stateRoot, stateInfo.Mode().Perm()&0o700|0o010); err != nil {
		return err
	}
	if err := os.MkdirAll(assistantRoot, 0o770); err != nil {
		return err
	}
	if err := os.Lchown(assistantRoot, webUID, gid); err != nil {
		return err
	}
	if err := os.Chmod(assistantRoot, 0o750); err != nil {
		return err
	}
	runtimeRoot := filepath.Join(assistantRoot, "runtime")
	if _, statErr := os.Lstat(runtimeRoot); statErr == nil {
		if err := filepath.WalkDir(runtimeRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := os.Lchown(path, webUID, gid); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			owner := info.Mode().Perm() & 0o700
			permissions := owner | (owner>>3)&0o050
			return os.Chmod(path, permissions)
		}); err != nil {
			return fmt.Errorf("make Linux AI Runtime payload read-only to Runtime identity: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	for _, name := range []string{"pi-home", "sessions", "workspaces"} {
		privateRoot := filepath.Join(assistantRoot, name)
		if err := os.MkdirAll(privateRoot, 0o700); err != nil {
			return err
		}
		if err := filepath.WalkDir(privateRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := os.Lchown(path, uid, gid); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.Chmod(path, info.Mode().Perm()&0o700)
		}); err != nil {
			return fmt.Errorf("isolate Linux AI Runtime private directory %s: %w", name, err)
		}
	}
	return nil
}

func SwitchExecutable(_, _, _ string) error {
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
			if err := systemctl("disable", "--now", "scriptboard-ai.socket"); err != nil {
				return err
			}
			if err := systemctl("disable", "--now", "scriptboard-runner.socket"); err != nil {
				return err
			}
			_ = systemctl("stop", "scriptboard-ai.service")
			_ = systemctl("stop", "scriptboard-runner.service")
			return systemctl("disable", "--now", "scriptboard-broker.service")
		},
		func() error {
			for _, path := range []string{unitPath, brokerUnitPath, aiUnitPath, aiSocketUnitPath, runnerUnitPath, runnerSocketPath, updaterUnitPath} {
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
	if err := systemctl("start", "scriptboard-ai.socket"); err != nil {
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
	if err := systemctl("stop", "scriptboard-ai.service"); err != nil {
		return err
	}
	if err := systemctl("stop", "scriptboard-runner.service"); err != nil {
		return err
	}
	if err := systemctl("stop", "scriptboard-ai.socket"); err != nil {
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
			aiUnit, err := os.ReadFile(aiUnitPath)
			if err != nil {
				return false, err
			}
			expectedAI := "ExecStart=" + systemdQuote(filepath.Join(filepath.Dir(executable), "scriptboard-ai-host")) + " --state-root " + systemdQuote(stateRoot) + " --allowed-identity " + webServiceUser
			aiText := string(aiUnit)
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
			return strings.Contains(aiText, expectedAI) && strings.Contains(aiText, "User="+aiServiceUser) &&
				strings.Contains(aiText, "IPAddressDeny=any") && strings.Contains(aiText, "IPAddressAllow=localhost") &&
				strings.Contains(aiText, "SystemCallFilter=@system-service") && strings.Contains(aiText, "SystemCallArchitectures=native") &&
				runnerMatches, nil
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
	if mode != RunnerIdentityIsolated {
		return ""
	}
	return `NoNewPrivileges=true
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
TasksMax=64
MemoryMax=2G
MemorySwapMax=0
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
