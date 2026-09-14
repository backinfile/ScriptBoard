package mysqlmanager

import (
	"context"
	"testing"
	"time"
)

func TestPlanDetailsHistorySurvivesRetentionAndSeparatesPlans(t *testing.T) {
	m, input := testPlanManager(t)
	ctx := context.Background()
	plan, err := m.SavePlan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	times, err := m.PlanNextFive(plan)
	if err != nil || len(times) != 5 {
		t.Fatalf("preview: %v %v", times, err)
	}
	for i, next := range times {
		want := time.Date(2026, 9, 6+i, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next=%v want=%v", next, want)
		}
	}
	first, err := m.Backup(ctx, BackupRequest{InstanceID: plan.InstanceID, Database: "inventory", PlanID: plan.ID, Kind: BackupScheduled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Backup(ctx, BackupRequest{InstanceID: plan.InstanceID, Database: "inventory", PlanID: plan.ID, Kind: BackupScheduled}); err != nil {
		t.Fatal(err)
	}
	if err := m.applyRetention(ctx, plan.ID, "inventory", 1); err != nil {
		t.Fatal(err)
	}
	// Removing an artifact must not remove its execution history.
	if _, err := m.db.Exec("DELETE FROM mysql_backups WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	input.Name = "Other plan"
	other, err := m.SavePlan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.recordSkippedOperation(ctx, plan.InstanceID, "inventory", other.ID); err != nil {
		t.Fatal(err)
	}
	op, _, release, err := m.beginOperation(ctx, "backup", plan.InstanceID, "inventory", Actor{}, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.updateOperation(ctx, op.ID, "failed", "fixture failure", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	release()
	if err := m.recordSkippedOperation(ctx, plan.InstanceID, "inventory", plan.ID); err != nil {
		t.Fatal(err)
	}
	rows, total, err := m.PlanOperationsPage(ctx, plan.ID, 2, 0)
	if err != nil || total != 4 || len(rows) != 2 {
		t.Fatalf("history %v %d %v", rows, total, err)
	}
	second, _, err := m.PlanOperationsPage(ctx, plan.ID, 2, 2)
	if err != nil || len(second) != 2 {
		t.Fatalf("second page: %v %v", second, err)
	}
	seen := map[string]bool{}
	phases := map[string]bool{}
	for _, row := range append(rows, second...) {
		if row.PlanID != plan.ID || seen[row.ID] {
			t.Fatalf("cross-plan or repeated record: %+v", row)
		}
		seen[row.ID] = true
		phases[row.Phase] = true
	}
	if !phases["failed"] || !phases["completed"] || !phases["skipped_overlap"] {
		t.Fatalf("missing execution states: %v", phases)
	}
}
