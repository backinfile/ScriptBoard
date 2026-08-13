package registryconnection

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scriptboard/internal/registrymonitor"
)

const maxLegacyStoreBytes = 1 << 20

// MigrateLegacy imports the former Web-owned AES credential map into sealed,
// Broker-owned connection records. Legacy files are removed only after every
// recoverable record is active in the new store.
func (service *Service) MigrateLegacy(ctx context.Context, database *sql.DB, legacyDirectory string) error {
	if service == nil || database == nil {
		return errors.New("Registry connection migration requires a service and database")
	}
	keyPath := filepath.Join(legacyDirectory, "custom-dashboard-registry.master-key")
	dataPath := filepath.Join(legacyDirectory, "custom-dashboard-registry.json")
	key, keyErr := os.ReadFile(keyPath)
	body, dataErr := os.ReadFile(dataPath)
	if errors.Is(keyErr, os.ErrNotExist) && errors.Is(dataErr, os.ErrNotExist) {
		return nil
	}
	migrated, err := service.legacyMigrationComplete()
	if err != nil {
		return err
	}
	if migrated {
		return removeLegacyRegistryFiles(dataPath, keyPath)
	}
	if keyErr != nil || dataErr != nil || len(key) != 32 || len(body) > maxLegacyStoreBytes {
		return errors.New("legacy Registry credential store is incomplete or invalid")
	}
	values := map[string]string{}
	if json.Unmarshal(body, &values) != nil || len(values) > maxConnections {
		return errors.New("decode legacy Registry credentials")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	for cardID, encoded := range values {
		sealed, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil || len(sealed) < gcm.NonceSize() {
			return errors.New("decode legacy Registry credential")
		}
		plain, openErr := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(cardID))
		if openErr != nil {
			return errors.New("decrypt legacy Registry credential")
		}
		var cardType, rawConfig string
		rowErr := database.QueryRowContext(ctx, `SELECT type,config_json FROM custom_dashboard_cards WHERE id=?`, cardID).Scan(&cardType, &rawConfig)
		if errors.Is(rowErr, sql.ErrNoRows) || rowErr == nil && cardType != "registry" {
			continue
		}
		if rowErr != nil {
			return fmt.Errorf("load legacy Registry card: %w", rowErr)
		}
		var config registrymonitor.Config
		if json.Unmarshal([]byte(rawConfig), &config) != nil || registrymonitor.ValidateConfig(config) != nil {
			return fmt.Errorf("legacy Registry card %q has invalid configuration", cardID)
		}
		digest := sha256.Sum256([]byte(cardID))
		operationID := "legacy-registry:" + hex.EncodeToString(digest[:12])
		if err := service.Prepare(ctx, operationID, cardID, config, string(plain), false); err != nil {
			return fmt.Errorf("prepare legacy Registry card %q: %w", cardID, err)
		}
		if err := service.Commit(ctx, operationID); err != nil {
			return fmt.Errorf("commit legacy Registry card %q: %w", cardID, err)
		}
		if err := service.Acknowledge(ctx, operationID); err != nil {
			return fmt.Errorf("acknowledge legacy Registry card %q: %w", cardID, err)
		}
	}
	// Persist the completion marker before deleting either legacy file. A
	// restart after only one deletion can then safely finish the cleanup.
	if err := service.markLegacyMigrationComplete(); err != nil {
		return err
	}
	return removeLegacyRegistryFiles(dataPath, keyPath)
}

func (service *Service) legacyMigrationComplete() (bool, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	return state.LegacyMigrated, err
}

func (service *Service) markLegacyMigrationComplete() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load()
	if err != nil || state.LegacyMigrated {
		return err
	}
	state.LegacyMigrated = true
	return service.write(state)
}

func removeLegacyRegistryFiles(dataPath, keyPath string) error {
	if err := os.Remove(dataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy Registry credential map: %w", err)
	}
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy Registry master key: %w", err)
	}
	return nil
}
