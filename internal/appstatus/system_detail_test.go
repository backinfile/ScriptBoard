package appstatus

import (
	"context"
	"testing"
	"time"
)

func TestSelectPrimaryProcessPrefersTheRootOfAnApplicationProcessTree(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	primary := selectPrimaryProcess([]RawProcess{
		{PID: 12, ParentPID: 11, CreatedAt: started.Add(-time.Hour), Name: "worker"},
		{PID: 11, ParentPID: 1, CreatedAt: started.Add(-30 * time.Minute), Name: "main"},
	})
	if primary.PID != 11 {
		t.Fatalf("primary PID = %d, want application process-tree root 11", primary.PID)
	}
}

func TestHostRuntimeDetailReportsAnExitedProcessAsStructuredUnavailableState(t *testing.T) {
	t.Parallel()

	detail := collectHostRuntimeDetail(context.Background(), DetailRequest{
		Application: Application{Kind: KindHost, Running: true},
	})
	if detail.State != RuntimeUnavailable || detail.Code != "process_exited" || detail.Host != nil {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestDockerTopProcessesUsesNamedColumnsWithoutDependingOnTheirOrder(t *testing.T) {
	t.Parallel()

	processes, firstPID := dockerTopProcesses(
		[]string{"CMD", "PPID", "USER", "PID"},
		[][]string{{"nginx: master", "0", "root", "1"}, {"nginx: worker", "1", "www-data", "17"}},
	)
	if firstPID != 1 || len(processes) != 2 ||
		processes[1].PID != 17 || processes[1].ParentPID != 1 ||
		processes[1].User != "www-data" || processes[1].CommandLine != "nginx: worker" {
		t.Fatalf("processes = %#v, first PID = %d", processes, firstPID)
	}
}
