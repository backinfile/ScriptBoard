package externaltrigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testManager(t *testing.T, now time.Time) (*Manager, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("initialize schema: %v", err)
		}
	}
	return New(db, Options{Now: func() time.Time { return now }, SecretsDirectory: filepath.Join(t.TempDir(), "secrets")}), db
}

func TestCreateKeyAndResolveEnabledLogEntry(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	manager, db := testManager(t, now)
	expiresAt := now.Add(24 * time.Hour)
	key, secret, err := manager.CreateKey(context.Background(), CreateKeyInput{
		Label: "CI pipeline", Enabled: true, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(secret, "sbk_") || strings.Contains(key.TokenHint, secret) {
		t.Fatalf("secret=%q hint=%q", secret, key.TokenHint)
	}
	var storedHash string
	if err := db.QueryRow("SELECT token_hash FROM external_trigger_keys WHERE id = ?", key.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == secret || strings.Contains(storedHash, secret) {
		t.Fatal("plaintext key was persisted")
	}
	revealed, err := manager.KeySecret(key.ID)
	if err != nil || revealed != secret {
		t.Fatalf("revealed secret=%q error=%v", revealed, err)
	}
	if err := filepath.Walk(manager.secretsDirectory, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		content, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(content), secret) {
			t.Fatalf("plaintext key was persisted in %s", path)
		}
		return readErr
	}); err != nil {
		t.Fatal(err)
	}

	entry, err := manager.CreateEntry(context.Background(), CreateEntryInput{
		KeyID: key.ID, Name: "deployment-log", Label: "Deployment callback", Type: ActionLog,
		Enabled: true, Config: LogConfig{File: "/logs/deploy.log", Category: "deploy", MaxMessageBytes: 1024},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	resolvedKey, resolvedEntry, err := manager.Resolve(context.Background(), secret, "deployment-log")
	if err != nil {
		t.Fatalf("resolve trigger: %v", err)
	}
	if resolvedKey.ID != key.ID || resolvedEntry.ID != entry.ID || resolvedEntry.Type != ActionLog {
		t.Fatalf("resolved key=%#v entry=%#v", resolvedKey, resolvedEntry)
	}
	if _, _, err := manager.Resolve(context.Background(), "sbk_invalid", "deployment-log"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key error = %v", err)
	}
}

func TestKeyNamesAreUniqueIgnoringCaseAndSurroundingWhitespace(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC))
	first, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Webhook", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: " webhook ", Enabled: true}); !errors.Is(err, ErrKeyLabelExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	second, _, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Deploy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateKey(context.Background(), second.ID, "WEBHOOK", nil); !errors.Is(err, ErrKeyLabelExists) {
		t.Fatalf("duplicate update error = %v", err)
	}
	if err := manager.UpdateKey(context.Background(), first.ID, "WEBHOOK", nil); err != nil {
		t.Fatalf("updating a key's own name: %v", err)
	}
}

func TestRotateAndDeleteKeyUpdateRecoverableSecret(t *testing.T) {
	manager, _ := testManager(t, time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC))
	key, original, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Agent", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, rotated, err := manager.RotateKey(context.Background(), key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == original {
		t.Fatal("rotation did not replace the key")
	}
	if revealed, revealErr := manager.KeySecret(key.ID); revealErr != nil || revealed != rotated {
		t.Fatalf("revealed rotated secret=%q error=%v", revealed, revealErr)
	}
	if err := manager.DeleteKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.KeySecret(key.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("deleted key secret error=%v", err)
	}
}

func TestResolveRejectsExpiredKeyAndDisabledEntry(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	manager, _ := testManager(t, now)
	expiresAt := now.Add(time.Hour)
	key, secret, err := manager.CreateKey(context.Background(), CreateKeyInput{Label: "Agent", Enabled: true, ExpiresAt: &expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.CreateEntry(context.Background(), CreateEntryInput{KeyID: key.ID, Name: "notice", Label: "Notice", Type: ActionLog, Enabled: false, Config: LogConfig{File: "/logs/notice.log", MaxMessageBytes: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Resolve(context.Background(), secret, entry.Name); !errors.Is(err, ErrEntryDisabled) {
		t.Fatalf("disabled entry error = %v", err)
	}
	if err := manager.SetEntryEnabled(context.Background(), entry.ID, true); err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return expiresAt }
	if _, _, err := manager.Resolve(context.Background(), secret, entry.Name); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expired key error = %v", err)
	}
}

func TestValidateVariableValueUsesConfiguredTypeBounds(t *testing.T) {
	minimum, maximum := int64(2), int64(5)
	integer := VariableConfig{VariableName: "retries", Type: VariableInteger, Minimum: &minimum, Maximum: &maximum}
	if value, err := ValidateVariableValue(integer, json.Number("4")); err != nil || value != "4" {
		t.Fatalf("integer value = %q, error = %v", value, err)
	}
	if _, err := ValidateVariableValue(integer, json.Number("6")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("out-of-range error = %v", err)
	}
	enumeration := VariableConfig{VariableName: "environment", Type: VariableEnum, Options: []string{"staging", "production"}}
	if _, err := ValidateVariableValue(enumeration, "root"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("enum error = %v", err)
	}
}
