package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/statebackup"
)

type brokerStateBackupService struct {
	stateRoot  string
	database   *sql.DB
	checkpoint *auditcheckpoint.Store
	audit      *auditlog.Store
	mu         sync.Mutex
}

func (service *brokerStateBackupService) Create(ctx context.Context, destination string, passphrase []byte) (statebackup.Artifact, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.checkpoint.VerifyOrBootstrap(ctx, service.audit, time.Now().UTC()); err != nil {
		return statebackup.Artifact{}, fmt.Errorf("verify signed audit checkpoint before state backup: %w", err)
	}
	document, err := service.checkpoint.TrustedDocument()
	if err != nil {
		return statebackup.Artifact{}, err
	}
	manager, err := statebackup.New(statebackup.Options{StateRoot: service.stateRoot, Database: service.database})
	if err != nil {
		return statebackup.Artifact{}, err
	}
	return manager.Create(ctx, statebackup.CreateRequest{Destination: destination, Passphrase: passphrase, AuditCheckpoint: document})
}

func (service *brokerStateBackupService) Inspect(ctx context.Context, archivePath string, passphrase []byte) (statebackup.Manifest, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return statebackup.Inspect(ctx, archivePath, passphrase)
}

func (service *brokerStateBackupService) Stage(ctx context.Context, archivePath string, passphrase []byte, confirmBackupID string) (statebackup.Stage, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return statebackup.StageRestore(ctx, statebackup.StageRequest{
		StateRoot: service.stateRoot, ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: confirmBackupID,
		MinimumSchemaVersion: 20, MaximumSchemaVersion: buildinfo.DatabaseSchemaVersion,
		ValidateStaged: func(ctx context.Context, databasePath string, manifest statebackup.Manifest) error {
			database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
			if err != nil {
				return err
			}
			defer database.Close()
			if err := database.PingContext(ctx); err != nil {
				return err
			}
			_, err = service.checkpoint.VerifyDetached(ctx, auditlog.New(database), manifest.AuditCheckpoint)
			return err
		},
	})
}

func (service *brokerStateBackupService) List(_ context.Context) ([]statebackup.Stage, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return statebackup.ListStages(service.stateRoot, time.Now().UTC())
}

func (service *brokerStateBackupService) Discard(_ context.Context, stageID string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	return statebackup.DiscardStage(service.stateRoot, stageID)
}
