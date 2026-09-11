package privilegebroker

import (
	"context"
	"errors"
	"scriptboard/internal/resourcelimits"
	"scriptboard/internal/store/memorysettings"
)

const ActionMemorySettings Action = "memory_settings_save"

// MemorySettingsExecutor binds writes to the instance state root, never a client path.
type MemorySettingsExecutor struct {
	StateRoot string
	Next      Executor
}

func (e *MemorySettingsExecutor) Execute(ctx context.Context, request ExecutionRequest) error {
	if request.Action != ActionMemorySettings {
		return e.Next.Execute(ctx, request)
	}
	if request.Resource != "runner-memory" || e.StateRoot == "" {
		return errors.New("memory settings binding invalid")
	}
	var memory resourcelimits.Memory
	if err := decodeParameters(request.Parameters, &memory); err != nil {
		return err
	}
	return memorysettings.Save(e.StateRoot, request.Revision, memory)
}
