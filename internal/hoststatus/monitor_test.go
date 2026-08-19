package hoststatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type sequenceProbe struct {
	mu      sync.Mutex
	samples []RawSample
	next    int
}

func (p *sequenceProbe) Facts(context.Context) (Facts, error) {
	return Facts{Hostname: "test-host", LogicalCores: 4, TotalMemoryBytes: 1000}, nil
}

func (p *sequenceProbe) Sample(context.Context) RawSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.next
	if index >= len(p.samples) {
		index = len(p.samples) - 1
	} else {
		p.next++
	}
	return p.samples[index]
}

func openMonitorDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE host_metric_minutes (
		bucket_at INTEGER PRIMARY KEY,
		sample_count INTEGER NOT NULL,
		average_json TEXT NOT NULL,
		maximum_json TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMonitorDerivesRatesFromConsecutiveSamples(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	probe := &sequenceProbe{samples: []RawSample{
		{
			At:       base,
			CPU:      &CPUCounters{TotalSeconds: 100, IdleSeconds: 60, UserSeconds: 20, SystemSeconds: 10, IOWaitSeconds: 2, IOWaitAvailable: true},
			Disks:    map[string]DiskCounters{"disk0": {ReadBytes: 100, WriteBytes: 200, Reads: 10, Writes: 20, ReadTimeMS: 100, WriteTimeMS: 200}},
			Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1000, SentBytes: 500, ReceivedErrors: 1, SentErrors: 2, ReceivedDrops: 3, SentDrops: 4}},
		},
		{
			At:       base.Add(5 * time.Second),
			CPU:      &CPUCounters{TotalSeconds: 110, IdleSeconds: 65, UserSeconds: 23, SystemSeconds: 11, IOWaitSeconds: 3, IOWaitAvailable: true},
			Load:     &[3]float64{1.25, 0.75, 0.5},
			Disks:    map[string]DiskCounters{"disk0": {ReadBytes: 600, WriteBytes: 1200, Reads: 20, Writes: 30, ReadTimeMS: 150, WriteTimeMS: 280}},
			Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1500, SentBytes: 1000, ReceivedErrors: 5, SentErrors: 6, ReceivedDrops: 7, SentDrops: 8}},
		},
	}}
	monitor, err := New(openMonitorDB(t), probe, Options{Interval: time.Hour, Retention: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)

	monitor.Collect(context.Background())
	first, err := monitor.Overview(context.Background(), Range15Minutes)
	if err != nil {
		t.Fatal(err)
	}
	if first.Current.CPU != nil || first.Current.Disk != nil || first.Current.Network != nil {
		t.Fatalf("first cumulative sample must not invent rates: %#v", first.Current)
	}

	monitor.Collect(context.Background())
	overview, err := monitor.Overview(context.Background(), Range15Minutes)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Current.CPU == nil || overview.Current.CPU.UsedPercent != 50 {
		t.Fatalf("CPU = %#v, want 50%%", overview.Current.CPU)
	}
	if overview.Current.CPU.UserPercent != 30 || overview.Current.CPU.SystemPercent != 10 || overview.Current.CPU.IOWaitPercent != 10 || !overview.Current.CPU.IOWaitAvailable {
		t.Fatalf("CPU composition = %#v", overview.Current.CPU)
	}
	if overview.Current.CPU.Load1 == nil || *overview.Current.CPU.Load1 != 1.25 || overview.Current.CPU.Load5 == nil || *overview.Current.CPU.Load5 != 0.75 || overview.Current.CPU.Load15 == nil || *overview.Current.CPU.Load15 != 0.5 {
		t.Fatalf("CPU load = %#v", overview.Current.CPU)
	}
	if overview.Current.Disk == nil || overview.Current.Disk.ReadBytesPerSecond != 100 || overview.Current.Disk.WriteBytesPerSecond != 200 {
		t.Fatalf("disk = %#v", overview.Current.Disk)
	}
	if len(overview.Current.Disks) != 1 || overview.Current.Disks[0].ReadOperationsPerSecond != 2 || overview.Current.Disks[0].WriteOperationsPerSecond != 2 || overview.Current.Disks[0].ReadLatencyMS == nil || *overview.Current.Disks[0].ReadLatencyMS != 5 || overview.Current.Disks[0].WriteLatencyMS == nil || *overview.Current.Disks[0].WriteLatencyMS != 8 {
		t.Fatalf("disk device = %#v", overview.Current.Disks)
	}
	if overview.Current.Network == nil || overview.Current.Network.ReceivedBytesPerSecond != 100 || overview.Current.Network.SentBytesPerSecond != 100 {
		t.Fatalf("network = %#v", overview.Current.Network)
	}
	if len(overview.Current.Interfaces) != 1 || overview.Current.Interfaces[0].ReceivedErrors != 5 || overview.Current.Interfaces[0].SentErrors != 6 || overview.Current.Interfaces[0].ReceivedDrops != 7 || overview.Current.Interfaces[0].SentDrops != 8 {
		t.Fatalf("network interface = %#v", overview.Current.Interfaces)
	}
}

func TestSampleMetricsIncludesDetailedValuesOnlyWhenAvailable(t *testing.T) {
	readLatency, writeLatency := 3.5, 7.25
	load1, load5, load15 := 1.5, 1.25, 1.0
	values := sampleMetrics(Sample{
		CPU:    &CPU{UsedPercent: 60, UserPercent: 35, SystemPercent: 20, IOWaitPercent: 5, IOWaitAvailable: true, Load1: &load1, Load5: &load5, Load15: &load15},
		Memory: &Memory{UsedPercent: 70, UsedBytes: 700, SwapAvailable: true, SwapUsedBytes: 120, SwapUsedPercent: 12, CommittedAvailable: true, CommittedBytes: 800, CommitLimitBytes: 1200},
		Disk:   &DiskSummary{ReadBytesPerSecond: 100, WriteBytesPerSecond: 200, ReadOperationsPerSecond: 3, WriteOperationsPerSecond: 4},
		Disks:  []Disk{{ID: "disk0", ReadBytesPerSecond: 100, WriteBytesPerSecond: 200, ReadOperationsPerSecond: 3, WriteOperationsPerSecond: 4, ReadLatencyMS: &readLatency, WriteLatencyMS: &writeLatency}},
	})
	for key, want := range map[string]float64{
		"cpu.userPercent": 35, "cpu.systemPercent": 20, "cpu.ioWaitPercent": 5,
		"cpu.load1": 1.5, "cpu.load5": 1.25, "cpu.load15": 1,
		"memory.swapUsedBytes": 120, "memory.swapUsedPercent": 12,
		"memory.committedBytes": 800, "memory.commitLimitBytes": 1200,
		"disk.readOperationsPerSecond": 3, "disk.writeOperationsPerSecond": 4,
	} {
		if got := values.Values[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	for key, want := range map[string]float64{"readOperationsPerSecond": 3, "writeOperationsPerSecond": 4, "readLatencyMS": 3.5, "writeLatencyMS": 7.25} {
		if got := values.Disks["disk0"][key]; got != want {
			t.Errorf("disk %s = %v, want %v", key, got, want)
		}
	}

	withoutOptional := sampleMetrics(Sample{CPU: &CPU{}, Memory: &Memory{}, Disks: []Disk{{ID: "idle"}}})
	for _, key := range []string{"cpu.ioWaitPercent", "cpu.load1", "memory.swapUsedBytes", "memory.committedBytes"} {
		if _, found := withoutOptional.Values[key]; found {
			t.Errorf("unavailable metric %s was persisted", key)
		}
	}
	if _, found := withoutOptional.Disks["idle"]["readLatencyMS"]; found {
		t.Fatal("unavailable disk latency was persisted")
	}
}

func TestCapabilitiesFollowCollectedAvailabilityInsteadOfPlatformAssumptions(t *testing.T) {
	load := floatPointer(1)
	sample := Sample{
		CPU:     &CPU{IOWaitAvailable: true, Load1: load, Load5: load, Load15: load},
		Memory:  &Memory{SwapAvailable: true, CommittedAvailable: true},
		Process: &Process{OpenFilesAvailable: true},
		Disks:   []Disk{{ReadLatencyMS: floatPointer(1)}},
	}
	got := capabilities(sample)
	for _, key := range []string{"loadAverage", "cpuIOWait", "swapMemory", "committedMemory", "diskLatency", "processOpenFiles"} {
		if !got[key] {
			t.Errorf("capability %s = false", key)
		}
	}
	if got["processHandles"] {
		t.Fatal("handles capability was inferred without collected data")
	}
}

func TestMonitorPersistsMinuteAverageAndPeak(t *testing.T) {
	base := time.Unix(1_700_000_040, 0).UTC()
	now := base.Add(70 * time.Second)
	probe := &sequenceProbe{samples: []RawSample{
		{At: base, Memory: &Memory{TotalBytes: 1000, UsedBytes: 100, AvailableBytes: 900, UsedPercent: 10}},
		{At: base.Add(5 * time.Second), Memory: &Memory{TotalBytes: 1000, UsedBytes: 900, AvailableBytes: 100, UsedPercent: 90}},
		{At: base.Add(65 * time.Second), Memory: &Memory{TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 500, UsedPercent: 50}},
	}}
	monitor, err := New(openMonitorDB(t), probe, Options{Interval: time.Hour, Retention: 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)
	monitor.Collect(context.Background())
	monitor.Collect(context.Background())
	monitor.Collect(context.Background())

	overview, err := monitor.Overview(context.Background(), Range1Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Series) != 1 {
		t.Fatalf("series count = %d, want 1", len(overview.Series))
	}
	point := overview.Series[0]
	if got := point.Average.Values["memory.usedPercent"]; got != 50 {
		t.Fatalf("average = %v", got)
	}
	if got := point.Maximum.Values["memory.usedPercent"]; got != 90 {
		t.Fatalf("maximum = %v", got)
	}
}

func TestMonitorPersistsAndSerializesDuplexDiskAndNetworkHistory(t *testing.T) {
	base := time.Unix(1_700_000_040, 0).UTC()
	now := base.Add(70 * time.Second)
	probe := &sequenceProbe{samples: []RawSample{
		{At: base, Disks: map[string]DiskCounters{"disk0": {ReadBytes: 100, WriteBytes: 200}}, Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 300, SentBytes: 400}}},
		{At: base.Add(5 * time.Second), Disks: map[string]DiskCounters{"disk0": {ReadBytes: 600, WriteBytes: 1200}}, Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1800, SentBytes: 2400}}},
		{At: base.Add(65 * time.Second), Disks: map[string]DiskCounters{"disk0": {ReadBytes: 700, WriteBytes: 1300}}, Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1900, SentBytes: 2500}}},
	}}
	monitor, err := New(openMonitorDB(t), probe, Options{Interval: time.Hour, Retention: 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)
	monitor.Collect(context.Background())
	monitor.Collect(context.Background())
	monitor.Collect(context.Background())

	overview, err := monitor.Overview(context.Background(), Range1Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Series) != 1 {
		t.Fatalf("series count = %d, want 1", len(overview.Series))
	}
	values := overview.Series[0].Average.Values
	for key, want := range map[string]float64{
		"disk.readBytesPerSecond": 100, "disk.writeBytesPerSecond": 200,
		"network.receivedBytesPerSecond": 300, "network.sentBytesPerSecond": 400,
	} {
		if got := values[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	payload, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"disk.readBytesPerSecond", "disk.writeBytesPerSecond", "network.receivedBytesPerSecond", "network.sentBytesPerSecond"} {
		if !strings.Contains(string(payload), key) {
			t.Errorf("overview JSON missing %q: %s", key, payload)
		}
	}
}

func TestMonitorMarksDataStaleAfterFifteenSeconds(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	now := base
	probe := &sequenceProbe{samples: []RawSample{{At: base, Memory: &Memory{TotalBytes: 100, AvailableBytes: 50, UsedBytes: 50, UsedPercent: 50}}}}
	monitor, err := New(openMonitorDB(t), probe, Options{Interval: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)
	monitor.Collect(context.Background())
	current, _ := monitor.Overview(context.Background(), Range15Minutes)
	if current.Stale {
		t.Fatal("fresh sample marked stale")
	}
	now = base.Add(16 * time.Second)
	stale, _ := monitor.Overview(context.Background(), Range15Minutes)
	if !stale.Stale {
		t.Fatal("old sample not marked stale")
	}
}

func TestCurrentOverviewDoesNotReadHistoricalSeries(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	db := openMonitorDB(t)
	probe := &sequenceProbe{samples: []RawSample{{At: base, Memory: &Memory{TotalBytes: 100, AvailableBytes: 50, UsedBytes: 50, UsedPercent: 50}}}}
	monitor, err := New(db, probe, Options{Interval: time.Hour, Now: func() time.Time { return base }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)
	monitor.Collect(context.Background())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	current := monitor.Current()
	if current.Current.Memory == nil || current.Current.Memory.UsedPercent != 50 {
		t.Fatalf("current memory = %#v", current.Current.Memory)
	}
	if len(current.Series) != 0 {
		t.Fatalf("current snapshot unexpectedly contains %d historical points", len(current.Series))
	}
}

func TestMonitorKeepsPartialDataAggregatesStorageAndReportsMostConstrainedRelevantVolume(t *testing.T) {
	base := time.Now().UTC()
	probe := &sequenceProbe{samples: []RawSample{{At: base,
		Memory: &Memory{TotalBytes: 1000, AvailableBytes: 400, UsedBytes: 600, UsedPercent: 60},
		Filesystems: []Filesystem{
			{ID: "install", Device: "disk-install", Mountpoint: "/opt/scriptboard", TotalBytes: 1000, UsedBytes: 700, UsedPercent: 70, AvailableBytes: 300, Roles: []string{"install"}, Online: true},
			{ID: "state", Device: "disk-state", Mountpoint: "/state", TotalBytes: 1000, UsedBytes: 920, UsedPercent: 92, AvailableBytes: 80, Roles: []string{"state"}, Online: true},
			{ID: "archive", Device: "disk-archive", Mountpoint: "/archive", TotalBytes: 8000, UsedBytes: 2380, UsedPercent: 29.75, AvailableBytes: 5620, Online: true},
			{ID: "archive-bind", Device: "disk-archive", Mountpoint: "/mnt/archive", TotalBytes: 8000, UsedBytes: 2380, UsedPercent: 29.75, AvailableBytes: 5620, Online: true},
		}, Errors: map[string]string{"cpu": "permission denied"},
	}}}
	monitor, err := New(openMonitorDB(t), probe, Options{Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.Close)
	monitor.Collect(context.Background())
	overview, err := monitor.Overview(context.Background(), Range15Minutes)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Current.Memory == nil || overview.Current.Memory.UsedPercent != 60 {
		t.Fatalf("memory lost after CPU failure: %#v", overview.Current)
	}
	if overview.Current.Storage == nil || overview.Current.Storage.TotalBytes != 10000 || overview.Current.Storage.UsedBytes != 4000 || overview.Current.Storage.AvailableBytes != 6000 || overview.Current.Storage.UsedPercent != 40 {
		t.Fatalf("storage summary = %#v", overview.Current.Storage)
	}
	if overview.Current.CriticalStorage == nil || overview.Current.CriticalStorage.ID != "state" {
		t.Fatalf("critical storage = %#v", overview.Current.CriticalStorage)
	}
	if overview.Errors["cpu"] == "" {
		t.Fatalf("errors = %#v", overview.Errors)
	}
	if overview.Errors["storage"] == "" {
		t.Fatalf("low critical volume was not reported: %#v", overview.Errors)
	}
}
