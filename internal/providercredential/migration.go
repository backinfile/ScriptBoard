package providercredential

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const legacyStorePurpose = "assistant-provider-credentials-v1"

// MigrateLegacy binds every credential-configured Assistant model to its
// owner, provider, model, endpoint and sharing policy before removing the old
// generic credential map. Missing credentials fail startup closed.
func (service *Service) MigrateLegacy(ctx context.Context, db *sql.DB) error {
	if service == nil || db == nil {
		return errors.New("provider credential migration requires a service and database")
	}
	legacyEncrypted := filepath.Join(filepath.Dir(service.path), "assistant-provider.enc")
	legacyPlain := filepath.Join(filepath.Dir(service.path), "assistant-provider.json")
	credentials := map[string]string{}
	legacyFound := false
	if body, err := os.ReadFile(legacyEncrypted); err == nil {
		legacyFound = true
		if len(body) > maxStoreBytes {
			return errors.New("legacy provider credential store is too large")
		}
		plain, err := service.vault.Unseal(legacyStorePurpose, body)
		if err != nil || json.Unmarshal(plain, &credentials) != nil {
			return errors.New("decode legacy provider credential store")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read legacy provider credentials: %w", err)
	}
	if body, err := os.ReadFile(legacyPlain); err == nil {
		legacyFound = true
		if len(body) > maxStoreBytes {
			return errors.New("legacy plaintext provider credential store is too large")
		}
		plainValues := map[string]string{}
		if json.Unmarshal(body, &plainValues) != nil {
			return errors.New("decode legacy plaintext provider credentials")
		}
		for id, credential := range plainValues {
			if strings.TrimSpace(credentials[id]) == "" {
				credentials[id] = credential
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read legacy plaintext provider credentials: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT id, owner_user_id, provider, model, endpoint, is_shared
		FROM assistant_models WHERE credential_configured = 1 ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list configured Assistant providers: %w", err)
	}
	defer rows.Close()
	type configuredModel struct {
		record Record
	}
	var models []configuredModel
	for rows.Next() {
		var model configuredModel
		var shared int
		if err := rows.Scan(&model.record.ID, &model.record.OwnerUserID, &model.record.Provider, &model.record.Model, &model.record.Endpoint, &shared); err != nil {
			return err
		}
		model.record = normalizeRecord(model.record)
		model.record.Shared = shared == 1
		if !validRecord(model.record) {
			return errors.New("configured Assistant provider has an invalid binding")
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	values, err := service.load()
	if err != nil {
		return err
	}
	changed := false
	for _, model := range models {
		existing, exists := values[model.record.ID]
		credential := strings.TrimSpace(credentials[model.record.ID])
		if credential == "" && exists {
			credential = existing.Credential
		}
		if !validCredential(credential, false) {
			return fmt.Errorf("configured Assistant provider %q has no recoverable credential", model.record.ID)
		}
		bound := storedRecord{
			OwnerUserID: model.record.OwnerUserID, Provider: model.record.Provider, Model: model.record.Model,
			Endpoint: model.record.Endpoint, Credential: credential, Shared: model.record.Shared,
		}
		if !exists || existing != bound {
			values[model.record.ID] = bound
			changed = true
		}
	}
	if changed {
		if err := service.write(values); err != nil {
			return fmt.Errorf("write migrated provider credentials: %w", err)
		}
	}
	if legacyFound {
		if err := os.Remove(legacyEncrypted); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy provider credential store: %w", err)
		}
		if err := os.Remove(legacyPlain); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy plaintext provider credentials: %w", err)
		}
	}
	return nil
}
