package hoststatus

import (
	"context"
	"database/sql"
	"path/filepath"
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
			CPU:      &CPUCounters{TotalSeconds: 100, IdleSeconds: 60},
			Disks:    map[string]DiskCounters{"disk0": {ReadBytes: 100, WriteBytes: 200, Reads: 10, Writes: 20}},
			Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1000, SentBytes: 500}},
		},
		{
			At:       base.Add(5 * time.Second),
			CPU:      &CPUCounters{TotalSeconds: 110, IdleSeconds: 65},
			Disks:    map[string]DiskCounters{"disk0": {ReadBytes: 600, WriteBytes: 1200, Reads: 20, Writes: 30}},
			Networks: map[string]NetworkCounters{"eth0": {ReceivedBytes: 1500, SentBytes: 1000}},
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
	if overview.Current.Disk == nil || overview.Current.Disk.ReadBytesPerSecond != 100 || overview.Current.Disk.WriteBytesPerSecond != 200 {
		t.Fatalf("disk = %#v", overview.Current.Disk)
	}
	if overview.Current.Network == nil || overview.Current.Network.ReceivedBytesPerSecond != 100 || overview.Current.Network.SentBytesPerSecond != 100 {
		t.Fatalf("network = %#v", overview.Current.Network)
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

func TestMonitorKeepsPartialDataAndSelectsMostConstrainedRelevantVolume(t *testing.T) {
	base := time.Now().UTC()
	probe := &sequenceProbe{samples: []RawSample{{At: base,
		Memory: &Memory{TotalBytes: 1000, AvailableBytes: 400, UsedBytes: 600, UsedPercent: 60},
		Filesystems: []Filesystem{
			{ID: "managed", Mountpoint: "/managed", UsedPercent: 70, AvailableBytes: 300, Roles: []string{"managed"}, Online: true},
			{ID: "state", Mountpoint: "/state", UsedPercent: 92, AvailableBytes: 80, Roles: []string{"state"}, Online: true},
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
	if overview.Current.Storage == nil || overview.Current.Storage.ID != "state" {
		t.Fatalf("storage summary = %#v", overview.Current.Storage)
	}
	if overview.Errors["cpu"] == "" {
		t.Fatalf("errors = %#v", overview.Errors)
	}
	if overview.Errors["storage"] == "" {
		t.Fatalf("low critical volume was not reported: %#v", overview.Errors)
	}
}
