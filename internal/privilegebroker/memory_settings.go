package privilegebroker

import (
	"context"
	"errors"
	"scriptboard/internal/config"
	"scriptboard/internal/resourcelimits"
)

const ActionMemorySettings Action = "memory_settings_save"

// MemorySettingsExecutor binds writes to the startup configuration path, never a client path.
type MemorySettingsExecutor struct {
	ConfigPath string
	Next       Executor
}

func (e *MemorySettingsExecutor) Execute(ctx context.Context, request ExecutionRequest) error {
	if request.Action != ActionMemorySettings {
		return e.Next.Execute(ctx, request)
	}
	if request.Resource != "runner-memory" || e.ConfigPath == "" {
		return errors.New("memory settings binding invalid")
	}
	var memory resourcelimits.Memory
	if err := decodeParameters(request.Parameters, &memory); err != nil {
		return err
	}
	return config.SaveMemorySettings(e.ConfigPath, request.Revision, memory)
}
