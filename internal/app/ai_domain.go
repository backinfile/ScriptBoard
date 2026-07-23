package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"scriptboard/internal/ai"
	"scriptboard/internal/diskspace"
	"scriptboard/internal/managedfiles"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/scheduler"
)

type aiDomain struct {
	app *App
}

func (d *aiDomain) register(registry *ai.ToolRegistry) error {
	query := func(name, description, schema string, handler ai.QueryHandler) error {
		return registry.RegisterQuery(ai.ToolDefinition{Name: name, Description: description, InputSchema: json.RawMessage(schema)}, handler)
	}
	action := func(name, description, schema string, risk ai.Risk, kind string) error {
		return registry.RegisterAction(ai.ToolDefinition{Name: name, Description: description, InputSchema: json.RawMessage(schema)}, risk,
			func(_ context.Context, raw json.RawMessage, call ai.CallContext) (ai.Action, error) {
				if !json.Valid(raw) {
					return ai.Action{}, errors.New("invalid JSON arguments")
				}
				prepared := ai.Action{
					Kind: kind, Risk: risk, Summary: description, Input: append(json.RawMessage(nil), raw...),
					ExpectedVersion: call.Conversation.ID,
				}
				prepared.Sensitive = kind == "variable.upsert" || suspectedCredential(raw)
				return prepared, nil
			})
	}
	for _, register := range []func() error{
		func() error {
			return query("list_files", "List Managed Entries in a directory.",
				`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listFiles)
		},
		func() error {
			return query("list_trash", "List ScriptBoard Trash entries.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listTrash)
		},
		func() error {
			return query("read_file", "Read a UTF-8 text chunk from a Managed Entry.",
				`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":65536}},"required":["path"],"additionalProperties":false}`, d.readFile)
		},
		func() error {
			return query("list_runs", "List Runs.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listRuns)
		},
		func() error {
			return query("get_run", "Read a Run and a bounded ordered Run Log segment.",
				`{"type":"object","properties":{"id":{"type":"string"},"after_sequence":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":500}},"required":["id"],"additionalProperties":false}`, d.getRun)
		},
		func() error {
			return query("list_schedules", "List Schedules with their next five triggers.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listSchedules)
		},
		func() error {
			return query("list_variables", "List variable metadata and, when authorized, values.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listVariables)
		},
		func() error {
			return query("list_quick_runs", "List Quick Runs.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`, d.listQuickRuns)
		},
		func() error {
			return query("list_audit", "List Audit Events without secret values.",
				`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":500},"offset":{"type":"integer","minimum":0},"query":{"type":"string"}},"additionalProperties":false}`, d.listAudit)
		},
		func() error {
			return query("get_version_protection", "Read Version Protection state.",
				`{"type":"object","additionalProperties":false}`, d.getVersionProtection)
		},
		func() error {
			return query("get_host_status", "Read the latest bounded local host and ScriptBoard service status.",
				`{"type":"object","additionalProperties":false}`, d.getHostStatus)
		},
		func() error {
			return query("get_ai_effective_settings", "Read the non-secret effective AI settings snapshot.",
				`{"type":"object","additionalProperties":false}`, d.getAISettings)
		},
		func() error {
			return query("list_attachments", "List staged attachments for this conversation.",
				`{"type":"object","additionalProperties":false}`, d.listAttachments)
		},
		func() error {
			return query("read_attachment", "Read a UTF-8 text attachment chunk; binary attachments return metadata only.",
				`{"type":"object","properties":{"id":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":65536}},"required":["id"],"additionalProperties":false}`, d.readAttachment)
		},

		func() error {
			return action("import_attachment", "Import a staged attachment into the Managed Root.",
				`{"type":"object","properties":{"attachment_id":{"type":"string"},"destination":{"type":"string"},"replace":{"type":"boolean"}},"required":["attachment_id","destination"],"additionalProperties":false}`, ai.RiskModify, "attachment.import")
		},
		func() error {
			return action("write_file", "Create or explicitly replace a UTF-8 text file.",
				`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"replace":{"type":"boolean"},"expected_sha256":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`, ai.RiskModify, "file.write")
		},
		func() error {
			return action("patch_file", "Patch a UTF-8 text file using exact replacements and an expected digest.",
				`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string"},"replacements":{"type":"array","items":{"type":"object","properties":{"old":{"type":"string"},"new":{"type":"string"},"all":{"type":"boolean"}},"required":["old","new"],"additionalProperties":false},"minItems":1}},"required":["path","expected_sha256","replacements"],"additionalProperties":false}`, ai.RiskModify, "file.patch")
		},
		func() error {
			return action("create_directory", "Create a directory in the Managed Root.",
				`{"type":"object","properties":{"path":{"type":"string"},"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`, ai.RiskModify, "file.mkdir")
		},
		func() error {
			return action("move_file", "Move or rename a Managed Entry.",
				`{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"}},"required":["source","destination"],"additionalProperties":false}`, ai.RiskModify, "file.move")
		},
		func() error {
			return action("delete_file", "Move a Managed Entry to ScriptBoard Trash.",
				`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`, ai.RiskModify, "file.delete")
		},
		func() error {
			return action("restore_trash", "Restore a Trash Entry to its original path.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskModify, "trash.restore")
		},
		func() error {
			return action("purge_trash", "Permanently purge one Trash Entry.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskModify, "trash.purge")
		},
		func() error {
			return action("set_executable", "Set or clear the Unix owner execute bit.",
				`{"type":"object","properties":{"path":{"type":"string"},"enabled":{"type":"boolean"}},"required":["path","enabled"],"additionalProperties":false}`, ai.RiskModify, "file.executable")
		},
		func() error {
			return action("run_script", "Start a trusted script through the Run Manager.",
				`{"type":"object","properties":{"path":{"type":"string"},"arguments":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":0,"maximum":86400}},"required":["path"],"additionalProperties":false}`, ai.RiskExecute, "run.start")
		},
		func() error {
			return action("stop_run", "Request termination of an active Run.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskExecute, "run.stop")
		},
		func() error {
			return action("run_quick_run", "Execute an existing Quick Run through the Run Manager.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskExecute, "quick_run.start")
		},
		func() error {
			return action("create_schedule", "Create a Schedule, optionally enabled immediately.",
				`{"type":"object","properties":{"name":{"type":"string"},"script_path":{"type":"string"},"arguments":{"type":"string"},"expression":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":0,"maximum":86400},"allow_overlap":{"type":"boolean"},"enabled":{"type":"boolean"}},"required":["name","script_path","expression"],"additionalProperties":false}`, ai.RiskExecute, "schedule.create")
		},
		func() error {
			return action("update_schedule", "Update a Schedule.",
				`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"script_path":{"type":"string"},"arguments":{"type":"string"},"expression":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":0,"maximum":86400},"allow_overlap":{"type":"boolean"}},"required":["id","name","script_path","expression"],"additionalProperties":false}`, ai.RiskExecute, "schedule.update")
		},
		func() error {
			return action("set_schedule_enabled", "Enable or disable a Schedule.",
				`{"type":"object","properties":{"id":{"type":"string"},"enabled":{"type":"boolean"}},"required":["id","enabled"],"additionalProperties":false}`, ai.RiskExecute, "schedule.enabled")
		},
		func() error {
			return action("run_schedule_now", "Run a Schedule immediately.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskExecute, "schedule.run")
		},
		func() error {
			return action("delete_schedule", "Delete a Schedule.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskExecute, "schedule.delete")
		},
		func() error {
			return action("upsert_variable", "Create or update a Variable.",
				`{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"},"password":{"type":"boolean"}},"required":["name","value"],"additionalProperties":false}`, ai.RiskModify, "variable.upsert")
		},
		func() error {
			return action("delete_variable", "Delete an unreferenced Variable.",
				`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`, ai.RiskModify, "variable.delete")
		},
		func() error {
			return action("create_quick_run", "Create a Quick Run from an existing Run.",
				`{"type":"object","properties":{"name":{"type":"string"},"source_run_id":{"type":"string"}},"required":["name","source_run_id"],"additionalProperties":false}`, ai.RiskModify, "quick_run.create")
		},
		func() error {
			return action("delete_quick_run", "Delete a Quick Run.",
				`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`, ai.RiskModify, "quick_run.delete")
		},
		func() error {
			return action("set_version_protection", "Enable, disable, or explicitly adopt Version Protection.",
				`{"type":"object","properties":{"mode":{"type":"string","enum":["enable","disable","adopt"]}},"required":["mode"],"additionalProperties":false}`, ai.RiskModify, "git.configure")
		},
		func() error {
			return action("version_checkpoint", "Create a Version Protection checkpoint.",
				`{"type":"object","additionalProperties":false}`, ai.RiskModify, "git.checkpoint")
		},
		func() error {
			return action("restore_versioned_file", "Restore one versioned file by commit.",
				`{"type":"object","properties":{"path":{"type":"string"},"commit":{"type":"string"}},"required":["path","commit"],"additionalProperties":false}`, ai.RiskModify, "git.restore")
		},
	} {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}

func decodeAI(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func pageValues(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (d *aiDomain) listFiles(ctx context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct {
		Path          string `json:"path"`
		Limit, Offset int
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	entries, err := d.app.managed.List(input.Path)
	if err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	if offset > len(entries) {
		offset = len(entries)
	}
	end := min(offset+limit, len(entries))
	return json.Marshal(map[string]any{"path": input.Path, "items": entries[offset:end], "next_offset": nullableNext(end, len(entries))})
}

func nullableNext(end, total int) any {
	if end >= total {
		return nil
	}
	return end
}

func (d *aiDomain) listTrash(ctx context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct{ Limit, Offset int }
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	rows, err := d.app.db.QueryContext(ctx, `SELECT id,original_path,deleted_at,size,is_directory
		FROM trash_entries ORDER BY deleted_at DESC,id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, path string
		var deleted int64
		var size int64
		var directory bool
		if err := rows.Scan(&id, &path, &deleted, &size, &directory); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "original_path": path, "deleted_at": time.Unix(deleted, 0), "size": size, "directory": directory,
		})
	}
	return json.Marshal(map[string]any{"items": items, "next_offset": offset + len(items)})
}

func (d *aiDomain) readFile(ctx context.Context, raw json.RawMessage, call ai.CallContext) (json.RawMessage, error) {
	var input struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Limit  int64  `json:"limit"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	if input.Offset < 0 {
		return nil, errors.New("offset cannot be negative")
	}
	if input.Limit <= 0 || input.Limit > 64<<10 {
		input.Limit = 64 << 10
	}
	file, info, err := d.app.managed.OpenRegular(input.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(input.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(file, input.Limit))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"path": input.Path, "size": info.Size(), "sha256": hex.EncodeToString(hash.Sum(nil)), "binary": true})
	}
	if suspectedCredential(content) && !call.Profile.AllowSensitiveReads {
		return json.Marshal(map[string]any{"path": input.Path, "size": info.Size(), "sensitive": true, "text_withheld": true})
	}
	if suspectedCredential(content) {
		d.app.recordAudit("ai_sensitive_query", input.Path, "succeeded", "ai")
	}
	return json.Marshal(map[string]any{
		"path": input.Path, "offset": input.Offset, "text": string(content),
		"size": info.Size(), "next_offset": nullableOffset(input.Offset+int64(len(content)), info.Size()),
	})
}

func nullableOffset(end, total int64) any {
	if end >= total {
		return nil
	}
	return end
}

func (d *aiDomain) listRuns(_ context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct{ Limit, Offset int }
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	runs, err := d.app.runs.ListPage(limit, offset)
	if err != nil {
		return nil, err
	}
	for index := range runs {
		runs[index].Events = nil
		runs[index].Arguments = nil
	}
	return json.Marshal(map[string]any{"items": runs, "next_offset": offset + len(runs)})
}

func (d *aiDomain) getRun(_ context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct {
		ID            string `json:"id"`
		AfterSequence int64  `json:"after_sequence"`
		Limit         int    `json:"limit"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	if input.Limit <= 0 || input.Limit > 500 {
		input.Limit = 100
	}
	run, err := d.app.runs.Get(input.ID)
	if err != nil {
		return nil, err
	}
	var events []runmanager.Event
	for _, event := range run.Events {
		if event.Sequence > input.AfterSequence && len(events) < input.Limit {
			events = append(events, event)
		}
	}
	run.Events = events
	run.Arguments = nil
	return json.Marshal(run)
}

func (d *aiDomain) listSchedules(_ context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct{ Limit, Offset int }
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	items, err := d.app.scheduler.ListPage(limit, offset)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"timezone": time.Now().Location().String(), "items": items})
}

func (d *aiDomain) listVariables(ctx context.Context, raw json.RawMessage, call ai.CallContext) (json.RawMessage, error) {
	var input struct{ Limit, Offset int }
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	rows, err := d.app.db.QueryContext(ctx, "SELECT name,value,is_password,updated_at FROM variables ORDER BY name LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var name, value string
		var password bool
		var updated int64
		if err := rows.Scan(&name, &value, &password, &updated); err != nil {
			return nil, err
		}
		item := map[string]any{"name": name, "password": password, "updated_at": time.Unix(updated, 0)}
		if call.Profile.AllowSensitiveReads {
			item["value"] = value
		}
		items = append(items, item)
	}
	if call.Profile.AllowSensitiveReads {
		d.app.recordAudit("ai_sensitive_query", "variables", "succeeded", "ai")
	}
	return json.Marshal(map[string]any{"items": items, "values_included": call.Profile.AllowSensitiveReads})
}

var suspectedCredentialPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|password|secret|authorization)\s*[:=]`)

func suspectedCredential(content []byte) bool {
	return suspectedCredentialPattern.Match(content)
}

func (d *aiDomain) listQuickRuns(ctx context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct{ Limit, Offset int }
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	rows, err := d.app.db.QueryContext(ctx, "SELECT id,name,script_path,arguments_template,timeout_seconds,source_run_id FROM quick_runs ORDER BY sort_order LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, name, script, arguments, source string
		var timeout int
		if err := rows.Scan(&id, &name, &script, &arguments, &timeout, &source); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name, "script_path": script, "arguments": arguments, "timeout_seconds": timeout, "source_run_id": source})
	}
	return json.Marshal(map[string]any{"items": items})
}

func (d *aiDomain) listAudit(ctx context.Context, raw json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	var input struct {
		Limit, Offset int
		Query         string `json:"query"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	limit, offset := pageValues(input.Limit, input.Offset)
	pattern := "%" + input.Query + "%"
	rows, err := d.app.db.QueryContext(ctx, `SELECT occurred_at,action,target,result,source_address FROM audit_events
		WHERE ?='' OR action LIKE ? OR target LIKE ? ORDER BY occurred_at DESC LIMIT ? OFFSET ?`,
		input.Query, pattern, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var occurred int64
		var action, target, result, source string
		if err := rows.Scan(&occurred, &action, &target, &result, &source); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"occurred_at": time.Unix(occurred, 0), "action": action, "target": target, "result": result, "source": source})
	}
	return json.Marshal(map[string]any{"items": items})
}

func (d *aiDomain) getVersionProtection(_ context.Context, _ json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	state, err := d.app.gitProtection.State()
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func (d *aiDomain) getHostStatus(ctx context.Context, _ json.RawMessage, _ ai.CallContext) (json.RawMessage, error) {
	overview, err := d.app.hostStatus.Overview(ctx, "1h")
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"facts": overview.Facts, "current": overview.Current, "collected_at": overview.CollectedAt,
		"capabilities": overview.Capabilities, "errors": overview.Errors,
	})
}

func (d *aiDomain) getAISettings(ctx context.Context, _ json.RawMessage, call ai.CallContext) (json.RawMessage, error) {
	settings, err := d.app.aiStore.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"profile":              map[string]any{"id": call.Profile.ID, "name": call.Profile.Name, "protocol": call.Profile.Protocol, "model": call.Profile.Model},
		"permission":           ai.EffectivePermission(call.Profile.Permission, call.Conversation.Permission, ai.Permission{Query: true, Execute: true, Modify: true}),
		"max_concurrent_turns": settings.MaxConcurrentTurns, "kill_switch": settings.KillSwitch,
	})
}

func (d *aiDomain) listAttachments(ctx context.Context, _ json.RawMessage, call ai.CallContext) (json.RawMessage, error) {
	items, err := d.app.aiStore.ListAttachments(ctx, call.Conversation.ID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].StoredPath = ""
	}
	return json.Marshal(map[string]any{"items": items})
}

func (d *aiDomain) readAttachment(ctx context.Context, raw json.RawMessage, call ai.CallContext) (json.RawMessage, error) {
	var input struct {
		ID     string `json:"id"`
		Offset int64  `json:"offset"`
		Limit  int64  `json:"limit"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	if input.Limit <= 0 || input.Limit > 64<<10 {
		input.Limit = 64 << 10
	}
	item, err := d.app.aiStore.GetAttachment(ctx, call.Conversation.ID, input.ID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(item.StoredPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(input.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(file, input.Limit))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return json.Marshal(map[string]any{"id": item.ID, "name": item.Name, "size": item.Size, "sha256": item.SHA256, "binary": true})
	}
	if suspectedCredential(content) && !call.Profile.AllowSensitiveReads {
		return json.Marshal(map[string]any{"id": item.ID, "name": item.Name, "size": item.Size, "sha256": item.SHA256, "sensitive": true, "text_withheld": true})
	}
	if suspectedCredential(content) {
		d.app.recordAudit("ai_sensitive_query", "attachment:"+item.ID, "succeeded", "ai")
	}
	return json.Marshal(map[string]any{
		"id": item.ID, "name": item.Name, "size": item.Size, "sha256": item.SHA256,
		"offset": input.Offset, "text": string(content), "next_offset": nullableOffset(input.Offset+int64(len(content)), item.Size),
	})
}

func (d *aiDomain) Validate(ctx context.Context, action ai.Action) error {
	switch action.Kind {
	case "attachment.import":
		var input struct {
			AttachmentID string `json:"attachment_id"`
			Destination  string `json:"destination"`
			Replace      bool   `json:"replace"`
		}
		if err := decodeAI(action.Input, &input); err != nil {
			return err
		}
		if input.AttachmentID == "" || input.Destination == "" {
			return errors.New("attachment_id and destination are required")
		}
		if _, err := d.app.aiStore.GetAttachment(ctx, action.ExpectedVersion, input.AttachmentID); err != nil {
			return err
		}
		if _, err := d.app.managed.Info(input.Destination); err == nil && !input.Replace {
			return errors.New("destination exists and replace is false")
		}
	case "file.write":
		var input struct {
			Path           string `json:"path"`
			Content        string `json:"content"`
			ExpectedSHA256 string `json:"expected_sha256"`
			Replace        bool   `json:"replace"`
		}
		if err := decodeAI(action.Input, &input); err != nil {
			return err
		}
		if input.Path == "" || len([]byte(input.Content)) > 1<<20 || !utf8.ValidString(input.Content) {
			return errors.New("invalid text file path or content")
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(input.Path)))
		if parent == "." {
			parent = ""
		}
		if _, err := d.app.managed.List(parent); err != nil {
			return err
		}
		if _, err := d.app.managed.Info(input.Path); err == nil && (!input.Replace || input.ExpectedSHA256 == "") {
			return errors.New("existing file replacement requires replace=true and expected_sha256")
		}
	case "file.patch":
		_, _, err := d.patchContent(action.Input)
		return err
	case "file.move", "file.delete", "file.executable":
		path := actionString(action.Input, "path")
		if action.Kind == "file.move" {
			path = actionString(action.Input, "source")
		}
		if path == "" {
			return errors.New("path is required")
		}
		if d.app.runs.ConflictsPath(path) {
			return errors.New("an active Run holds a Run Lease for this path")
		}
		_, err := d.app.managed.Info(path)
		return err
	case "run.start":
		path := actionString(action.Input, "path")
		_, err := d.app.managed.PrepareScript(path)
		return err
	case "run.stop":
		run, err := d.app.runs.Get(actionString(action.Input, "id"))
		if err != nil {
			return err
		}
		if run.Status != "starting" && run.Status != "running" && run.Status != "stopping" && run.Status != "timing_out" {
			return errors.New("Run is not active")
		}
	case "quick_run.start":
		var exists bool
		if err := d.app.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM quick_runs WHERE id=?)", actionString(action.Input, "id")).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return sql.ErrNoRows
		}
	case "schedule.create", "schedule.update":
		var input struct {
			Name           string `json:"name"`
			ScriptPath     string `json:"script_path"`
			Arguments      string `json:"arguments"`
			Expression     string `json:"expression"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			AllowOverlap   bool   `json:"allow_overlap"`
		}
		if err := decodeAI(action.Input, &input); err != nil {
			return err
		}
		if input.Name == "" || len([]byte(input.Name)) > 256 || input.TimeoutSeconds < 0 || input.TimeoutSeconds > 86400 {
			return errors.New("invalid Schedule name or timeout")
		}
		if _, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(input.Expression); err != nil {
			return err
		}
		_, err := d.app.managed.PrepareScript(input.ScriptPath)
		return err
	case "variable.upsert":
		var input struct {
			Name, Value string
		}
		if err := decodeAI(action.Input, &input); err != nil {
			return err
		}
		if !variableNamePattern.MatchString(input.Name) || len([]byte(input.Value)) > 4<<10 {
			return errors.New("invalid Variable name or value")
		}
	case "git.restore":
		if d.app.runs.HasActive() {
			return errors.New("Version Protection restore is blocked while a Run is active")
		}
	case "git.configure":
		if d.app.runs.HasActive() {
			return errors.New("Version Protection changes are blocked while a Run is active")
		}
		switch actionString(action.Input, "mode") {
		case "enable", "disable", "adopt":
		default:
			return errors.New("invalid Version Protection mode")
		}
	}
	return nil
}

func (d *aiDomain) Execute(ctx context.Context, action ai.Action) (json.RawMessage, error) {
	if err := d.Validate(ctx, action); err != nil {
		return nil, err
	}
	var result any
	var err error
	switch action.Kind {
	case "attachment.import":
		result, err = d.importAttachment(ctx, action.ExpectedVersion, action.Input)
	case "file.write":
		result, err = d.writeFile(action.Input)
	case "file.patch":
		result, err = d.applyPatch(action.Input)
	case "file.mkdir":
		var input struct{ Path, Name string }
		err = decodeAI(action.Input, &input)
		if err == nil {
			err = diskspace.Require(d.app.managedRoot, diskspace.MinimumWritableBytes)
		}
		if err == nil {
			err = d.app.managed.CreateDirectory(input.Path, input.Name)
		}
		createdPath := filepath.ToSlash(filepath.Join(input.Path, input.Name))
		if err == nil {
			err = d.app.checkpointWebMutation("ai-create-directory", createdPath)
		}
		result = map[string]any{"path": createdPath}
	case "file.move":
		var input struct{ Source, Destination string }
		err = decodeAI(action.Input, &input)
		if err == nil {
			err = d.app.managed.Move(input.Source, input.Destination)
		}
		if err == nil {
			err = d.app.checkpointWebMutation("ai-move", input.Destination)
		}
		result = map[string]any{"source": input.Source, "destination": input.Destination}
	case "file.delete":
		result, err = d.deleteFile(actionString(action.Input, "path"))
	case "trash.restore":
		result, err = d.restoreTrash(actionString(action.Input, "id"))
	case "trash.purge":
		result, err = d.purgeTrash(actionString(action.Input, "id"))
	case "file.executable":
		var input struct {
			Path    string
			Enabled bool
		}
		err = decodeAI(action.Input, &input)
		if err == nil {
			info, infoErr := d.app.managed.Info(input.Path)
			err = infoErr
			if err == nil && (info.Mode().Perm()&0o100 != 0) != input.Enabled {
				_, err = d.app.managed.ToggleOwnerExecute(input.Path)
			}
		}
		if err == nil {
			err = d.app.checkpointWebMutation("ai-executable", input.Path)
		}
		result = map[string]any{"path": input.Path, "enabled": input.Enabled}
	case "run.start":
		var input struct {
			Path           string `json:"path"`
			Arguments      string `json:"arguments"`
			TimeoutSeconds *int   `json:"timeout_seconds"`
		}
		err = decodeAI(action.Input, &input)
		timeout := 300
		if conversation, profileErr := d.app.aiStore.GetConversation(ctx, action.ExpectedVersion); profileErr == nil {
			if profile, profileErr := d.app.aiStore.GetProfile(ctx, conversation.ProfileID); profileErr == nil {
				timeout = profile.DefaultRunTimeoutSec
			}
		}
		if input.TimeoutSeconds != nil {
			timeout = *input.TimeoutSeconds
		}
		var variables map[string]string
		if err == nil {
			variables, err = d.app.loadVariables()
		}
		var id string
		if err == nil {
			id, err = d.app.runs.Start(runmanager.StartRequest{
				ScriptPath: input.Path, ArgumentsTemplate: input.Arguments, TimeoutSeconds: timeout,
				SourceType: "ai", SourceName: "AI action batch", Variables: variables,
			})
		}
		result = map[string]any{"run_id": id, "variable_names": mapKeys(variables)}
		if err == nil {
			if completed := d.waitRun(ctx, id, 30*time.Second); completed != nil {
				result = map[string]any{
					"run_id": id, "status": completed.Status, "exit_code": completed.ExitCode,
					"error": completed.Error, "variable_names": mapKeys(variables),
				}
			}
		}
	case "run.stop":
		id := actionString(action.Input, "id")
		err = d.app.runs.Stop(id)
		result = map[string]any{"run_id": id, "status": "stopping"}
	case "quick_run.start":
		id := actionString(action.Input, "id")
		var name, script, arguments string
		var timeout int
		err = d.app.db.QueryRowContext(ctx, `SELECT name,script_path,arguments_template,timeout_seconds
			FROM quick_runs WHERE id=?`, id).Scan(&name, &script, &arguments, &timeout)
		var variables map[string]string
		if err == nil {
			variables, err = d.app.loadVariables()
		}
		var runID string
		if err == nil {
			runID, err = d.app.runs.Start(runmanager.StartRequest{
				ScriptPath: script, ArgumentsTemplate: arguments, TimeoutSeconds: timeout,
				SourceType: "quick-run", SourceName: name, Variables: variables,
			})
		}
		result = map[string]any{"quick_run_id": id, "run_id": runID, "variable_names": mapKeys(variables)}
	case "schedule.create":
		result, err = d.createSchedule(action.Input)
	case "schedule.update":
		result, err = d.updateSchedule(action.Input)
	case "schedule.enabled":
		var input struct {
			ID      string
			Enabled bool
		}
		err = decodeAI(action.Input, &input)
		if err == nil {
			err = d.app.scheduler.SetEnabled(input.ID, input.Enabled)
		}
		result = map[string]any{"id": input.ID, "enabled": input.Enabled}
	case "schedule.run":
		id := actionString(action.Input, "id")
		var runID string
		runID, err = d.app.scheduler.RunNow(id)
		result = map[string]any{"id": id, "run_id": runID}
	case "schedule.delete":
		id := actionString(action.Input, "id")
		err = d.app.scheduler.Delete(id)
		result = map[string]any{"id": id, "deleted": err == nil}
	case "variable.upsert":
		result, err = d.upsertVariable(action.Input)
	case "variable.delete":
		result, err = d.deleteVariable(actionString(action.Input, "name"))
	case "quick_run.create":
		result, err = d.createQuickRun(action.Input)
	case "quick_run.delete":
		id := actionString(action.Input, "id")
		_, err = d.app.db.ExecContext(ctx, "DELETE FROM quick_runs WHERE id=?", id)
		result = map[string]any{"id": id}
	case "git.checkpoint":
		err = d.app.gitProtection.Checkpoint("ScriptBoard AI checkpoint\n\nScriptBoard-Operation: ai-batch")
		result = map[string]any{"checkpoint": err == nil}
	case "git.configure":
		mode := actionString(action.Input, "mode")
		switch mode {
		case "enable":
			err = d.app.gitProtection.Enable()
		case "disable":
			err = d.app.gitProtection.Disable()
		case "adopt":
			err = d.app.gitProtection.Adopt()
		}
		result = map[string]any{"mode": mode, "succeeded": err == nil}
	case "git.restore":
		var input struct{ Path, Commit string }
		err = decodeAI(action.Input, &input)
		if err == nil {
			err = d.app.gitProtection.RestoreFile(input.Path, input.Commit)
		}
		result = map[string]any{"path": input.Path, "commit": input.Commit}
	default:
		err = fmt.Errorf("unsupported AI action %q", action.Kind)
	}
	if err != nil {
		d.app.recordAudit("ai_"+strings.ReplaceAll(action.Kind, ".", "_"), action.Summary, "failed", "ai")
		return nil, err
	}
	d.app.recordAudit("ai_"+strings.ReplaceAll(action.Kind, ".", "_"), action.Summary, "succeeded", "ai")
	encoded, _ := json.Marshal(result)
	return encoded, nil
}

func (d *aiDomain) waitRun(parent context.Context, id string, maximum time.Duration) *runmanager.Run {
	ctx, cancel := context.WithTimeout(parent, maximum)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := d.app.runs.Get(id)
		if err != nil {
			return nil
		}
		switch run.Status {
		case "starting", "running", "stopping", "timing_out":
		default:
			return &run
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (d *aiDomain) importAttachment(ctx context.Context, conversationID string, raw json.RawMessage) (any, error) {
	var input struct {
		AttachmentID string `json:"attachment_id"`
		Destination  string `json:"destination"`
		Replace      bool   `json:"replace"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	item, err := d.app.aiStore.GetAttachment(ctx, conversationID, input.AttachmentID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(item.StoredPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(input.Destination)))
	if parent == "." {
		parent = ""
	}
	name := filepath.Base(filepath.FromSlash(input.Destination))
	stored, _ := randomToken(18)
	trashed, err := d.app.managed.Upload(parent, name, file, 100<<20, input.Replace, stored)
	if err != nil {
		return nil, err
	}
	if trashed != nil {
		if err := d.recordTrash(*trashed); err != nil {
			return nil, err
		}
	}
	if err := d.app.checkpointWebMutation("ai-import-attachment", input.Destination); err != nil {
		return nil, err
	}
	if err := d.app.aiStore.ConsumeAttachment(ctx, conversationID, input.AttachmentID); err != nil {
		return nil, err
	}
	return map[string]any{"path": input.Destination, "sha256": item.SHA256, "size": item.Size}, nil
}

func actionString(raw json.RawMessage, name string) string {
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	var value string
	_ = json.Unmarshal(values[name], &value)
	return value
}

func (d *aiDomain) writeFile(raw json.RawMessage) (any, error) {
	var input struct {
		Path           string `json:"path"`
		Content        string `json:"content"`
		ExpectedSHA256 string `json:"expected_sha256"`
		Replace        bool   `json:"replace"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	if err := diskspace.Require(d.app.managedRoot, diskspace.MinimumWritableBytes); err != nil {
		return nil, err
	}
	if d.app.runs.ConflictsPath(input.Path) {
		return nil, errors.New("an active Run holds a Run Lease for this path")
	}
	stored, _ := randomToken(18)
	if _, err := d.app.managed.Info(input.Path); err == nil {
		if !input.Replace {
			return nil, errors.New("target exists and replace is false")
		}
		trashed, err := d.app.managed.SaveText(input.Path, input.ExpectedSHA256, input.Content, stored, 1<<20)
		if err != nil {
			return nil, err
		}
		if err := d.recordTrash(trashed); err != nil {
			_ = d.app.managed.RollbackTextSave(input.Path, stored)
			return nil, err
		}
	} else {
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(input.Path)))
		if parent == "." {
			parent = ""
		}
		name := filepath.Base(filepath.FromSlash(input.Path))
		if _, err := d.app.managed.Upload(parent, name, strings.NewReader(input.Content), 1<<20, false, stored); err != nil {
			return nil, err
		}
	}
	if err := d.app.checkpointWebMutation("ai-write", input.Path); err != nil {
		return nil, err
	}
	document, _ := d.app.managed.ReadText(input.Path, 1<<20)
	return map[string]any{"path": input.Path, "sha256": document.Digest}, nil
}

type patchInput struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Replacements   []struct {
		Old string `json:"old"`
		New string `json:"new"`
		All bool   `json:"all"`
	} `json:"replacements"`
}

func (d *aiDomain) patchContent(raw json.RawMessage) (patchInput, string, error) {
	var input patchInput
	if err := decodeAI(raw, &input); err != nil {
		return input, "", err
	}
	document, err := d.app.managed.ReadText(input.Path, 1<<20)
	if err != nil {
		return input, "", err
	}
	if document.Digest != input.ExpectedSHA256 {
		return input, "", errors.New("file digest changed")
	}
	content := document.Content
	for _, replacement := range input.Replacements {
		if replacement.Old == "" {
			return input, "", errors.New("patch old text cannot be empty")
		}
		count := strings.Count(content, replacement.Old)
		if count == 0 {
			return input, "", errors.New("patch old text was not found")
		}
		if !replacement.All && count != 1 {
			return input, "", fmt.Errorf("patch old text matched %d times", count)
		}
		limit := 1
		if replacement.All {
			limit = -1
		}
		content = strings.Replace(content, replacement.Old, replacement.New, limit)
	}
	return input, content, nil
}

func (d *aiDomain) applyPatch(raw json.RawMessage) (any, error) {
	input, content, err := d.patchContent(raw)
	if err != nil {
		return nil, err
	}
	value, _ := json.Marshal(map[string]any{"path": input.Path, "content": content, "replace": true, "expected_sha256": input.ExpectedSHA256})
	return d.writeFile(value)
}

func (d *aiDomain) deleteFile(path string) (any, error) {
	if d.app.runs.ConflictsPath(path) {
		return nil, errors.New("an active Run holds a Run Lease for this path")
	}
	id, _ := randomToken(18)
	trashed, err := d.app.managed.MoveToTrash(path, id)
	if err != nil {
		return nil, err
	}
	if err := d.recordTrash(trashed); err != nil {
		_ = d.app.managed.RestoreFromTrash(trashed.StoredName, trashed.OriginalPath)
		return nil, err
	}
	if err := d.app.checkpointWebMutation("ai-delete", path); err != nil {
		return nil, err
	}
	return map[string]any{"trash_id": id, "original_path": path}, nil
}

func (d *aiDomain) recordTrash(item managedfiles.Trashed) error {
	_, err := d.app.db.Exec(`INSERT INTO trash_entries(id,original_path,stored_name,deleted_at,size,is_directory)
		VALUES(?,?,?,?,?,?)`, item.StoredName, item.OriginalPath, item.StoredName, time.Now().UTC().Unix(), item.Size, item.Directory)
	return err
}

func (d *aiDomain) restoreTrash(id string) (any, error) {
	var original, stored string
	if err := d.app.db.QueryRow("SELECT original_path,stored_name FROM trash_entries WHERE id=?", id).Scan(&original, &stored); err != nil {
		return nil, err
	}
	if err := d.app.managed.RestoreFromTrash(stored, original); err != nil {
		return nil, err
	}
	if _, err := d.app.db.Exec("DELETE FROM trash_entries WHERE id=?", id); err != nil {
		return nil, err
	}
	if err := d.app.checkpointWebMutation("ai-restore", original); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "path": original}, nil
}

func (d *aiDomain) purgeTrash(id string) (any, error) {
	var stored string
	if err := d.app.db.QueryRow("SELECT stored_name FROM trash_entries WHERE id=?", id).Scan(&stored); err != nil {
		return nil, err
	}
	if err := d.app.managed.PurgeTrash(stored); err != nil {
		return nil, err
	}
	_, err := d.app.db.Exec("DELETE FROM trash_entries WHERE id=?", id)
	return map[string]any{"id": id, "purged": err == nil}, err
}

func (d *aiDomain) createSchedule(raw json.RawMessage) (any, error) {
	var input struct {
		Name           string `json:"name"`
		ScriptPath     string `json:"script_path"`
		Arguments      string `json:"arguments"`
		Expression     string `json:"expression"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		AllowOverlap   bool   `json:"allow_overlap"`
		Enabled        bool   `json:"enabled"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	id, err := d.app.scheduler.Create(scheduler.CreateRequest{
		Name: input.Name, ScriptPath: input.ScriptPath, ArgumentsTemplate: input.Arguments,
		Expression: input.Expression, TimeoutSeconds: input.TimeoutSeconds, AllowOverlap: input.AllowOverlap,
	})
	if err == nil && !input.Enabled {
		err = d.app.scheduler.SetEnabled(id, false)
	}
	var next []time.Time
	if err == nil {
		for _, item := range mustSchedules(d.app.scheduler.List()) {
			if item.ID == id {
				next = item.NextFive
			}
		}
	}
	return map[string]any{"id": id, "enabled": input.Enabled, "timezone": time.Now().Location().String(), "next_five": next}, err
}

func mustSchedules(values []scheduler.Schedule, err error) []scheduler.Schedule {
	if err != nil {
		return nil
	}
	return values
}

func (d *aiDomain) updateSchedule(raw json.RawMessage) (any, error) {
	var input struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		ScriptPath     string `json:"script_path"`
		Arguments      string `json:"arguments"`
		Expression     string `json:"expression"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		AllowOverlap   bool   `json:"allow_overlap"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	err := d.app.scheduler.Update(input.ID, scheduler.CreateRequest{
		Name: input.Name, ScriptPath: input.ScriptPath, ArgumentsTemplate: input.Arguments,
		Expression: input.Expression, TimeoutSeconds: input.TimeoutSeconds, AllowOverlap: input.AllowOverlap,
	})
	return map[string]any{"id": input.ID}, err
}

func (d *aiDomain) upsertVariable(raw json.RawMessage) (any, error) {
	var input struct {
		Name, Value string
		Password    bool
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	var count int
	if err := d.app.db.QueryRow("SELECT COUNT(*) FROM variables").Scan(&count); err != nil {
		return nil, err
	}
	var exists bool
	_ = d.app.db.QueryRow("SELECT EXISTS(SELECT 1 FROM variables WHERE name=?)", input.Name).Scan(&exists)
	if !exists && count >= 1000 {
		return nil, errors.New("Variable limit reached")
	}
	now := time.Now().UTC().Unix()
	_, err := d.app.db.Exec(`INSERT INTO variables(name,value,is_password,created_at,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET value=excluded.value,is_password=excluded.is_password,updated_at=excluded.updated_at`,
		input.Name, input.Value, input.Password, now, now)
	return map[string]any{"name": input.Name, "password": input.Password, "value_length": len(input.Value)}, err
}

func (d *aiDomain) deleteVariable(name string) (any, error) {
	var count int
	pattern := "%${" + name + "}%"
	if err := d.app.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM schedules WHERE deleted=0 AND arguments_template LIKE ?)+
		(SELECT COUNT(*) FROM quick_runs WHERE arguments_template LIKE ?)`, pattern, pattern).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("Variable is referenced by a Schedule or Quick Run")
	}
	result, err := d.app.db.Exec("DELETE FROM variables WHERE name=?", name)
	if err == nil {
		if affected, _ := result.RowsAffected(); affected == 0 {
			err = sql.ErrNoRows
		}
	}
	return map[string]any{"name": name}, err
}

func (d *aiDomain) createQuickRun(raw json.RawMessage) (any, error) {
	var input struct {
		Name        string
		SourceRunID string `json:"source_run_id"`
	}
	if err := decodeAI(raw, &input); err != nil {
		return nil, err
	}
	if input.Name == "" || len([]byte(input.Name)) > 256 {
		return nil, errors.New("invalid Quick Run name")
	}
	source, err := d.app.runs.Get(input.SourceRunID)
	if err != nil {
		return nil, err
	}
	id, _ := randomToken(18)
	var order int
	_ = d.app.db.QueryRow("SELECT COALESCE(MAX(sort_order),0)+1 FROM quick_runs").Scan(&order)
	_, err = d.app.db.Exec(`INSERT INTO quick_runs(id,name,script_path,arguments_template,timeout_seconds,source_run_id,sort_order,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, id, input.Name, source.ScriptPath, source.ArgumentsTemplate, source.TimeoutSeconds, source.ID, order, time.Now().UTC().Unix())
	return map[string]any{"id": id, "source_run_id": source.ID}, err
}

func mapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
