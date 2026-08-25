package customtab

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"scriptboard/internal/secretstore"
)

func testManager(t *testing.T) (*Manager, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "custom-tabs.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := secretstore.New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(Options{DB: db, SecretStore: vault})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return manager, db
}

func TestLifecycleSealsKeyAndPreservesOrder(t *testing.T) {
	manager, db := testManager(t)
	ctx := context.Background()
	first, err := manager.Create(ctx, Input{Name: "服务控制台", TargetURL: "http://127.0.0.1:3000/path?q=1", Enabled: true, CredentialMode: ModeTargetState, VisibilityRoles: []string{"administrator", "operator"}})
	if err != nil {
		t.Fatal(err)
	}
	if !first.VisibleTo("administrator") || !first.VisibleTo("operator") || first.VisibleTo("viewer") {
		t.Fatalf("visibility roles=%v", first.VisibilityRoles)
	}
	second, err := manager.Create(ctx, Input{Name: "内部工单", TargetURL: "https://tickets.local:8443/", CredentialMode: ModeKey, KeyName: "access_key", Key: "never-store-plaintext"})
	if err != nil {
		t.Fatal(err)
	}
	var cipher []byte
	if err := db.QueryRow(`SELECT key_ciphertext FROM custom_tabs WHERE id=?`, second.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cipher), "never-store-plaintext") || !second.KeyConfigured {
		t.Fatalf("key was not sealed: cipher=%q tab=%#v", cipher, second)
	}
	keyName, key, err := manager.Credential(ctx, second.ID)
	if err != nil || keyName != "access_key" || key != "never-store-plaintext" {
		t.Fatalf("credential name=%q key=%q err=%v", keyName, key, err)
	}
	if _, err := manager.Update(ctx, second.ID, Input{Name: "内部工单 2", TargetURL: second.TargetURL, CredentialMode: ModeKey, KeyName: "access_key", PreserveKey: true}); err != nil {
		t.Fatal(err)
	}
	if _, key, err = manager.Credential(ctx, second.ID); err != nil || key != "never-store-plaintext" {
		t.Fatalf("preserved key=%q err=%v", key, err)
	}
	if _, err := manager.Move(ctx, second.ID, -1); err != nil {
		t.Fatal(err)
	}
	tabs, err := manager.List(ctx)
	if err != nil || len(tabs) != 2 || tabs[0].ID != second.ID || tabs[1].ID != first.ID {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	if _, err := manager.SetEnabled(ctx, second.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
}

func TestValidationAcceptsBrowserLocalHTTPButRejectsUnsafeURLs(t *testing.T) {
	manager, _ := testManager(t)
	ctx := context.Background()
	for _, target := range []string{"http://localhost:9090", "http://192.168.1.8:8080/ui", "https://service.local/app?mode=embed"} {
		if _, err := manager.Create(ctx, Input{Name: "Local", TargetURL: target, CredentialMode: ModeIsolated}); err != nil {
			t.Fatalf("target %q rejected: %v", target, err)
		}
	}
	for _, target := range []string{"/relative", "file:///tmp/page", "https://user:secret@example.com", "https://example.com/#secret", "javascript:alert(1)"} {
		if _, err := manager.Create(ctx, Input{Name: "Unsafe", TargetURL: target, CredentialMode: ModeIsolated}); err == nil {
			t.Fatalf("target %q accepted", target)
		}
	}
}

func TestVisibilityRequiresKnownRoleAndDefaultsForInternalCallers(t *testing.T) {
	manager, _ := testManager(t)
	ctx := context.Background()
	tab, err := manager.Create(ctx, Input{Name: "Default", TargetURL: "https://example.test", CredentialMode: ModeIsolated})
	if err != nil || !tab.VisibleTo("administrator") || !tab.VisibleTo("viewer") {
		t.Fatalf("default visibility=%v err=%v", tab.VisibilityRoles, err)
	}
	if _, err := manager.Create(ctx, Input{Name: "Empty", TargetURL: "https://example.test", CredentialMode: ModeIsolated, VisibilityRoles: []string{}}); err == nil {
		t.Fatal("empty visibility accepted")
	}
	if _, err := manager.Create(ctx, Input{Name: "Unknown", TargetURL: "https://example.test", CredentialMode: ModeIsolated, VisibilityRoles: []string{"root"}}); err == nil {
		t.Fatal("unknown visibility role accepted")
	}
}

func TestKeyCannotBePreservedAcrossOriginChange(t *testing.T) {
	manager, _ := testManager(t)
	ctx := context.Background()
	tab, err := manager.Create(ctx, Input{Name: "Tickets", TargetURL: "https://tickets.local/a", CredentialMode: ModeKey, KeyName: "key", Key: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(ctx, tab.ID, Input{Name: tab.Name, TargetURL: "https://other.local/a", CredentialMode: ModeKey, KeyName: "key", PreserveKey: true}); err == nil {
		t.Fatal("preserved key crossed target origin")
	}
}
