package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"scriptboard/internal/config"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/runnerhost"
	"scriptboard/internal/secretredaction"
)

func main() {
	if handled, err := runAsWindowsService(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, secretredaction.String("Runner Host service error: "+err.Error()))
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("Runner Host error: "+err.Error()))
		os.Exit(1)
	}
}

func run(arguments []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runContext(ctx, arguments)
}

func runContext(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("scriptboard-runner", flag.ContinueOnError)
	configPath := flags.String("config", "", "absolute ScriptBoard configuration path")
	stateRoot := flags.String("state-root", "", "absolute ScriptBoard State Root")
	endpoint := flags.String("endpoint", "", "local IPC endpoint override")
	allowedIdentity := flags.String("allowed-identity", "", "authorized Web service OS identity")
	maximum := flags.Int("maximum", 16, "maximum concurrent Runs")
	developmentCurrentUser := flags.Bool("development-current-user", false, "authorize the current OS user for local development")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected Runner Host arguments: %v", flags.Args())
	}
	absolute, err := filepath.Abs(strings.TrimSpace(*stateRoot))
	if err != nil || strings.TrimSpace(*stateRoot) == "" || !filepath.IsAbs(*stateRoot) {
		return errors.New("Runner Host requires an absolute --state-root")
	}
	if *maximum < 1 || *maximum > 32 {
		return errors.New("Runner Host --maximum must be between 1 and 32")
	}
	loaded, err := config.Load(runnerConfigArguments(*configPath, absolute), os.Getenv)
	if err != nil {
		return err
	}
	if !*developmentCurrentUser {
		if err := platformservice.ValidateRunnerRuntimeIdentity(); err != nil {
			return fmt.Errorf("refuse to start managed Runner Host with unsafe OS identity: %w", err)
		}
	}
	transport, err := runnerhost.Listen(runnerhost.TransportOptions{StateRoot: absolute, Endpoint: *endpoint, AllowedIdentity: *allowedIdentity, DevelopmentCurrentUser: *developmentCurrentUser})
	if err != nil {
		return err
	}
	defer transport.Close()
	server, err := runnerhost.NewServer(runnerhost.ServerOptions{Listener: transport.Listener, VerifyPeer: transport.VerifyPeer, ExecutorChains: loaded.ExecutorChains, Maximum: *maximum})
	if err != nil {
		return err
	}
	server.Start()
	<-ctx.Done()
	return server.Close(context.Background())
}

func runnerConfigArguments(configPath, stateRoot string) []string {
	arguments := []string{"--state-root", stateRoot}
	if path := strings.TrimSpace(configPath); path != "" {
		arguments = append([]string{"--config", path}, arguments...)
	}
	return arguments
}
