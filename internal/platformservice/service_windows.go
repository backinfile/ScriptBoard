//go:build windows

package platformservice

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const serviceName = "ScriptBoard"

func Install(configPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	commandLine := fmt.Sprintf(`"%s" serve --config "%s"`, executable, configPath)
	if output, err := exec.Command("sc.exe", "create", serviceName, "binPath=", commandLine, "start=", "auto", "obj=", "LocalSystem", "DisplayName=", "ScriptBoard").CombinedOutput(); err != nil {
		return fmt.Errorf("安装 Windows 服务: %w: %s", err, strings.TrimSpace(string(output)))
	}
	_, _ = exec.Command("sc.exe", "description", serviceName, "ScriptBoard trusted-script management service").CombinedOutput()
	return nil
}

func Uninstall() error { return runSC("delete", serviceName) }
func Start() error     { return runSC("start", serviceName) }
func Stop() error      { return runSC("stop", serviceName) }
func Restart() error {
	_ = Stop()
	return Start()
}
func Status() (string, error) {
	output, err := exec.Command("sc.exe", "query", serviceName).CombinedOutput()
	return string(output), err
}

func runSC(arguments ...string) error {
	output, err := exec.Command("sc.exe", arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc.exe %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
