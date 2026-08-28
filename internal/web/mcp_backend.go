package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"scriptboard/internal/identity"
	"scriptboard/internal/mcpaccess"
	"scriptboard/internal/mcpcommand"
	"scriptboard/internal/mcpserver"
	"scriptboard/internal/runcontrol"
	"scriptboard/internal/runmanager"
)

func (a *App) HostStatus(context.Context) (any, error) {
	overview := a.hostStatus.Current()
	overview.Series = nil
	return overview, nil
}

func (a *App) ListQuickRuns(ctx context.Context, cursor string, limit int) (mcpserver.QuickRunPage, error) {
	if limit <= 0 {
		limit = 50
	}
	result := mcpserver.QuickRunPage{Items: []mcpserver.QuickRun{}}
	scanCursor := cursor
	const maximumScannedRows = 1000
	for scanned := 0; scanned < maximumScannedRows; {
		batchSize := min(64, maximumScannedRows-scanned)
		rows, err := a.db.QueryContext(ctx, `SELECT q.id,q.name,COALESCE(g.name,''),q.script_path,q.script_sha256,q.revision,q.timeout_seconds,EXISTS(SELECT 1 FROM runs r WHERE r.source_id=q.id AND r.status IN ('starting','running','stopping','timing_out')) FROM quick_runs q LEFT JOIN quick_run_groups g ON g.id=q.group_id WHERE q.id>? ORDER BY q.id LIMIT ?`, scanCursor, batchSize)
		if err != nil {
			return result, err
		}
		batchRows := 0
		for rows.Next() {
			batchRows++
			scanned++
			var item mcpserver.QuickRun
			var path, digest string
			if err := rows.Scan(&item.ID, &item.Name, &item.Group, &path, &digest, &item.Version, &item.TimeoutSeconds, &item.Running); err != nil {
				_ = rows.Close()
				return result, err
			}
			scanCursor = item.ID
			prepared, err := a.hostPrepareScript(ctx, path)
			if err != nil || digest == "" || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(digest)) != 1 {
				continue
			}
			result.Items = append(result.Items, item)
			if len(result.Items) > limit {
				result.NextCursor = result.Items[limit-1].ID
				result.Items = result.Items[:limit]
				_ = rows.Close()
				return result, nil
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return result, err
		}
		if err := rows.Close(); err != nil {
			return result, err
		}
		if batchRows < batchSize {
			return result, nil
		}
	}
	// 修复发布失效项在 SQL LIMIT 后过滤导致后续项不可达：达到扫描上限时按最后检查行继续。
	if scanCursor != cursor {
		result.NextCursor = scanCursor
	}
	return result, nil
}

func sanitizedRun(run runmanager.Run) map[string]any {
	return map[string]any{"run_id": run.ID, "status": run.Status, "source_type": run.SourceType, "source_name": run.SourceName, "source_id": run.SourceID, "created_at": run.CreatedAt, "started_at": run.StartedAt, "finished_at": run.FinishedAt, "exit_code": run.ExitCode, "error": run.Error, "timeout_seconds": run.TimeoutSeconds, "initiator_user_id": run.InitiatorUserID, "initiator_username": run.InitiatorUsername, "log_expired": run.LogExpired, "log_truncated": run.LogTruncated}
}
func (a *App) GetRun(_ context.Context, id string) (any, error) {
	run, err := a.runs.GetMetadata(strings.TrimSpace(id))
	if err != nil {
		return nil, errors.New("run not found")
	}
	return sanitizedRun(run), nil
}
func (a *App) GetRunLogs(_ context.Context, id, cursor string, limit int) (mcpserver.RunLogPage, error) {
	before := int64(0)
	if cursor != "" {
		parsed, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return mcpserver.RunLogPage{}, errors.New("invalid cursor")
		}
		before = parsed
	}
	page, err := a.runs.EventPage(id, before, limit)
	if err != nil {
		return mcpserver.RunLogPage{}, errors.New("run logs unavailable")
	}
	result := mcpserver.RunLogPage{Events: make([]mcpserver.RunLogEvent, 0, len(page.Events)), Truncated: page.HasMore}
	for _, event := range page.Events {
		result.Events = append(result.Events, mcpserver.RunLogEvent{Cursor: strconv.FormatInt(event.Sequence, 10), Time: event.Time, Source: event.Source, Text: event.Data})
	}
	if page.Before > 0 {
		result.NextCursor = strconv.FormatInt(page.Before, 10)
	}
	return result, nil
}

func (a *App) StartQuickRun(ctx context.Context, p mcpaccess.Principal, input mcpserver.StartQuickRunInput) (any, error) {
	const tool = "scriptboard.start_quick_run"
	return a.mcpCommands.Execute(ctx, mcpcommand.Key{UserID: p.UserID, ClientID: p.ClientID, Tool: tool, RequestID: input.RequestID}, func() (any, error) {
		started, err := a.runControl.Start(ctx, runcontrol.StartRequest{QuickRunID: input.QuickRunID, ConfirmOverlap: input.ConfirmOverlap, Actor: runcontrol.Actor{UserID: p.UserID, Username: p.Username, Role: identity.Role(p.Role)}})
		if err != nil {
			if errors.Is(err, runcontrol.ErrNotFound) {
				return nil, errors.New("forbidden")
			}
			return nil, err
		}
		result := any(started)
		if started.Conflict != "" {
			return mcpcommand.Uncached(result), nil
		}
		a.recordAuditWithActor("mcp_start_quick_run", input.QuickRunID, "accepted", "mcp:"+p.ClientID, p.UserID, p.Username, identity.Role(p.Role))
		_ = a.mcpStore.RecordInvocation(ctx, p, tool, input.QuickRunID, mcpaccess.ParameterDigest([]byte(fmt.Sprintf("%s:%t", input.QuickRunID, input.ConfirmOverlap))), "accepted", input.RequestID)
		return result, nil
	})
}

func (a *App) StopRun(ctx context.Context, p mcpaccess.Principal, input mcpserver.StopRunInput) (any, error) {
	const tool = "scriptboard.stop_run"
	return a.mcpCommands.Execute(ctx, mcpcommand.Key{UserID: p.UserID, ClientID: p.ClientID, Tool: tool, RequestID: input.RequestID}, func() (any, error) {
		run, err := a.runControl.Stop(ctx, runcontrol.Actor{UserID: p.UserID, Username: p.Username, Role: identity.Role(p.Role)}, input.RunID)
		if err != nil {
			return nil, errors.New("forbidden")
		}
		result := sanitizedRun(run)
		a.recordAuditWithActor("mcp_stop_run", input.RunID, "accepted", "mcp:"+p.ClientID, p.UserID, p.Username, identity.Role(p.Role))
		_ = a.mcpStore.RecordInvocation(ctx, p, tool, input.RunID, mcpaccess.ParameterDigest([]byte(input.RunID)), "accepted", input.RequestID)
		return result, nil
	})
}
