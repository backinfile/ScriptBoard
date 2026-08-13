package bootstrap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"scriptboard/internal/assistant/runtimehost"
	"scriptboard/internal/platformservice"
)

func RunAIHost(ctx context.Context, arguments []string) error {
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
	absolute, err := absoluteStateRoot(*stateRoot, "AI Runtime Host")
	if err != nil {
		return err
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

func absoluteStateRoot(value, component string) (string, error) {
	trimmed := strings.TrimSpace(value)
	absolute, err := filepath.Abs(trimmed)
	if err != nil || trimmed == "" || !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s requires an absolute --state-root", component)
	}
	return absolute, nil
}
