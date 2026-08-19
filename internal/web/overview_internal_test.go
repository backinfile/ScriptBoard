package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/hoststatus"
)

func renderOverviewFixture(t *testing.T, tab string, capabilities map[string]bool) string {
	t.Helper()
	load1, load5, load15 := 1.25, 0.75, 0.5
	readLatency, writeLatency := 2.5, 4.75
	view := overviewResponse{
		Overview: hoststatus.Overview{
			Facts: hoststatus.Facts{Hostname: "fixture-host", Platform: "Fixture OS", Architecture: "amd64", LogicalCores: 8, TotalMemoryBytes: 16 << 30},
			Current: hoststatus.Sample{
				At:          time.Date(2026, time.August, 19, 9, 25, 0, 0, time.UTC),
				CPU:         &hoststatus.CPU{UsedPercent: 62, UserPercent: 38, SystemPercent: 19, IOWaitPercent: 5, IOWaitAvailable: true, Load1: &load1, Load5: &load5, Load15: &load15},
				Memory:      &hoststatus.Memory{TotalBytes: 16 << 30, UsedBytes: 10 << 30, AvailableBytes: 6 << 30, UsedPercent: 62.5, SwapAvailable: true, SwapTotalBytes: 4 << 30, SwapUsedBytes: 1 << 30, SwapUsedPercent: 25, CommittedAvailable: true, CommittedBytes: 12 << 30, CommitLimitBytes: 20 << 30},
				Storage:     &hoststatus.StorageSummary{TotalBytes: 1000 << 30, UsedBytes: 600 << 30, AvailableBytes: 400 << 30, UsedPercent: 60},
				Filesystems: []hoststatus.Filesystem{{ID: "root", Device: "disk0", Mountpoint: "/", Type: "ext4", TotalBytes: 1000 << 30, UsedBytes: 600 << 30, AvailableBytes: 400 << 30, UsedPercent: 60, Online: true}},
				Disk:        &hoststatus.DiskSummary{ReadBytesPerSecond: 10 << 20, WriteBytesPerSecond: 5 << 20, ReadOperationsPerSecond: 120, WriteOperationsPerSecond: 80},
				Disks: []hoststatus.Disk{
					{ID: "disk0", Name: "disk0", ReadBytesPerSecond: 10 << 20, WriteBytesPerSecond: 5 << 20, ReadOperationsPerSecond: 120, WriteOperationsPerSecond: 80, ReadLatencyMS: &readLatency, WriteLatencyMS: &writeLatency, Online: true},
					{ID: "disk1", Name: "disk1", Online: true},
				},
				Network:    &hoststatus.NetworkSummary{ReceivedBytesPerSecond: 2 << 20, SentBytesPerSecond: 1 << 20, ReceivedErrors: 1, SentErrors: 2, ReceivedDrops: 3, SentDrops: 4},
				Interfaces: []hoststatus.NetworkInterface{{ID: "eth0", Name: "eth0", Addresses: []string{"192.0.2.10/24"}, ReceivedBytesPerSecond: 2 << 20, SentBytesPerSecond: 1 << 20, ReceivedErrors: 1, SentErrors: 2, ReceivedDrops: 3, SentDrops: 4, Online: true}},
				Process:    &hoststatus.Process{CPUPercent: 3, ResidentMemoryBytes: 128 << 20, Threads: 12, OpenFiles: 42, OpenFilesAvailable: true},
			},
			Capabilities: capabilities,
			CollectedAt:  time.Date(2026, time.August, 19, 9, 25, 0, 0, time.UTC),
		},
		HostUptime:    48 * time.Hour,
		ServiceUptime: 6 * time.Hour,
	}
	var output bytes.Buffer
	if err := overviewTemplate.Execute(&output, struct {
		overviewResponse
		Range  string
		Tab    string
		Locale webLocale
	}{overviewResponse: view, Range: hoststatus.Range1Hour, Tab: tab, Locale: localeEnglishUS}); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestOverviewSummaryTabKeepsBasicMetricsAndOmitsDetailedLedgers(t *testing.T) {
	page := renderOverviewFixture(t, "summary", map[string]bool{})
	for _, expected := range []string{
		`data-overview-tab="summary"`, `data-metric-card="disk"`,
		`data-chart-series="disk.readBytesPerSecond"`, `data-chart-series="disk.writeBytesPerSecond"`,
		`data-chart-series="network.receivedBytesPerSecond"`, `data-chart-series="network.sentBytesPerSecond"`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("summary page missing %q", expected)
		}
	}
	for _, omitted := range []string{`data-host-detail`, `data-disk-device-table`, `data-network-interface-table`, `data-process-facts`} {
		if strings.Contains(page, omitted) {
			t.Errorf("summary page includes detail-only surface %q", omitted)
		}
	}
}

func TestOverviewDetailTabRendersCompleteAvailableFacts(t *testing.T) {
	capabilities := map[string]bool{
		"cpuIOWait": true, "loadAverage": true, "swapMemory": true, "committedMemory": true,
		"diskLatency": true, "processOpenFiles": true,
	}
	page := renderOverviewFixture(t, "details", capabilities)
	for _, expected := range []string{
		`data-overview-tab="details"`, `data-host-detail`,
		"User", "System", "I/O wait", "Load 1 / 5 / 15", "Swap", "Committed / limit",
		`data-disk-device-table`, "Read IOPS", "Write IOPS", "Read latency", "Write latency",
		`data-disk-latency="unavailable"`,
		`data-network-interface-table`, "Inbound errors", "Outbound errors", "Inbound drops", "Outbound drops",
		`data-process-facts`, "Open files",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("detail page missing %q", expected)
		}
	}
	for _, omitted := range []string{`data-metric-card="cpu"`, "Handles"} {
		if strings.Contains(page, omitted) {
			t.Errorf("detail page includes unavailable/basic-only content %q", omitted)
		}
	}
}

func TestOverviewDetailTabShowsHandlesOnlyWhenCollected(t *testing.T) {
	page := renderOverviewFixture(t, "details", map[string]bool{"processHandles": true})
	if !strings.Contains(page, "Handles") {
		t.Fatal("detail page hides collected process handles")
	}
	if strings.Contains(page, "Open files") {
		t.Fatal("detail page invents open-file availability")
	}
}
