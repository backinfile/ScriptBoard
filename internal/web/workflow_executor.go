package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"scriptboard/internal/identity"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/workflow"
	"strings"
	"time"
)

type workflowExecutor struct{ app *App }

func (x workflowExecutor) Execute(ctx context.Context, run workflow.Run, n workflow.Node, inputs map[string]json.RawMessage, started func(string) error) (map[string]json.RawMessage, error) {
	a := x.app
	// Re-check the initiator at dispatch so queued work cannot outlive revoked execution rights.
	var role identity.Role
	if err := a.db.QueryRowContext(ctx, "SELECT role FROM users WHERE id=? AND enabled=1", run.Actor).Scan(&role); err != nil {
		return nil, err
	}
	if !identity.Allows(role, identity.PermissionExecute) {
		return nil, fmt.Errorf("execution permission revoked")
	}
	config := n.Config
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	get := func(key string) string {
		raw := inputs[key]
		if len(raw) == 0 {
			raw = config[key]
		}
		var value string
		_ = json.Unmarshal(raw, &value)
		return value
	}
	directory := get("directory")
	if directory == "" {
		directory = a.defaultHostDirectory(ctx)
	}
	if n.Kind == "git" {
		directory = filepath.Dir(get("directory"))
	}
	prepared, err := a.hostPrepareDirectory(ctx, directory)
	if err != nil {
		return nil, err
	}
	if n.Kind == "script" {
		script, prepareErr := a.hostPrepareScript(ctx, get("script"))
		if prepareErr != nil {
			return nil, prepareErr
		}
		if expected := get("scriptDigest"); expected != "" && expected != script.Digest {
			return nil, fmt.Errorf("local script changed; save and publish the workflow again")
		}
		config = cloneWorkflowConfig(config)
		config["scriptDigest"], _ = json.Marshal(script.Digest)
	}
	var output string
	// Node identifiers never become filesystem components; use a fixed hash-free random suffix.
	token, err := randomToken(18)
	if err != nil {
		return nil, err
	}
	output = filepath.Join(prepared.Path, ".scriptboard-workflow-"+token+".json")
	spec := map[string]any{"kind": n.Kind, "inputs": inputs, "config": config, "script": n.Script, "output": output}
	body, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	source := strings.ReplaceAll(workflowPython, "__SPEC__", base64.StdEncoding.EncodeToString(body))
	timeout := 1800
	if raw := config["timeout"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &timeout)
	}
	id, err := a.runs.StartOneTime(runmanager.OneTimeStartRequest{SourceType: "workflow", SourceName: n.Name, SourceID: run.ID, WorkingDirectory: prepared.Path, Extension: ".py", Source: source, TimeoutSeconds: timeout, MemoryLimit: get("memory"), PreparedDirectory: &prepared, InitiatorUserID: run.Actor, InitiatorRole: string(role), AuditSource: "workflow:" + run.ID + ":" + n.ID})
	if err != nil {
		return nil, err
	}
	if err = started(id); err != nil {
		_ = a.runs.Stop(id)
		return nil, fmt.Errorf("%w: persist process identity: %v", workflow.ErrUncertain, err)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = a.runs.Stop(id)
			return nil, ctx.Err()
		case <-ticker.C:
			current, err := (workflow.Repository{DB: a.db}).Run(ctx, run.ID)
			if err != nil {
				return nil, fmt.Errorf("%w: read execution status: %v", workflow.ErrUncertain, err)
			}
			if current.Status == "cancelling" {
				_ = a.runs.Stop(id)
			}
			process, err := a.runs.GetMetadata(id)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", workflow.ErrUncertain, err)
			}
			if process.FinishedAt == nil {
				continue
			}
			if process.Status != "succeeded" {
				return nil, fmt.Errorf("process %s: %s %s", id, process.Status, process.Error)
			}
			file, _, err := a.hostOpenRegular(ctx, output)
			if err != nil {
				return nil, fmt.Errorf("missing or invalid output: %w", err)
			}
			raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
			file.Close()
			if err != nil {
				return nil, err
			}
			if len(raw) > 4<<20 {
				return nil, fmt.Errorf("output exceeds 4 MiB")
			}
			result := map[string]json.RawMessage{}
			if err = json.Unmarshal(raw, &result); err != nil {
				return nil, err
			}
			_ = a.hostRemoveRegular(ctx, output)
			return result, nil
		}
	}
}

var workflowPython = mustWebAsset("ui/assets/workflow-runner.py")

func cloneWorkflowConfig(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
