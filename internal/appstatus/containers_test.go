package appstatus_test

import (
	"context"
	"testing"
	"time"

	"scriptboard/internal/appstatus"
)

type containerOperationProbe struct {
	snapshot appstatus.RawSnapshot
	name     string
	action   appstatus.ContainerAction
}

func (probe *containerOperationProbe) Snapshot(context.Context) appstatus.RawSnapshot {
	return probe.snapshot
}

func (probe *containerOperationProbe) OperateContainer(_ context.Context, name string, action appstatus.ContainerAction) error {
	probe.name, probe.action = name, action
	return nil
}

func TestContainerViewKeepsStoppedContainersAndSortsThroughThePublicInterface(t *testing.T) {
	t.Parallel()

	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{{
		CollectedAt:     time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
		DockerAvailable: true,
		Containers: []appstatus.RawContainer{
			{ID: "running-id", Name: "/gateway", Image: "nginx:1.27", State: "running", CPUPercent: 12, PublishedPorts: []string{"0.0.0.0:8080 -> 80/tcp"}},
			{ID: "stopped-id", Name: "backup", Image: "restic:0.18", State: "exited", Status: "Exited (0) 2 hours ago"},
			{ID: "restarting-id", Name: "worker", Image: "example/worker:2", State: "restarting", Status: "Restarting (137)", Health: "unhealthy"},
		},
	}}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, err := monitor.ContainerView(context.Background(), appstatus.ContainerQuery{Sort: "name", Direction: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 3 || view.Running != 2 || view.Stopped != 1 || view.Attention != 1 {
		t.Fatalf("container counts = %#v", view)
	}
	if got := []string{view.Containers[0].Name, view.Containers[1].Name, view.Containers[2].Name}; got[0] != "worker" || got[1] != "gateway" || got[2] != "backup" {
		t.Fatalf("container order = %v", got)
	}
	if view.Containers[0].State != appstatus.ContainerRestarting || !view.Containers[0].Attention || view.Containers[2].State != appstatus.ContainerStopped {
		t.Fatalf("container states = %#v", view.Containers)
	}

	stopped, err := monitor.ContainerView(context.Background(), appstatus.ContainerQuery{Status: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Matched != 1 || len(stopped.Containers) != 1 || stopped.Containers[0].Name != "backup" {
		t.Fatalf("stopped filter = %#v", stopped)
	}
}

func TestPinnedContainerHistoryUsesNormalizedNameAcrossImageAndContainerIDChanges(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	probe := &snapshotProbe{snapshots: []appstatus.RawSnapshot{
		{CollectedAt: started, DockerAvailable: true, Containers: []appstatus.RawContainer{{ID: "old-id", Name: "/Gateway", Image: "nginx:1.26", State: "running"}}},
		{CollectedAt: started.Add(time.Minute), DockerAvailable: true, Containers: []appstatus.RawContainer{{ID: "new-id", Name: "gateway", Image: "nginx:1.27", State: "running"}}},
	}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{Now: func() time.Time { return started.Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.PinContainer(context.Background(), "GATEWAY"); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, err := monitor.ContainerView(context.Background(), appstatus.ContainerQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pinned) != 1 || view.Pinned[0].NameKey != "gateway" || view.Pinned[0].ID != "new-id" {
		t.Fatalf("pinned container = %#v", view.Pinned)
	}
	details, err := monitor.ContainerDetails(context.Background(), "gateway", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Versions) != 2 || details.Versions[0].Image != "nginx:1.27" || details.Versions[1].Image != "nginx:1.26" {
		t.Fatalf("version history = %#v", details.Versions)
	}
	if details.Versions[0].ContainerID != "new-id" || details.Versions[1].ContainerID != "old-id" {
		t.Fatalf("container ids = %#v", details.Versions)
	}
}

func TestContainerOperationResolvesTheExactNormalizedName(t *testing.T) {
	t.Parallel()

	probe := &containerOperationProbe{snapshot: appstatus.RawSnapshot{
		CollectedAt: time.Now().UTC(), DockerAvailable: true,
		Containers: []appstatus.RawContainer{{ID: "api-id", Name: "/API", Image: "example/api", State: "running"}},
	}}
	monitor, err := appstatus.New(openStore(t), probe, appstatus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.OperateContainer(context.Background(), "api", appstatus.ContainerRestart); err != nil {
		t.Fatal(err)
	}
	if probe.name != "api" || probe.action != appstatus.ContainerRestart {
		t.Fatalf("operation = %q %q", probe.name, probe.action)
	}
	if err := monitor.OperateContainer(context.Background(), "missing", appstatus.ContainerStop); err == nil {
		t.Fatal("operation accepted a container outside the current snapshot")
	}
	if err := monitor.OperateContainer(context.Background(), "api", appstatus.ContainerAction("remove")); err == nil {
		t.Fatal("operation accepted an unsupported action")
	}
}
