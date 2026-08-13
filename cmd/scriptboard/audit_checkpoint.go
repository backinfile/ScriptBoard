package main

import (
	"context"
	"fmt"
	"time"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/auditlog"
	"scriptboard/internal/secretstore"
)

func verifySignedAuditCheckpoint(ctx context.Context, stateRoot string, audit *auditlog.Store) error {
	checkpoint, err := openSignedAuditCheckpoint(stateRoot, true)
	if err != nil {
		return err
	}
	if err := checkpoint.VerifyOrBootstrap(ctx, audit, time.Time{}); err != nil {
		return fmt.Errorf("verify external signed audit checkpoint: %w", err)
	}
	return nil
}

func openSignedAuditCheckpoint(stateRoot string, readOnly bool) (*auditcheckpoint.Store, error) {
	vault, err := secretstore.Open(stateRoot)
	if err != nil {
		return nil, fmt.Errorf("open external credential key: %w", err)
	}
	checkpoint, err := auditcheckpoint.New(auditcheckpoint.Options{
		StateRoot: stateRoot, SecretStore: vault, ReadOnly: readOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("open external signed audit checkpoint: %w", err)
	}
	return checkpoint, nil
}
