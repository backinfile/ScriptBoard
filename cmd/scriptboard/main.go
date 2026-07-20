package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/config"
	"scriptboard/internal/doctor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("请指定命令；可用命令：serve")
	}
	switch arguments[0] {
	case "serve":
		return serve(arguments[1:])
	case "version":
		fmt.Fprintln(os.Stdout, "ScriptBoard development")
		return nil
	case "config":
		if len(arguments) < 2 || arguments[1] != "validate" {
			return errors.New("可用配置命令：config validate")
		}
		return validateConfig(arguments[2:])
	case "doctor":
		return runDoctor(arguments[1:])
	default:
		return fmt.Errorf("未知命令 %q；可用命令：serve、version", arguments[0])
	}
}

func runDoctor(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	report := doctor.Run(doctor.Config{
		ManagedRoot: loaded.ManagedRoot, StateRoot: loaded.StateRoot, GitExecutable: loaded.GitExecutable,
	})
	for _, check := range report.Checks {
		status := "OK"
		if !check.Healthy {
			status = "FAIL"
		}
		fmt.Fprintf(os.Stdout, "[%s] %s: %s\n", status, check.Name, check.Detail)
	}
	if !report.Healthy {
		return errors.New("doctor found unhealthy checks")
	}
	return nil
}

func serve(arguments []string) error {
	loaded, err := config.Load(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if err := requireLoopback(loaded.Listen); err != nil {
		return err
	}

	application, err := app.Open(app.Config{
		ManagedRoot: loaded.ManagedRoot, StateRoot: loaded.StateRoot,
		RunTimeoutGrace: loaded.RunTimeoutGrace, GitExecutable: loaded.GitExecutable,
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
	fmt.Fprintln(os.Stdout, "ScriptBoard 已启动：http://"+listener.Addr().String())

	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-interruptContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	err = server.Serve(listener)
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
	if err := requireLoopback(loaded.Listen); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "配置有效\nManaged Root: %s\nState Root: %s\nListen: %s\n", loaded.ManagedRoot, loaded.StateRoot, loaded.Listen)
	return nil
}

func requireLoopback(address string) error {
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
