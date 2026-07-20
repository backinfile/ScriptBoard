package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"scriptboard/internal/app"
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
	default:
		return fmt.Errorf("未知命令 %q；可用命令：serve、version", arguments[0])
	}
}

func serve(arguments []string) error {
	managedDefault, stateDefault := defaultRoots()
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	managedRoot := flags.String("managed-root", managedDefault, "受管根目录")
	stateRoot := flags.String("state-root", stateDefault, "内部状态目录")
	listenAddress := flags.String("listen", "127.0.0.1:8787", "HTTP 监听地址")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := requireLoopback(*listenAddress); err != nil {
		return err
	}

	application, err := app.Open(app.Config{ManagedRoot: *managedRoot, StateRoot: *stateRoot})
	if err != nil {
		return err
	}
	defer application.Close()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", *listenAddress, err)
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

func defaultRoots() (string, string) {
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		base := filepath.Join(programData, "ScriptBoard")
		return filepath.Join(base, "managed"), filepath.Join(base, "state")
	}
	return "/var/lib/scriptboard/managed", "/var/lib/scriptboard/state"
}
