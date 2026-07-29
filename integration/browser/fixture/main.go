package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"scriptboard/internal/app"
	"scriptboard/internal/appstatus"
)

const (
	fixtureUsername = "admin"
	fixturePassword = "calibration-ledger-2026"
)

type applicationProbe struct {
	mu    sync.Mutex
	index int
}

func (p *applicationProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	collectedAt := time.Date(2026, 7, 29, 8, 30, 0, 0, time.UTC).Add(time.Duration(p.index) * 5 * time.Second)
	step := uint64(p.index)
	p.index++
	return appstatus.RawSnapshot{
		CollectedAt:      collectedAt,
		LogicalCores:     4,
		TotalMemoryBytes: 16 << 30,
		DockerAvailable:  true,
		Processes: []appstatus.RawProcess{
			{
				PID: 201, CreatedAt: collectedAt.Add(-2 * time.Hour), Name: "Host Agent",
				ExecutablePath: `C:\Program Files\ScriptBoard Fixture\host-agent.exe`,
				CPUSeconds:     10 + float64(p.index), ResidentMemoryBytes: 480 << 20, Threads: 9,
				ReadBytes: 1_000_000 + step*2_000_000, WriteBytes: 2_000_000 + step*1_000_000,
			},
			{
				PID: 301, CreatedAt: collectedAt.Add(-time.Hour), Name: "Worker",
				ExecutablePath: `C:\Tools\worker.exe`,
				CPUSeconds:     5 + float64(p.index)/2, ResidentMemoryBytes: 220 << 20, Threads: 4,
				ReadBytes: 500_000 + step*500_000, WriteBytes: 700_000 + step*250_000,
			},
			{
				PID: 302, CreatedAt: collectedAt.Add(-45 * time.Minute), Name: "Worker",
				ExecutablePath: `c:\tools\WORKER.exe`,
				CPUSeconds:     4 + float64(p.index)/2, ResidentMemoryBytes: 180 << 20, Threads: 3,
				ReadBytes: 400_000 + step*400_000, WriteBytes: 600_000 + step*200_000,
			},
		},
		Containers: []appstatus.RawContainer{
			{
				Name: "api-prod", Image: "ghcr.io/example/api:2026.07", CPUPercent: 32.5,
				MemoryBytes: 720 << 20, MemoryLimitBytes: 2 << 30,
				ReadBytesPerSecond: 4 << 20, WriteBytesPerSecond: 2 << 20, ProcessCount: 18,
			},
			{
				Name: "cache-local", Image: "redis:7.4-alpine", CPUPercent: 8.25,
				MemoryBytes: 192 << 20, MemoryLimitBytes: 1 << 30,
				ReadBytesPerSecond: 512 << 10, WriteBytesPerSecond: 256 << 10, ProcessCount: 6,
			},
		},
	}
}

func main() {
	root, err := os.MkdirTemp("", "scriptboard-browser-managed-root-location-with-a-long-path-")
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
		ManagedRoot:      managedRoot,
		StateRoot:        stateRoot,
		AdminUsername:    fixtureUsername,
		AdminPassword:    fixturePassword,
		ApplicationProbe: &applicationProbe{},
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
		"documentation/recovery-checklist.md": `# Recovery checklist

1. Read the last Run.
2. Verify the target.
3. Restore deliberately.

[Return to the fixture guide](../README.md).

` + "```powershell" + `
param([string]$Target)
if ($Target) { Write-Output "ready" }
` + "```" + `

![Remote fixture diagram](https://example.invalid/scriptboard-fixture.png)

<script>alert("fixture")</script>
`,
	}
	for path, content := range files {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
