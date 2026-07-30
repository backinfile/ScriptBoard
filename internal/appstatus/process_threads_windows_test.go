package appstatus

import (
	"context"
	"os"
	"testing"

	"github.com/shirou/gopsutil/v4/process"
)

func TestSnapshotThreadCountsIncludesCurrentProcess(t *testing.T) {
	counts, err := snapshotThreadCounts(context.Background())
	if err != nil {
		t.Fatalf("snapshot thread counts: %v", err)
	}
	if got := counts[int32(os.Getpid())]; got < 1 {
		t.Fatalf("current process thread count = %d, want at least 1", got)
	}
}

func TestCollectProcessUsesTheSharedThreadSnapshot(t *testing.T) {
	current, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		t.Fatalf("open current process: %v", err)
	}

	raw, ok := collectProcess(context.Background(), current, map[int32]int32{
		current.Pid: 123,
	})
	if !ok {
		t.Fatal("collect current process returned false")
	}
	if raw.Threads != 123 {
		t.Fatalf("thread count = %d, want value from shared snapshot", raw.Threads)
	}
}

func BenchmarkSnapshotThreadCounts(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		if _, err := snapshotThreadCounts(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSystemProbeSnapshot(b *testing.B) {
	probe := NewSystemProbe()
	b.Cleanup(func() {
		if err := probe.Close(); err != nil {
			b.Error(err)
		}
	})
	ctx := context.Background()
	for b.Loop() {
		if snapshot := probe.Snapshot(ctx); len(snapshot.Processes) == 0 {
			b.Fatal("system snapshot returned no processes")
		}
	}
}
