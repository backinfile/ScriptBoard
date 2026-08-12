package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scriptboard/internal/assistant/runtimehost"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/shutdownsignal"
)

func main() {
	if handled, err := runAsWindowsService(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, secretredaction.String("AI Runtime Host service error: "+err.Error()))
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("AI Runtime Host error: "+err.Error()))
		os.Exit(1)
	}
}

func run(arguments []string) error {
	ctx, stop := shutdownsignal.Context(context.Background())
	defer stop()
	return runContext(ctx, arguments)
}

func runContext(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("scriptboard-ai-host", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "absolute ScriptBoard State Root")
	endpoint := flags.String("endpoint", "", "local IPC endpoint override")
	allowedIdentity := flags.String("allowed-identity", "", "authorized Web service OS identity")
	maximum := flags.Int("maximum", 8, "maximum concurrent Runtime processes")
	developmentCurrentUser := flags.Bool("development-current-user", false, "authorize the current OS user for local development")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected AI Runtime Host arguments: %v", flags.Args())
	}
	absolute, err := filepath.Abs(strings.TrimSpace(*stateRoot))
	if err != nil || strings.TrimSpace(*stateRoot) == "" || !filepath.IsAbs(*stateRoot) {
		return errors.New("AI Runtime Host requires an absolute --state-root")
	}
	if *maximum < 1 || *maximum > 8 {
		return errors.New("AI Runtime Host --maximum must be between 1 and 8")
	}
	if !*developmentCurrentUser {
		if err := platformservice.ValidateAIRuntimeIdentity(); err != nil {
			return fmt.Errorf("refuse to start managed AI Runtime Host with unsafe OS identity: %w", err)
		}
	}
	transport, err := runtimehost.Listen(runtimehost.TransportOptions{
		StateRoot: absolute, Endpoint: *endpoint, AllowedIdentity: *allowedIdentity,
		DevelopmentCurrentUser: *developmentCurrentUser,
	})
	if err != nil {
		return err
	}
	defer transport.Close()
	server, err := runtimehost.NewServer(runtimehost.ServerOptions{
		Listener: transport.Listener, VerifyPeer: transport.VerifyPeer,
		StateRoot: absolute, Maximum: *maximum,
	})
	if err != nil {
		return err
	}
	server.Start()
	<-ctx.Done()
	return server.Close(context.Background())
}
