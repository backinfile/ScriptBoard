package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/assistant/pirpc"
	"scriptboard/internal/assistant/runtimehost"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/config"
	"scriptboard/internal/doctor"
	"scriptboard/internal/installation"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/privilegebroker"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/runnerhost"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/shutdownsignal"
	updatepkg "scriptboard/internal/update"
)

func main() {
	if handled, err := runAsWindowsService(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, secretredaction.String("Windows 服务错误："+err.Error()))
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("错误："+err.Error()))
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
	case "audit":
		if len(arguments) < 2 || arguments[1] != "verify" {
			return errors.New("可用审计命令：audit verify")
		}
		return verifyAudit(arguments[2:])
	case "emergency":
		if len(arguments) < 2 {
			return errors.New("可用应急命令：emergency pause-external|revoke-key|export-evidence")
		}
		return runEmergency(arguments[1], arguments[2:])
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
			return errors.New("可用更新命令：update status|check|verify-package|repair-current|recover")
		}
		return runUpdate(arguments[1], arguments[2:])
	default:
		return fmt.Errorf("未知命令 %q；可用命令：serve、service、update、emergency、admin、audit、config、doctor、version", arguments[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `ScriptBoard — 单机可信脚本管理器

用法：
  scriptboard serve [配置选项]
  scriptboard service install|uninstall|start|stop|restart|status
  scriptboard update status|check
  scriptboard update verify-package --archive PATH --manifest PATH --signature PATH [--json]
  scriptboard update repair-current --confirm REPAIR-CURRENT [配置选项]
  scriptboard update recover --operation ID --confirm-operation ID [配置选项]
  scriptboard admin reset [配置选项]
  scriptboard audit verify [配置选项] [--json]
  scriptboard emergency pause-external --confirm PAUSE-EXTERNAL [配置选项]
  scriptboard emergency revoke-key --key-id ID --confirm-key-id ID [配置选项]
  scriptboard emergency export-evidence --output ABSOLUTE_PATH [配置选项]
  scriptboard config validate [配置选项]
  scriptboard doctor [配置选项]
  scriptboard version

常用配置选项：
  --config PATH              YAML 配置文件
  --state-root PATH          内部状态目录
  --listen ADDRESS           HTTP 监听地址
  --tls-cert PATH            TLS 证书
  --tls-key PATH             TLS 私钥
  --trusted-proxy IP_OR_CIDR 可信反向代理（可重复）
  --allowed-host HOST        允许的 HTTP Host（可重复）
  --canonical-external-url URL 对外访问的规范 URL`)
}

func verifyAudit(arguments []string) error {
	jsonOutput, arguments := takeBooleanArgument(arguments, "--json")
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	databasePath := filepath.ToSlash(filepath.Join(loaded.StateRoot, "app.db"))
	database, err := sql.Open("sqlite", "file:"+databasePath+"?mode=ro")
	if err != nil {
		return err
	}
	defer database.Close()
	ctx := context.Background()
	audit := auditlog.New(database)
	verification, err := audit.Verify(ctx)
	if err != nil {
		return fmt.Errorf("审计哈希链验证失败: %w", err)
	}
	if err := verifySignedAuditCheckpoint(ctx, loaded.StateRoot, audit); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"valid": true, "events": verification.Count, "tail_sha256": verification.LastHash, "signed_checkpoint": "valid",
		})
	}
	fmt.Fprintf(os.Stdout, "审计哈希链与外部签名 checkpoint 有效：%d 条事件，链尾 %s\n", verification.Count, verification.LastHash)
	return nil
}

func runUpdate(action string, arguments []string) error {
	jsonOutput, arguments := takeBooleanArgument(arguments, "--json")
	switch action {
	case "repair-current":
		confirmation, remaining := takeStringArgument(arguments, "--confirm")
		if confirmation != "REPAIR-CURRENT" {
			return errors.New("update repair-current 需要 --confirm REPAIR-CURRENT")
		}
		loaded, err := config.Load(remaining, os.Getenv)
		if err != nil {
			return err
		}
		info, err := updatepkg.RepairCurrentInstallation(loaded.StateRoot)
		if err != nil {
			return fmt.Errorf("修复当前安装版本: %w", err)
		}
		if database, auditErr := openEmergencyDatabase(loaded.StateRoot); auditErr == nil {
			auditErr = emergencyMutation(context.Background(), database, loaded.StateRoot, func(context.Context, *sql.Tx) (auditlog.Event, error) {
				return localEmergencyEvent("emergency.update.repair-current", info.Version), nil
			})
			_ = database.Close()
			if auditErr != nil {
				fmt.Fprintln(os.Stderr, "警告：当前版本已修复，但无法写入本地审计链："+secretredaction.String(auditErr.Error()))
			}
		} else {
			fmt.Fprintln(os.Stderr, "警告：当前版本已修复，但无法打开本地审计数据库："+secretredaction.String(auditErr.Error()))
		}
		fmt.Fprintf(os.Stdout, "当前已验证版本 %s 的服务指针已修复；服务保持停止状态。\n", info.Version)
		return nil
	case "verify-package":
		archivePath, remaining := takeStringArgument(arguments, "--archive")
		manifestPath, remaining := takeStringArgument(remaining, "--manifest")
		signaturePath, remaining := takeStringArgument(remaining, "--signature")
		if archivePath == "" || manifestPath == "" || signaturePath == "" {
			return errors.New("update verify-package 需要 --archive PATH、--manifest PATH 与 --signature PATH")
		}
		if len(remaining) != 0 {
			return fmt.Errorf("未知 update verify-package 参数: %v", remaining)
		}
		verified, err := updatepkg.VerifyOfflinePackage(archivePath, manifestPath, signaturePath)
		if err != nil {
			return fmt.Errorf("离线更新包验证失败: %w", err)
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(verified)
		}
		fmt.Fprintf(os.Stdout, "离线更新包有效：%s (%s/%s)，签名 Key %s，SHA-256 %s\n",
			verified.Version, verified.OS, verified.Arch, verified.KeyID, verified.ArchiveSHA256)
		return nil
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
		initializer, err := app.Open(app.Config{
			StateRoot: metadata.StateRoot, InstallRoot: metadata.InstallRoot, ConfigPath: metadata.ConfigPath, TLSKey: loaded.TLSKey,
			RunTimeoutGrace: loaded.RunTimeoutGrace, ExecutorChains: loaded.ExecutorChains,
			AdminUsername: loaded.AdminUsername, AdminPasswordFile: loaded.AdminPasswordFile,
		})
		if err != nil {
			return fmt.Errorf("初始化 managed service 状态: %w", err)
		}
		if err := initializer.Close(); err != nil {
			return fmt.Errorf("完成 managed service 状态初始化: %w", err)
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
	interruptContext, stop := shutdownsignal.Context(context.Background())
	defer stop()
	return serveContext(interruptContext, arguments)
}

func serveContext(runContext context.Context, arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if err := requireSafeNetwork(loaded.Listen, loaded.TLSCert, loaded.TLSKey, loaded.TrustedProxies); err != nil {
		return err
	}
	securityEventToken := ""
	if loaded.SecurityEventTokenFile != "" {
		body, readErr := os.ReadFile(loaded.SecurityEventTokenFile)
		if readErr != nil {
			return fmt.Errorf("read security event token file: %w", readErr)
		}
		if len(body) > 4096 || strings.TrimSpace(string(body)) == "" {
			return errors.New("security event token file must contain 1 to 4096 bytes")
		}
		securityEventToken = strings.TrimSpace(string(body))
	}
	installRoot := applicationInstallRoot(loaded.StateRoot)
	var assistantLauncher pirpc.ProcessLauncher
	var runnerLauncher runmanager.ProcessLauncher
	var auditCheckpoint app.AuditCheckpoint
	privilegedBrokerEndpoint := ""
	if installRoot != "" {
		if err := platformservice.ValidateWebRuntimeIdentity(); err != nil {
			return fmt.Errorf("refuse to start managed Web service with unsafe OS identity: %w", err)
		}
		endpoint, err := runtimehost.DefaultEndpoint(loaded.StateRoot)
		if err != nil {
			return fmt.Errorf("resolve isolated Runtime Host endpoint: %w", err)
		}
		assistantLauncher = runtimehost.NewClientLauncher(runtimehost.Dial(endpoint))
		runnerEndpoint, err := runnerhost.DefaultEndpoint(loaded.StateRoot)
		if err != nil {
			return fmt.Errorf("resolve isolated Runner Host endpoint: %w", err)
		}
		runnerLauncher = runnerhost.NewClientLauncher(runnerhost.Dial(runnerEndpoint))
		privilegedBrokerEndpoint, err = privilegebroker.DefaultEndpoint(loaded.StateRoot)
		if err != nil {
			return fmt.Errorf("resolve privileged Broker endpoint: %w", err)
		}
		brokerClient := privilegebroker.NewClient(privilegebroker.ClientOptions{Dial: privilegebroker.Dial(privilegedBrokerEndpoint)})
		auditCheckpoint = privilegebroker.NewRemoteCheckpoint(brokerClient)
	}
	updateShutdown := make(chan struct{}, 1)
	var requestRestart func() error
	if canRestartManagedService(loaded.StateRoot, loaded.ConfigPath) {
		requestRestart = func() error { return platformservice.RequestRestart(time.Second) }
	}

	application, err := app.Open(app.Config{
		StateRoot: loaded.StateRoot, InstallRoot: installRoot, ConfigPath: loaded.ConfigPath, TLSKey: loaded.TLSKey,
		RunTimeoutGrace: loaded.RunTimeoutGrace, ExecutorChains: loaded.ExecutorChains, AdminUsername: loaded.AdminUsername, AdminPasswordFile: loaded.AdminPasswordFile, TrustedProxies: loaded.TrustedProxies,
		AllowedHosts: loaded.AllowedHosts, CanonicalExternalURL: loaded.CanonicalExternalURL,
		SecurityEventEndpoint: loaded.SecurityEventEndpoint, SecurityEventToken: securityEventToken,
		SecurityEventTokenFile: loaded.SecurityEventTokenFile, SecurityEventAllowPrivate: loaded.SecurityEventAllowPrivate,
		UpdateCheck: loaded.UpdateCheck, UpdateInterval: loaded.UpdateInterval,
		RequestShutdown: func() {
			select {
			case updateShutdown <- struct{}{}:
			default:
			}
		},
		RequestRestart:           requestRestart,
		AssistantProcessLauncher: assistantLauncher,
		RunnerProcessLauncher:    runnerLauncher,
		PrivilegedBrokerEndpoint: privilegedBrokerEndpoint,
		AuditCheckpoint:          auditCheckpoint,
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
	if err := requireSafeNetwork(loaded.Listen, loaded.TLSCert, loaded.TLSKey, loaded.TrustedProxies); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "配置有效\nState Root: %s\nListen: %s\n", loaded.StateRoot, loaded.Listen)
	return nil
}

func requireSafeNetwork(address, certificate, key string, _ []string) error {
	if (certificate == "") != (key == "") {
		return errors.New("TLS 证书与私钥必须同时配置")
	}
	if certificate != "" {
		if _, err := tls.LoadX509KeyPair(certificate, key); err != nil {
			return fmt.Errorf("TLS 证书或私钥无效: %w", err)
		}
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("无效监听地址 %q: %w", address, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("明文 HTTP 只能监听回环地址")
	}
	return nil
}
