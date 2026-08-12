package securitybaseline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryStoresOnlyCheckStatusAndReportsDrift(t *testing.T) {
	root := t.TempDir()
	store, err := NewHistoryStore(root)
	if err != nil {
		t.Fatal(err)
	}
	baseline := Report{Score: 100, Checks: []Check{{ID: "firewall", Status: StatusPass, Evidence: "secret host evidence", Guidance: "sensitive guidance"}}}
	capturedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	if _, err := store.Capture(baseline, capturedAt); err != nil {
		t.Fatal(err)
	}
	drift, err := store.Compare(Report{Score: 0, Checks: []Check{{ID: "firewall", Status: StatusAttention, Evidence: "other secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !drift.HasSnapshot || drift.Score != 100 || !drift.CapturedAt.Equal(capturedAt) || len(drift.Changes) != 1 || drift.Changes[0].ID != "firewall" || drift.Changes[0].Previous != StatusPass || drift.Changes[0].Current != StatusAttention {
		t.Fatalf("drift = %#v", drift)
	}
	body, err := os.ReadFile(filepath.Join(root, "security-baseline", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), "guidance") || !strings.Contains(string(body), `"firewall":"pass"`) {
		t.Fatalf("history contains unexpected data: %s", body)
	}
}

func TestHistoryRejectsOversizedOrMalformedState(t *testing.T) {
	root := t.TempDir()
	store, err := NewHistoryStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(`{"version":1,"snapshots":[{"captured_at":"bad"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Compare(Report{}); err == nil {
		t.Fatal("malformed history was accepted")
	}
}
