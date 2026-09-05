package mysqlmanager

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPlanManager(t *testing.T) (*Manager, PlanInput) {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	applyTestSchema(t, db)
	m, err := New(Options{DB: db, StateRoot: root, Now: func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	m.server = &fakeServer{}
	m.runner = &recordingRunner{output: "CREATE TABLE regression_fixture (id INT);\n"}
	i, err := m.SaveInstance(context.Background(), InstanceInput{Name: "fixture", Host: "localhost", Port: 3306, Username: "fixture", Password: "fixture", TLSMode: TLSDisabled})
	if err != nil {
		t.Fatal(err)
	}
	return m, PlanInput{Name: "Daily", InstanceID: i.ID, Databases: []string{"inventory"}, Expression: "0 0 * * *", RetentionCount: 10, Enabled: true}
}
func TestImpossibleBackupSchedule(t *testing.T) {
	m, input := testPlanManager(t)
	p, err := m.SavePlan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", p.ID} {
		input.ID = id
		input.Expression = "0 0 31 2 *"
		if _, err := m.SavePlan(context.Background(), input); err == nil || !strings.Contains(err.Error(), "no future occurrence") {
			t.Fatalf("impossible schedule: %v", err)
		}
	}
	stored, err := m.Plan(context.Background(), p.ID)
	if err != nil || stored.Expression != p.Expression {
		t.Fatalf("rejected edit changed stored plan: %+v %v", stored, err)
	}
}
func TestLegacyImpossibleBackupPlanIsDisabled(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		t.Run(map[bool]string{false: "poll", true: "startup"}[reconcile], func(t *testing.T) {
			m, input := testPlanManager(t)
			ctx := context.Background()
			p, err := m.SavePlan(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.db.Exec("UPDATE mysql_backup_plans SET expression='0 0 31 2 *',next_fire_at=? WHERE id=?", m.now().Add(-time.Minute).UnixNano(), p.ID); err != nil {
				t.Fatal(err)
			}
			if reconcile {
				if err := m.ReconcilePlans(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := m.RunDuePlans(ctx); err != nil {
					t.Fatal(err)
				}
			}
			p, err = m.Plan(ctx, p.ID)
			if err != nil || p.Enabled {
				t.Fatalf("invalid plan remained enabled: %+v %v", p, err)
			}
			var count int
			if err := m.db.QueryRow("SELECT COUNT(*) FROM mysql_operations").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid plan created %d operations", count)
			}
		})
	}
}
func TestBackupPlanMustAdvanceBeforeExecution(t *testing.T) {
	m, input := testPlanManager(t)
	ctx := context.Background()
	p, err := m.SavePlan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec("UPDATE mysql_backup_plans SET next_fire_at=? WHERE id=?", m.now().Add(-time.Minute).UnixNano(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec("CREATE TRIGGER reject_advance BEFORE UPDATE OF next_fire_at ON mysql_backup_plans BEGIN SELECT RAISE(FAIL,'advance unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if err := m.RunDuePlans(ctx); err == nil {
		t.Fatal("expected failed reservation")
	}
	var count int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM mysql_operations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed advance created %d operations", count)
	}
}
