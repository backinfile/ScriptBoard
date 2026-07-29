package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	updatepkg "scriptboard/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ScriptBoard updater error: "+err.Error())
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "apply" {
		return errors.New("usage: scriptboard-updater apply --state-root PATH --operation ID")
	}
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "ScriptBoard State Root")
	operation := flags.String("operation", "", "Update operation ID")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *stateRoot == "" || *operation == "" {
		return errors.New("state-root and operation are required")
	}
	return updatepkg.ApplyOperation(context.Background(), *stateRoot, *operation)
}
