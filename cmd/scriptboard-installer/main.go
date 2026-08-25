package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"scriptboard/internal/buildinfo"
	"scriptboard/internal/installation"
	"scriptboard/internal/processlaunch"
	"scriptboard/internal/secretredaction"
	updatepkg "scriptboard/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, secretredaction.String("error: "+err.Error()))
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "--version-json" {
		raw, err := buildinfo.JSON()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(raw))
		return err
	}
	extractDestination := ""
	if len(arguments) > 0 && arguments[0] == "--extract-to" {
		if len(arguments) != 2 || !filepath.IsAbs(arguments[1]) || filepath.Clean(arguments[1]) == filepath.VolumeName(arguments[1])+string(filepath.Separator) {
			return errors.New("--extract-to requires one absolute non-root directory")
		}
		extractDestination = filepath.Clean(arguments[1])
	}
	if !buildinfo.Current().ValidRelease() {
		return errors.New("only a formal ScriptBoard release installer can install managed services")
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve installer executable: %w", err)
	}
	unpackedSize, _, err := updatepkg.MeasureArchive(self)
	if err != nil {
		return fmt.Errorf("validate embedded release payload: %w", err)
	}
	if extractDestination != "" {
		if err := extractAndVerify(self, extractDestination, unpackedSize); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "ScriptBoard %s extracted to %s\n", buildinfo.Current().Version, extractDestination)
		return nil
	}
	temporaryRoot, err := os.MkdirTemp("", "scriptboard-setup-*")
	if err != nil {
		return fmt.Errorf("create private installer directory: %w", err)
	}
	defer os.RemoveAll(temporaryRoot)
	payloadRoot := filepath.Join(temporaryRoot, "release")
	if err := extractAndVerify(self, payloadRoot, unpackedSize); err != nil {
		return err
	}
	name := "scriptboard"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	entrypoint := filepath.Join(payloadRoot, name)
	if err := os.Chmod(entrypoint, 0o700); err != nil {
		return fmt.Errorf("prepare release entrypoint: %w", err)
	}
	childArguments := append([]string{"service", "install", "--start"}, arguments...)
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: context.Background(), Executable: entrypoint, Arguments: childArguments,
		Environment: processlaunch.EnvironmentInherit, Directory: payloadRoot,
	})
	if err != nil {
		return err
	}
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install ScriptBoard: %w", err)
	}
	return nil
}

func extractAndVerify(installerPath, destination string, unpackedSize int64) error {
	if err := updatepkg.ExtractArchive(installerPath, destination, unpackedSize); err != nil {
		return fmt.Errorf("extract embedded release payload: %w", err)
	}
	if err := installation.ValidateReleaseSource(destination, buildinfo.Current()); err != nil {
		_ = os.RemoveAll(destination)
		return fmt.Errorf("verify embedded release payload: %w", err)
	}
	if err := preparePayloadExecutables(destination); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	return nil
}

func preparePayloadExecutables(root string) error {
	names := []string{"scriptboard", "scriptboard-broker", "scriptboard-runner", "scriptboard-updater"}
	if runtime.GOOS == "windows" {
		names = []string{"scriptboard.exe", "scriptboard-broker.exe", "scriptboard-runner.exe", "scriptboard-updater.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe"}
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("prepare release executable %s: %w", name, err)
		}
	}
	return nil
}
