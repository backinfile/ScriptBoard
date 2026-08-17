package appstatus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestDockerSnapshotAllowsSlowLocalEngineResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte("[]"))
	}))
	defer server.Close()

	api, err := client.New(
		client.WithHost(server.URL),
		client.WithHTTPClient(server.Client()),
		client.WithVersion("1.48"),
	)
	if err != nil {
		t.Fatal(err)
	}
	collector := &dockerCollector{client: api, previous: make(map[string]dockerBlockSample)}
	defer collector.Close()

	containers, _, available, err := collector.Snapshot(context.Background(), 1, time.Now())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !available || len(containers) != 0 {
		t.Fatalf("Snapshot() = available %v, containers %#v", available, containers)
	}
}

func TestDeriveDockerContainerNormalizesWholeHostCPUAndBlockRates(t *testing.T) {
	collectedAt := time.Date(2026, 7, 29, 9, 0, 5, 0, time.UTC)
	summary := containertypes.Summary{
		ID:    "1234567890abcdef",
		Names: []string{"/api-prod"},
		Image: "ghcr.io/example/api:2026.07",
	}
	stats := containertypes.StatsResponse{
		Read: collectedAt,
		CPUStats: containertypes.CPUStats{
			CPUUsage:    containertypes.CPUUsage{TotalUsage: 5_000_000_000},
			SystemUsage: 100_000_000_000,
		},
		PreCPUStats: containertypes.CPUStats{
			CPUUsage:    containertypes.CPUUsage{TotalUsage: 3_000_000_000},
			SystemUsage: 90_000_000_000,
		},
		MemoryStats: containertypes.MemoryStats{
			Usage: 740 << 20, Limit: 2 << 30,
			Stats: map[string]uint64{"inactive_file": 20 << 20},
		},
		PidsStats: containertypes.PidsStats{Current: 18},
		BlkioStats: containertypes.BlkioStats{IoServiceBytesRecursive: []containertypes.BlkioStatEntry{
			{Op: "Read", Value: 300},
			{Op: "Write", Value: 500},
		}},
	}
	container, sample := deriveDockerContainer(summary, stats, dockerBlockSample{
		readBytes: 100, writeBytes: 200, collectedAt: collectedAt.Add(-5 * time.Second),
	}, 4, collectedAt)

	if container.Name != "api-prod" || container.Image != summary.Image {
		t.Fatalf("identity = %#v", container)
	}
	if container.CPUPercent != 20 {
		t.Fatalf("CPU = %.1f%%, want 20%% of the whole host", container.CPUPercent)
	}
	if container.MemoryBytes != 720<<20 || container.MemoryLimitBytes != 2<<30 || container.ProcessCount != 18 {
		t.Fatalf("memory/processes = %#v", container)
	}
	if container.ReadBytesPerSecond != 40 || container.WriteBytesPerSecond != 60 {
		t.Fatalf("block rates = %.1f / %.1f", container.ReadBytesPerSecond, container.WriteBytesPerSecond)
	}
	if sample.readBytes != 300 || sample.writeBytes != 500 || !sample.collectedAt.Equal(collectedAt) {
		t.Fatalf("sample = %#v", sample)
	}
}

func TestDeriveDockerSummaryKeepsStoppedStateComposeAndPublishedPorts(t *testing.T) {
	summary := containertypes.Summary{
		ID: "stopped-id", Names: []string{"/worker"}, Image: "worker:v3",
		State: "exited", Status: "Exited (0) 2 hours ago",
		Labels: map[string]string{
			"com.docker.compose.project": "platform",
			"com.docker.compose.service": "worker",
		},
		Ports: []containertypes.PortSummary{
			{IP: netip.MustParseAddr("127.0.0.1"), PrivatePort: 8080, PublicPort: 18080, Type: "tcp"},
			{PrivatePort: 9090, Type: "tcp"},
		},
		Health: &containertypes.HealthSummary{Status: containertypes.Unhealthy},
	}

	container := deriveDockerSummary(summary)
	if container.State != "exited" || container.Status != summary.Status || container.Health != "unhealthy" {
		t.Fatalf("runtime state = %#v", container)
	}
	if container.ComposeProject != "platform" || container.ComposeService != "worker" {
		t.Fatalf("compose identity = %#v", container)
	}
	if len(container.PublishedPorts) != 1 || container.PublishedPorts[0] != "127.0.0.1:18080 -> 8080/tcp" {
		t.Fatalf("published ports = %#v", container.PublishedPorts)
	}
}

func TestCgroupMatchesOnlyKnownLocalDockerContainers(t *testing.T) {
	content := "0::/system.slice/docker-1234567890abcdef.scope\n"
	if !cgroupMatchesContainer(content, []string{"1234567890abcdef"}) {
		t.Fatal("known Docker cgroup was not recognized")
	}
	if cgroupMatchesContainer(content, []string{"fedcba0987654321"}) {
		t.Fatal("unrelated Docker cgroup was recognized")
	}
}
