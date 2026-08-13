package main

import (
	"context"
	"fmt"
	"os"

	"scriptboard/internal/bootstrap"
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
	return bootstrap.RunAIHost(ctx, arguments)
}
