package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/config"
	"scriptboard/internal/doctor"
	"scriptboard/internal/installation"
	"scriptboard/internal/platformservice"
	updatepkg "scriptboard/internal/update"
)

func main() {
	if handled, err := runAsWindowsService(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Windows 服务错误："+err.Error())
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage()
		return nil
	}
	if arguments[0] == "help" || arguments[0] == "-h" || arguments[0] == "--help" || (len(arguments) > 1 && (arguments[1] == "-h" || arguments[1] == "--help")) {
		printUsage()
		return nil
	}
	switch arguments[0] {
	case "serve":
		return serve(arguments[1:])
	case "version":
		if len(arguments) > 1 && arguments[1] == "--json" {
			encoded, err := buildinfo.JSON()
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, string(encoded))
			return nil
		}
		if len(arguments) > 1 {
			return fmt.Errorf("未知 version 参数: %v", arguments[1:])
		}
		fmt.Fprintln(os.Stdout, "ScriptBoard "+buildinfo.Current().Version)
		return nil
	case "config":
		if len(arguments) < 2 || arguments[1] != "validate" {
			return errors.New("可用配置命令：config validate")
		}
		return validateConfig(arguments[2:])
	case "doctor":
		return runDoctor(arguments[1:])
	case "admin":
		if len(arguments) < 2 || arguments[1] != "reset" {
			return errors.New("可用管理员命令：admin reset")
		}
		return resetAdmin(arguments[2:])
	case "service":
		if len(arguments) < 2 {
			return errors.New("可用服务命令：service install|uninstall|start|stop|restart|status")
		}
		return runService(arguments[1], arguments[2:])
	case "update":
		if len(arguments) < 2 {
			return errors.New("可用更新命令：update status|check|recover")
		}
		return runUpdate(arguments[1], arguments[2:])
	default:
		return fmt.Errorf("未知命令 %q；可用命令：serve、service、update、admin、config、doctor、version", arguments[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `ScriptBoard — 单机可信脚本管理器

用法：
  scriptboard serve [配置选项]
  scriptboard service install|uninstall|start|stop|restart|status
  scriptboard update status|check|recover
  scriptboard admin reset [配置选项]
  scriptboard config validate [配置选项]
  scriptboard doctor [配置选项]
  scriptboard version

常用配置选项：
  --config PATH              YAML 配置文件
  --state-root PATH          内部状态目录
  --listen ADDRESS           HTTP 监听地址
  --tls-cert PATH            TLS 证书
  --tls-key PATH             TLS 私钥
  --trusted-proxy IP_OR_CIDR 可信反向代理（可重复）`)
}

func runUpdate(action string, arguments []string) error {
	jsonOutput, arguments := takeBooleanArgument(arguments, "--json")
	switch action {
	case "status", "check":
		loaded, err := config.Load(arguments, os.Getenv)
		if err != nil {
			return err
		}
		manager := updatepkg.NewManager(updatepkg.ManagerConfig{
			StateRoot: loaded.StateRoot, CheckEnabled: loaded.UpdateCheck, CheckInterval: loaded.UpdateInterval,
		})
		snapshot := manager.Snapshot()
		if action == "check" {
			checkContext, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			snapshot, err = manager.Check(checkContext, true)
			if err != nil {
				return err
			}
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snapshot)
		}
		fmt.Fprintf(os.Stdout, "当前版本: %s\n安装方式: %s\n", snapshot.Build.Version, snapshot.InstallMode)
		if snapshot.Latest != nil {
			fmt.Fprintf(os.Stdout, "最新版本: %s\n", snapshot.Latest.Version)
		}
		if snapshot.Operation != nil {
			fmt.Fprintf(os.Stdout, "更新操作: %s (%s)\n", snapshot.Operation.ID, snapshot.Operation.Phase)
		}
		if snapshot.LastError != "" {
			fmt.Fprintf(os.Stdout, "最近错误: %s\n", snapshot.LastError)
		}
		return nil
	case "recover":
		operationID, remaining := takeStringArgument(arguments, "--operation")
		confirmation, remaining := takeStringArgument(remaining, "--confirm-operation")
		if operationID == "" || confirmation != operationID {
			return errors.New("recover 需要匹配的 --operation ID 与 --confirm-operation ID")
		}
		loaded, err := config.Load(remaining, os.Getenv)
		if err != nil {
			return err
		}
		running, err := platformservice.IsRunning()
		if err == nil && running {
			return errors.New("执行 update recover 前必须先停止 ScriptBoard 服务")
		}
		return updatepkg.RecoverOperation(context.Background(), loaded.StateRoot, operationID)
	default:
		return fmt.Errorf("未知更新命令 %q", action)
	}
}

func takeBooleanArgument(arguments []string, name string) (bool, []string) {
	result := make([]string, 0, len(arguments))
	found := false
	for _, argument := range arguments {
		if argument == name {
			found = true
			continue
		}
		result = append(result, argument)
	}
	return found, result
}

func takeStringArgument(arguments []string, name string) (string, []string) {
	result := make([]string, 0, len(arguments))
	value := ""
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == name && index+1 < len(arguments) {
			value = arguments[index+1]
			index++
			continue
		}
		result = append(result, arguments[index])
	}
	return value, result
}

func runService(action string, arguments []string) error {
	switch action {
	case "install":
		loaded, err := config.Load(arguments, os.Getenv)
		if err != nil {
			return err
		}
		exists, err := platformservice.Exists()
		if err != nil {
			return err
		}
		if exists {
			metadata, loadErr := installation.Load(loaded.StateRoot)
			if loadErr != nil || installation.ValidateVersion(metadata, metadata.Current, buildinfo.Current()) != nil {
				return errors.New("已存在的 ScriptBoard 服务不是受支持的新版 managed service；请先手工卸载旧服务再全新安装")
			}
			if metadata.Current != buildinfo.Current().Version {
				return errors.New("服务已经由新版安装流程管理；请通过应用更新功能升级")
			}
			if err := platformservice.Install(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath, installation.ServiceUpdaterExecutable(metadata), metadata.StateRoot); err != nil {
				return err
			}
			return platformservice.InstallTrayAutostart(filepath.Join(metadata.InstallRoot, "scriptboard-tray-launcher.exe"), metadata.ConfigPath)
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		metadata, err := installation.Prepare(installation.PrepareOptions{
			SourceRoot: filepath.Dir(executable), InstallRoot: installation.DefaultRoot(),
			StateRoot: loaded.StateRoot, ConfigPath: loaded.ConfigPath, Build: buildinfo.Current(),
		})
		if err != nil {
			return err
		}
		if err := platformservice.Install(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath, installation.ServiceUpdaterExecutable(metadata), metadata.StateRoot); err != nil {
			return err
		}
		return platformservice.InstallTrayAutostart(filepath.Join(metadata.InstallRoot, "scriptboard-tray-launcher.exe"), metadata.ConfigPath)
	case "uninstall":
		if err := platformservice.Uninstall(); err != nil {
			return err
		}
		return platformservice.RemoveTrayAutostart()
	case "start":
		return platformservice.Start()
	case "stop":
		return platformservice.Stop()
	case "restart":
		delayValue, remaining := takeStringArgument(arguments, "--delay")
		if len(remaining) > 0 {
			return fmt.Errorf("未知 service restart 参数: %v", remaining)
		}
		if delayValue != "" {
			delay, err := time.ParseDuration(delayValue)
			if err != nil || delay < 0 || delay > 30*time.Second {
				return errors.New("service restart --delay 必须在 0s 到 30s 之间")
			}
			time.Sleep(delay)
		}
		return platformservice.Restart()
	case "status":
		status, err := platformservice.Status()
		fmt.Fprint(os.Stdout, status)
		return err
	default:
		return fmt.Errorf("未知服务命令 %q", action)
	}
}

func resetAdmin(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	application, err := app.Open(app.Config{
		StateRoot: loaded.StateRoot, InstallRoot: applicationInstallRoot(loaded.StateRoot), ConfigPath: loaded.ConfigPath, TLSKey: loaded.TLSKey,
		AdminPasswordFile: loaded.AdminPasswordFile, RunTimeoutGrace: loaded.RunTimeoutGrace, ExecutorChains: loaded.ExecutorChains,
	})
	if err != nil {
		return err
	}
	defer application.Close()
	if _, err := application.ResetAdminCredentials("admin"); err != nil {
		return fmt.Errorf("重置管理员凭据: %w", err)
	}
	fmt.Fprintln(os.Stdout, "管理员已重置；一次性密码位于 "+filepath.Join(loaded.StateRoot, "secrets", "initial-admin-password"))
	return nil
}

func runDoctor(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	report := doctor.Run(doctor.Config{
		StateRoot: loaded.StateRoot, ConfigPath: loaded.ConfigPath,
		Listen: loaded.Listen, TLSCert: loaded.TLSCert, TLSKey: loaded.TLSKey,
	})
	for _, check := range report.Checks {
		status := "OK"
		if !check.Healthy {
			status = "FAIL"
		}
		fmt.Fprintf(os.Stdout, "[%s] %s: %s\n", status, check.Name, check.Detail)
	}
	if !report.Healthy {
		return errors.New("doctor 发现不健康检查项")
	}
	return nil
}

func serve(arguments []string) error {
	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return serveContext(interruptContext, arguments)
}

func serveContext(runContext context.Context, arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if err := validateNetworkConfiguration(loaded.Listen, loaded.TLSCert, loaded.TLSKey); err != nil {
		return err
	}
	updateShutdown := make(chan struct{}, 1)
	var requestRestart func() error
	if canRestartManagedService(loaded.StateRoot, loaded.ConfigPath) {
		requestRestart = func() error { return platformservice.RequestRestart(time.Second) }
	}

	application, err := app.Open(app.Config{
		StateRoot: loaded.StateRoot, InstallRoot: applicationInstallRoot(loaded.StateRoot), ConfigPath: loaded.ConfigPath, TLSKey: loaded.TLSKey,
		RunTimeoutGrace: loaded.RunTimeoutGrace, ExecutorChains: loaded.ExecutorChains, AdminUsername: loaded.AdminUsername, AdminPassword: loaded.AdminPassword, AdminPasswordFile: loaded.AdminPasswordFile, TrustedProxies: loaded.TrustedProxies,
		UpdateCheck: loaded.UpdateCheck, UpdateInterval: loaded.UpdateInterval,
		RequestShutdown: func() {
			select {
			case updateShutdown <- struct{}{}:
			default:
			}
		},
		RequestRestart: requestRestart,
	})
	if err != nil {
		return err
	}
	defer application.Close()

	listener, err := net.Listen("tcp", loaded.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", loaded.Listen, err)
	}
	defer listener.Close()
	if _, err := updatepkg.WriteRuntimeMarker(loaded.StateRoot, application.ValidationOperationID()); err != nil {
		return fmt.Errorf("写入运行时标记: %w", err)
	}
	defer updatepkg.RemoveRuntimeMarker(loaded.StateRoot)

	server := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	scheme := "http"
	if loaded.TLSCert != "" {
		scheme = "https"
	}
	fmt.Fprintln(os.Stdout, "ScriptBoard 已启动："+scheme+"://"+listener.Addr().String())

	go func() {
		select {
		case <-runContext.Done():
		case <-updateShutdown:
		}
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(runContext), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	if loaded.TLSCert != "" {
		err = server.ServeTLS(listener, loaded.TLSCert, loaded.TLSKey)
	} else {
		err = server.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务失败: %w", err)
	}
	return nil
}

func applicationInstallRoot(stateRoot string) string {
	metadata, err := installation.Detect(stateRoot)
	if err != nil {
		return ""
	}
	return metadata.InstallRoot
}

func canRestartManagedService(stateRoot, configPath string) bool {
	metadata, err := installation.Detect(stateRoot)
	if err != nil {
		return false
	}
	matches, err := platformservice.MatchesExecutable(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath)
	if err != nil || !matches {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	currentExecutable, currentErr := os.Stat(executable)
	serviceExecutable, serviceErr := os.Stat(installation.ServiceEntryExecutable(metadata))
	currentConfig, currentConfigErr := os.Stat(configPath)
	serviceConfig, serviceConfigErr := os.Stat(metadata.ConfigPath)
	return currentErr == nil && serviceErr == nil && os.SameFile(currentExecutable, serviceExecutable) &&
		currentConfigErr == nil && serviceConfigErr == nil && os.SameFile(currentConfig, serviceConfig)
}

func validateConfig(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if loaded.StateRoot == "" {
		return errors.New("State Root 不能为空")
	}
	if err := validateNetworkConfiguration(loaded.Listen, loaded.TLSCert, loaded.TLSKey); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "配置有效\nState Root: %s\nListen: %s\n", loaded.StateRoot, loaded.Listen)
	return nil
}

func validateNetworkConfiguration(address, certificate, key string) error {
	if (certificate == "") != (key == "") {
		return errors.New("TLS 证书与私钥必须同时配置")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("无效监听地址 %q: %w", address, err)
	}
	if certificate != "" {
		if _, err := tls.LoadX509KeyPair(certificate, key); err != nil {
			return fmt.Errorf("TLS 证书或私钥无效: %w", err)
		}
	}
	return nil
}
