package privilegebroker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"scriptboard/internal/store/memorysettings"
	"testing"
)

func TestMemorySettingsBrokerBindsStateRevisionAndParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	os.MkdirAll(path, 0700)

	before, err := memorysettings.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	executor := &MemorySettingsExecutor{StateRoot: path, Next: &fixtureExecutor{}}
	server, client := brokerFixture(t, &fixtureAuthorizer{actor: Actor{UserID: "user-1", Username: "admin", Role: "administrator"}}, executor)
	defer server.Close()
	ctx := WithAuthorization(context.Background(), Authorization{SessionToken: "session-token-fixture-0123456789", RequestID: "memory-settings-test"})
	next := before.Memory
	next.Total = "8GiB"
	next.PerRun = "512MiB"
	payload, _ := json.Marshal(next)
	if err := client.Invoke(ctx, ActionMemorySettings, "other-file", before.Revision, payload); err == nil {
		t.Fatal("accepted substituted file")
	}
	if err := client.Invoke(context.Background(), ActionMemorySettings, "runner-memory", before.Revision, payload); err == nil {
		t.Fatal("accepted missing authorization")
	}
	if err := client.Invoke(ctx, ActionMemorySettings, "runner-memory", before.Revision, payload); err != nil {
		t.Fatal(err)
	}
	after, err := memorysettings.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Memory != next {
		t.Fatal("not saved")
	}
	if err := client.Invoke(ctx, ActionMemorySettings, "runner-memory", before.Revision, payload); err == nil {
		t.Fatal("accepted stale revision")
	}
	malicious := json.RawMessage(`{"Total":"8GiB","ConfigPath":"/etc/other"}`)
	if err := client.Invoke(ctx, ActionMemorySettings, "runner-memory", after.Revision, malicious); err == nil {
		t.Fatal("accepted unrelated fields")
	}
}
