package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/config"
	"scriptboard/internal/doctor"
	"scriptboard/internal/platformservice"
)

var version = "development"

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
		fmt.Fprintln(os.Stdout, "ScriptBoard "+version)
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
	default:
		return fmt.Errorf("未知命令 %q；可用命令：serve、service、admin、config、doctor、version", arguments[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `ScriptBoard — 单机可信脚本管理器

用法：
  scriptboard serve [配置选项]
  scriptboard service install|uninstall|start|stop|restart|status
  scriptboard admin reset [配置选项]
  scriptboard config validate [配置选项]
  scriptboard doctor [配置选项]
  scriptboard version

常用配置选项：
  --config PATH              YAML 配置文件
  --managed-root PATH        受管根目录
  --state-root PATH          内部状态目录
  --listen ADDRESS           HTTP 监听地址
  --tls-cert PATH            TLS 证书
  --tls-key PATH             TLS 私钥
  --trusted-proxy IP_OR_CIDR 可信反向代理（可重复）`)
}

func runService(action string, arguments []string) error {
	switch action {
	case "install":
		loaded, err := config.Load(arguments, os.Getenv)
		if err != nil {
			return err
		}
		return platformservice.Install(loaded.ConfigPath)
	case "uninstall":
		return platformservice.Uninstall()
	case "start":
		return platformservice.Start()
	case "stop":
		return platformservice.Stop()
	case "restart":
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
	application, err := app.Open(app.Config{ManagedRoot: loaded.ManagedRoot, StateRoot: loaded.StateRoot, RunTimeoutGrace: loaded.RunTimeoutGrace, GitExecutable: loaded.GitExecutable, ExecutorChains: loaded.ExecutorChains})
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
		ManagedRoot: loaded.ManagedRoot, StateRoot: loaded.StateRoot, GitExecutable: loaded.GitExecutable,
		ConfigPath: loaded.ConfigPath, Listen: loaded.Listen, TLSCert: loaded.TLSCert, TLSKey: loaded.TLSKey,
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
	if err := requireSafeNetwork(loaded.Listen, loaded.TLSCert, loaded.TLSKey, loaded.TrustedProxies); err != nil {
		return err
	}

	application, err := app.Open(app.Config{
		ManagedRoot: loaded.ManagedRoot, StateRoot: loaded.StateRoot,
		RunTimeoutGrace: loaded.RunTimeoutGrace, GitExecutable: loaded.GitExecutable, ExecutorChains: loaded.ExecutorChains, AdminUsername: loaded.AdminUsername, AdminPassword: loaded.AdminPassword, AdminPasswordFile: loaded.AdminPasswordFile, TrustedProxies: loaded.TrustedProxies,
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

	server := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	scheme := "http"
	if loaded.TLSCert != "" {
		scheme = "https"
	}
	fmt.Fprintln(os.Stdout, "ScriptBoard 已启动："+scheme+"://"+listener.Addr().String())

	go func() {
		<-runContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func validateConfig(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if loaded.ManagedRoot == "" || loaded.StateRoot == "" {
		return errors.New("Managed Root 和 State Root 不能为空")
	}
	if err := requireSafeNetwork(loaded.Listen, loaded.TLSCert, loaded.TLSKey, loaded.TrustedProxies); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "配置有效\nManaged Root: %s\nState Root: %s\nListen: %s\n", loaded.ManagedRoot, loaded.StateRoot, loaded.Listen)
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
