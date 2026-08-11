package doctor_test

import (
	"path/filepath"
	"strings"
	"testing"

	"scriptboard/internal/app"
	"scriptboard/internal/doctor"
)

func TestDoctorReportsHealthyDirectoriesAndSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	application, err := app.Open(app.Config{StateRoot: stateRoot})
	if err != nil {
		t.Fatalf("open application: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("close application: %v", err)
	}
	report := doctor.Run(doctor.Config{StateRoot: stateRoot})
	if !report.Healthy {
		t.Fatalf("doctor report is unhealthy: %+v", report.Checks)
	}
	if !report.HasHealthy("state-root") || !report.HasHealthy("sqlite-integrity") {
		t.Fatalf("required checks missing: %+v", report.Checks)
	}
}

func TestDoctorRedactsSecretsFromCheckDetails(t *testing.T) {
	t.Parallel()

	const secret = "doctor-password-value"
	report := doctor.Run(doctor.Config{
		StateRoot:  filepath.Join(t.TempDir(), "missing-password="+secret),
		ConfigPath: filepath.Join(t.TempDir(), "token="+secret),
	})
	for _, check := range report.Checks {
		if strings.Contains(check.Detail, secret) {
			t.Fatalf("check %q leaked secret in %q", check.Name, check.Detail)
		}
	}
}
