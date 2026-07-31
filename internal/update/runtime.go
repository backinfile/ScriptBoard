package update

import (
	"os"
	"path/filepath"
	"time"

	"scriptboard/internal/buildinfo"
)

type RuntimeMarker struct {
	Schema                int            `json:"schema"`
	BootID                string         `json:"boot_id"`
	PID                   int            `json:"pid"`
	Build                 buildinfo.Info `json:"build"`
	StartedAt             string         `json:"started_at"`
	ValidationOperationID string         `json:"validation_operation_id,omitempty"`
}

func WriteRuntimeMarker(stateRoot, validationOperationID string) (RuntimeMarker, error) {
	bootID, err := NewOperationID()
	if err != nil {
		return RuntimeMarker{}, err
	}
	marker := RuntimeMarker{
		Schema: OperationSchema, BootID: bootID, PID: os.Getpid(), Build: buildinfo.Current(),
		StartedAt:             time.Now().UTC().Format(time.RFC3339Nano),
		ValidationOperationID: validationOperationID,
	}
	if err := writeAtomicJSON(filepath.Join(stateRoot, "runtime.json"), marker, 0o600); err != nil {
		return RuntimeMarker{}, err
	}
	return marker, nil
}

func LoadRuntimeMarker(stateRoot string) (RuntimeMarker, error) {
	var marker RuntimeMarker
	if err := readStrictJSON(filepath.Join(stateRoot, "runtime.json"), &marker, 64<<10); err != nil {
		return RuntimeMarker{}, err
	}
	if marker.Schema != OperationSchema || marker.BootID == "" || marker.PID <= 0 {
		return RuntimeMarker{}, os.ErrInvalid
	}
	return marker, nil
}

func RemoveRuntimeMarker(stateRoot string) {
	_ = os.Remove(filepath.Join(stateRoot, "runtime.json"))
}

func PendingValidation(stateRoot string, _ buildinfo.Info) (string, bool) {
	active, err := LoadActive(stateRoot)
	if err != nil {
		return "", false
	}
	operation, err := LoadOperation(stateRoot, active.OperationID)
	if err != nil {
		return "", false
	}
	switch operation.Phase {
	case PhaseSwitching, PhaseStartingTarget, PhaseValidating, PhaseRollingBack, PhaseNeedsRecovery:
		return operation.ID, true
	default:
		return "", false
	}
}
