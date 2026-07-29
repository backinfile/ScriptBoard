package appstatus_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/appstatus"
)

type snapshotProbe struct {
	snapshots []appstatus.RawSnapshot
	index     int
}

func (p *snapshotProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	if p.index >= len(p.snapshots) {
		return p.snapshots[len(p.snapshots)-1]
	}
	value := p.snapshots[p.index]
	p.index++
	return value
}

func TestViewAggregatesMatchingWindowsExecutablesAndDerivesRates(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{
		{
			CollectedAt:      started,
			LogicalCores:     4,
			TotalMemoryBytes: 1_000,
			Processes: []appstatus.RawProcess{
				{PID: 101, CreatedAt: started.Add(-time.Hour), Name: "worker", ExecutablePath: `C:\Apps\Worker.exe`, CPUSeconds: 10, ResidentMemoryBytes: 100, Threads: 2, ReadBytes: 100, WriteBytes: 200},
				{PID: 102, CreatedAt: started.Add(-time.Hour), Name: "worker", ExecutablePath: `c:\apps\worker.exe`, CPUSeconds: 5, ResidentMemoryBytes: 150, Threads: 3, ReadBytes: 50, WriteBytes: 100},
			},
		},
		{
			CollectedAt:      started.Add(5 * time.Second),
			LogicalCores:     4,
			TotalMemoryBytes: 1_000,
			Processes: []appstatus.RawProcess{
				{PID: 101, CreatedAt: started.Add(-time.Hour), Name: "worker", ExecutablePath: `C:\Apps\Worker.exe`, CPUSeconds: 12, ResidentMemoryBytes: 120, Threads: 4, ReadBytes: 300, WriteBytes: 500},
				{PID: 102, CreatedAt: started.Add(-time.Hour), Name: "worker", ExecutablePath: `c:\apps\worker.exe`, CPUSeconds: 7, ResidentMemoryBytes: 180, Threads: 5, ReadBytes: 150, WriteBytes: 250},
			},
		},
	}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{HostOS: "windows", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, err := monitor.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Applications) != 1 {
		t.Fatalf("applications = %#v, want one aggregated application", view.Applications)
	}
	application := view.Applications[0]
	if application.Kind != appstatus.KindHost || application.ProcessCount != 2 || application.ThreadCount != 9 {
		t.Fatalf("identity/counts = %#v", application)
	}
	if application.MemoryBytes != 300 || application.MemoryPercent != 30 {
		t.Fatalf("memory = %d / %.1f%%, want 300 / 30%%", application.MemoryBytes, application.MemoryPercent)
	}
	if application.CPUPercent != 20 || application.ReadBytesPerSecond != 60 || application.WriteBytesPerSecond != 90 {
		t.Fatalf("derived rates = CPU %.1f read %.1f write %.1f", application.CPUPercent, application.ReadBytesPerSecond, application.WriteBytesPerSecond)
	}
}

func TestPIDReuseDoesNotCreateRatesFromAnotherProcess(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{
		{
			CollectedAt:  started,
			LogicalCores: 4,
			Processes: []appstatus.RawProcess{{
				PID: 71, CreatedAt: started.Add(-time.Hour), Name: "agent",
				ExecutablePath: "/opt/agent", CPUSeconds: 500, ReadBytes: 10_000, WriteBytes: 20_000,
			}},
		},
		{
			CollectedAt:  started.Add(5 * time.Second),
			LogicalCores: 4,
			Processes: []appstatus.RawProcess{{
				PID: 71, CreatedAt: started.Add(2 * time.Second), Name: "agent",
				ExecutablePath: "/opt/agent", CPUSeconds: 1, ReadBytes: 20, WriteBytes: 40,
			}},
		},
	}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{HostOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, err := monitor.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	application := view.Applications[0]
	if application.RateAvailable || application.CPUPercent != 0 ||
		application.ReadBytesPerSecond != 0 || application.WriteBytesPerSecond != 0 {
		t.Fatalf("PID reuse produced rates: %#v", application)
	}
}

func TestMissingProcessCreationTimeDoesNotCreateRatesAcrossSamples(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{
		{
			CollectedAt:  started,
			LogicalCores: 4,
			Processes: []appstatus.RawProcess{{
				PID: 71, Name: "restricted-clock", ExecutablePath: "/opt/restricted-clock",
				CPUSeconds: 10, ReadBytes: 100, WriteBytes: 200,
			}},
		},
		{
			CollectedAt:  started.Add(5 * time.Second),
			LogicalCores: 4,
			Processes: []appstatus.RawProcess{{
				PID: 71, Name: "restricted-clock", ExecutablePath: "/opt/restricted-clock",
				CPUSeconds: 20, ReadBytes: 200, WriteBytes: 400,
			}},
		},
	}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{HostOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := monitor.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Applications[0].RateAvailable {
		t.Fatalf("process without a creation time produced a rate: %#v", view.Applications[0])
	}
}

func TestPinPersistsAcrossMonitorRestarts(t *testing.T) {
	t.Parallel()

	collectedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	raw := appstatus.RawSnapshot{
		CollectedAt: collectedAt,
		Processes: []appstatus.RawProcess{{
			PID: 91, CreatedAt: collectedAt.Add(-time.Hour), Name: "postgres",
			ExecutablePath: "/usr/lib/postgresql/postgres", ResidentMemoryBytes: 512,
		}},
	}
	db := openStore(t)
	first, err := appstatus.New(db, &snapshotProbe{snapshots: []appstatus.RawSnapshot{raw}}, appstatus.Options{HostOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := first.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Pin(context.Background(), before.Applications[0].ID); err != nil {
		t.Fatal(err)
	}

	restarted, err := appstatus.New(db, &snapshotProbe{snapshots: []appstatus.RawSnapshot{raw}}, appstatus.Options{HostOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := restarted.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Pinned) != 1 || after.Pinned[0].ID != before.Applications[0].ID || !after.Applications[0].Pinned {
		t.Fatalf("persisted view = %#v", after)
	}
}

func TestUnpinRemovesTheApplicationWithoutRemovingItsCurrentSnapshot(t *testing.T) {
	t.Parallel()

	collectedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{{
		CollectedAt: collectedAt,
		Containers: []appstatus.RawContainer{{
			Name: "api-prod", Image: "example/api:latest", CPUPercent: 12,
		}},
	}}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := monitor.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	id := before.Applications[0].ID
	if err := monitor.Pin(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Unpin(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	after, err := monitor.View(context.Background(), appstatus.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Pinned) != 0 || len(after.Applications) != 1 || after.Applications[0].Pinned {
		t.Fatalf("view after unpin = %#v", after)
	}
}

func TestViewSortsEveryVisibleColumnInBothDirections(t *testing.T) {
	t.Parallel()

	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{{
		CollectedAt: time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
		Containers: []appstatus.RawContainer{
			{Name: "alpha", CPUPercent: 10, MemoryBytes: 300, ReadBytesPerSecond: 30, WriteBytesPerSecond: 100, ProcessCount: 3},
			{Name: "beta", CPUPercent: 30, MemoryBytes: 100, ReadBytesPerSecond: 20, WriteBytesPerSecond: 300, ProcessCount: 1},
			{Name: "gamma", CPUPercent: 20, MemoryBytes: 200, ReadBytesPerSecond: 10, WriteBytesPerSecond: 200, ProcessCount: 2},
		},
	}}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial, err := monitor.View(context.Background(), appstatus.Query{Sort: "name", Direction: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Pin(context.Background(), initial.Applications[1].ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		field string
		asc   []string
	}{
		{field: "name", asc: []string{"alpha", "beta", "gamma"}},
		{field: "cpu", asc: []string{"alpha", "gamma", "beta"}},
		{field: "memory", asc: []string{"beta", "gamma", "alpha"}},
		{field: "read", asc: []string{"gamma", "beta", "alpha"}},
		{field: "write", asc: []string{"alpha", "gamma", "beta"}},
		{field: "processes", asc: []string{"beta", "gamma", "alpha"}},
		{field: "pinned", asc: []string{"alpha", "gamma", "beta"}},
	}
	for _, test := range tests {
		t.Run(test.field, func(t *testing.T) {
			for _, direction := range []string{"asc", "desc"} {
				view, err := monitor.View(context.Background(), appstatus.Query{Sort: test.field, Direction: direction})
				if err != nil {
					t.Fatal(err)
				}
				want := append([]string(nil), test.asc...)
				if direction == "desc" {
					if test.field == "pinned" {
						want = []string{"beta", "alpha", "gamma"}
					} else {
						for left, right := 0, len(want)-1; left < right; left, right = left+1, right-1 {
							want[left], want[right] = want[right], want[left]
						}
					}
				}
				for index, application := range view.Applications {
					if application.Name != want[index] {
						t.Fatalf("%s %s order = %#v, want %#v", test.field, direction, view.Applications, want)
					}
				}
			}
		})
	}
}

func TestViewFiltersBeforeApplyingTheResultLimit(t *testing.T) {
	t.Parallel()

	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{{
		CollectedAt: time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
		Containers: []appstatus.RawContainer{
			{Name: "alpha", Image: "example/worker:latest", CPUPercent: 30},
			{Name: "beta", Image: "example/api:latest", CPUPercent: 20},
			{Name: "gamma", Image: "example/api:canary", CPUPercent: 10},
		},
	}}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, err := monitor.View(context.Background(), appstatus.Query{Search: "api:", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if view.Matched != 2 || len(view.Applications) != 1 || !view.Truncated || view.Applications[0].Name != "beta" {
		t.Fatalf("filtered view = %#v", view)
	}
}

func openStore(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE application_pins (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		identity TEXT NOT NULL,
		name TEXT NOT NULL,
		technical TEXT NOT NULL,
		sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE(kind, identity)
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}
