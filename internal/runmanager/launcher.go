package runmanager

import (
	"context"
	"io"
)

type LaunchRequest struct {
	MemoryLimit      string   `json:"memoryLimit,omitempty"`
	RunID            string   `json:"runId"`
	ScriptPath       string   `json:"scriptPath"`
	ScriptDigest     string   `json:"scriptDigest"`
	WorkingDirectory string   `json:"workingDirectory"`
	Arguments        []string `json:"arguments"`
}

type ManagedProcess interface {
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Terminate(force bool) error
	Close() error
}

type ProcessLauncher interface {
	Launch(context.Context, LaunchRequest) (ManagedProcess, string, error)
	RuntimeIdentity() string
}
