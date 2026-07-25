package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"scriptboard/internal/app"
)

const (
	fixtureUsername = "admin"
	fixturePassword = "calibration-ledger-2026"
)

func main() {
	root, err := os.MkdirTemp("", "scriptboard-browser-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	if err := seedManagedRoot(managedRoot); err != nil {
		panic(err)
	}

	application, err := app.Open(app.Config{
		ManagedRoot:   managedRoot,
		StateRoot:     stateRoot,
		AdminUsername: fixtureUsername,
		AdminPassword: fixturePassword,
	})
	if err != nil {
		panic(err)
	}
	defer application.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Printf("READY http://%s\n", listener.Addr())

	runContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-runContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func seedManagedRoot(root string) error {
	directories := []string{
		"automation",
		"automation/maintenance",
		"data/exports",
		"documentation",
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		"README.md": "# ScriptBoard fixture\n\nDeterministic files for the Chromium desktop gate.\n",
		"automation/weekly-system-check.ps1": `param([string]$Environment = "production")
$ErrorActionPreference = "Stop"
Write-Output "CALIBRATION / WEEKLY SYSTEM CHECK"
Write-Output "environment=$Environment"
Write-Output "filesystem: healthy"
Write-Output "database: healthy"
Write-Output "scheduler: healthy"
Write-Output "version-protection: ready"
Write-Output "result=passed"
`,
		"automation/maintenance/archive-old-logs.cmd":    "@echo off\r\necho archived=42\r\necho result=passed\r\n",
		"automation/maintenance/check-disk-space.ps1":    "Write-Output \"free-space=stable\"\n",
		"automation/maintenance/rebuild-local-index.ps1": "Write-Output \"index=rebuilt\"\n",
		"data/exports/host-inventory.csv":                "host,platform,state\nfixture,windows,ready\n",
		"documentation/operator-notes.txt":               "Keep scripts small, observable, and reversible.\n",
		"documentation/recovery-checklist.md":            "# Recovery checklist\n\n1. Read the last Run.\n2. Verify the target.\n3. Restore deliberately.\n",
	}
	for path, content := range files {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
