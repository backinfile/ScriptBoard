package doctor_test

import (
	"path/filepath"
	"testing"

	"scriptboard/internal/app"
	"scriptboard/internal/doctor"
)

func TestDoctorReportsHealthyDirectoriesAndSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed")
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{ManagedRoot: managedRoot, StateRoot: stateRoot})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("close application: %v", err)
	}
	report := doctor.Run(doctor.Config{ManagedRoot: managedRoot, StateRoot: stateRoot})
	if !report.Healthy {
		t.Fatalf("doctor report is unhealthy: %+v", report.Checks)
	}
	if !report.HasHealthy("managed-root") || !report.HasHealthy("sqlite-integrity") {
		t.Fatalf("required checks missing: %+v", report.Checks)
	}
}
