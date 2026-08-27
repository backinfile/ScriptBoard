package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/doctor"
	app "scriptboard/internal/web"
)

func TestDoctorReportsHealthyDirectoriesAndSQLite(t *testing.T) {
	t.Parallel()

	root := sqliteTempDir(t)
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
	if !report.HasHealthy("state-root") || !report.HasHealthy("sqlite-integrity") ||
		!report.HasHealthy("credential-master-key") || !report.HasHealthy("audit-checkpoint-key") ||
		!report.HasHealthy("audit-checkpoint") {
		t.Fatalf("required checks missing: %+v", report.Checks)
	}
}

func sqliteTempDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "scriptboard-doctor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(30 * time.Second)
		for {
			removeErr := os.RemoveAll(root)
			if removeErr == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("remove SQLite test directory after bounded sharing-violation retries: %v", removeErr)
				return
			}
			// Windows security scanners can briefly open a just-closed SQLite file without delete sharing.
			time.Sleep(25 * time.Millisecond)
		}
	})
	return root
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
