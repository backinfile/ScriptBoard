package web

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"scriptboard/internal/assistant/toolbroker"
)

func (executor *assistantToolExecutor) planSearchRunLog(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolEvidenceSearchParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	query, err := normalizeAssistantEvidenceQuery(parameters.Query)
	if err != nil {
		return assistantToolPlan{}, err
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	cursor, err := executor.evidenceCursor(invocation, "search_run_log", parameters.ID, query, parameters.Cursor)
	if err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Query, parameters.Limit = query, limit
	return assistantToolPlan{targetSummary: parameters.ID + " run-log", parameterSummary: fmt.Sprintf("literal query, limit %d", limit), normalized: parameters, deepLink: "/history/runs/" + url.PathEscape(parameters.ID), execute: func(context.Context) (any, string, bool, error) {
		run, err := executor.app.runs.Get(parameters.ID)
		if err != nil {
			return nil, "", false, err
		}
		entries := make([]any, 0, limit)
		bytesUsed, index := 0, cursor.Offset
		needle := strings.ToLower(query)
		for index < len(run.Events) && len(entries) < limit && bytesUsed < assistantToolTextBytes {
			event := run.Events[index]
			index++
			if !strings.Contains(strings.ToLower(event.Data), needle) {
				continue
			}
			text, truncated := assistantToolBoundText(event.Data, 40)
			if bytesUsed+len(text) > assistantToolTextBytes {
				index--
				break
			}
			bytesUsed += len(text)
			entries = append(entries, map[string]any{"sequence": event.Sequence, "time": assistantToolTime(event.Time), "source": event.Source, "text": text, "textTruncated": truncated, "encodingError": event.EncodingError})
		}
		next := ""
		if index < len(run.Events) {
			next = executor.nextEvidenceCursor(cursor, "", index)
		}
		content := map[string]any{
			"source": "ScriptBoard Run log", "untrustedData": true, "runId": run.ID, "query": query,
			"scope":   map[string]any{"eventCount": len(run.Events), "logExpired": run.LogExpired, "logIncomplete": run.LogIncomplete, "logTruncated": run.LogTruncated},
			"matches": entries, "matchCount": len(entries), "continuationCursor": next,
		}
		return content, fmt.Sprintf("Found %d bounded Run log matches.", len(entries)), next != "" || run.LogTruncated || run.LogIncomplete, nil
	}}, nil
}

func (executor *assistantToolExecutor) planReadRunLogWindow(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolEvidenceWindowParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) || parameters.Sequence < 0 {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 50)
	if err != nil {
		return assistantToolPlan{}, err
	}
	cursor, err := executor.evidenceCursor(invocation, "read_run_log_window", parameters.ID, fmt.Sprint(parameters.Sequence), parameters.Cursor)
	if err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Limit = limit
	return assistantToolPlan{targetSummary: parameters.ID + " run-log", parameterSummary: fmt.Sprintf("window limit %d", limit), normalized: parameters, deepLink: "/history/runs/" + url.PathEscape(parameters.ID), execute: func(context.Context) (any, string, bool, error) {
		run, err := executor.app.runs.Get(parameters.ID)
		if err != nil {
			return nil, "", false, err
		}
		index := cursor.Offset
		if parameters.Cursor == "" && parameters.Sequence > 0 {
			index = sort.Search(len(run.Events), func(index int) bool { return run.Events[index].Sequence >= parameters.Sequence })
		}
		end := index + limit
		if end > len(run.Events) {
			end = len(run.Events)
		}
		entries, bounded := compactAssistantRunEvents(run.Events[index:end], limit)
		next := ""
		if end < len(run.Events) {
			next = executor.nextEvidenceCursor(cursor, "", end)
		}
		return map[string]any{
			"source": "ScriptBoard Run log", "untrustedData": true, "runId": run.ID, "entries": entries,
			"scope":              map[string]any{"fromIndex": index, "toIndexExclusive": end, "eventCount": len(run.Events), "logExpired": run.LogExpired, "logIncomplete": run.LogIncomplete, "logTruncated": run.LogTruncated},
			"continuationCursor": next,
		}, fmt.Sprintf("Read %d bounded Run log events.", len(entries)), bounded || next != "" || run.LogTruncated || run.LogIncomplete, nil
	}}, nil
}

func (executor *assistantToolExecutor) planCompareRuns(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolCompareRunsParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || len(parameters.IDs) < 2 || len(parameters.IDs) > 5 {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	seen := map[string]bool{}
	for index, id := range parameters.IDs {
		id = strings.TrimSpace(id)
		if !validAssistantToolID(id) || seen[id] {
			return assistantToolPlan{}, errAssistantToolParameters
		}
		seen[id], parameters.IDs[index] = true, id
	}
	return assistantToolPlan{targetSummary: "Run comparison", parameterSummary: fmt.Sprintf("%d stable Run IDs", len(parameters.IDs)), normalized: parameters, deepLink: "/history/runs", execute: func(context.Context) (any, string, bool, error) {
		items := make([]any, 0, len(parameters.IDs))
		for _, id := range parameters.IDs {
			run, err := executor.app.runs.GetMetadata(id)
			if err != nil {
				return nil, "", false, err
			}
			item := compactAssistantRun(run)
			if run.StartedAt != nil && run.FinishedAt != nil {
				item["durationMilliseconds"] = run.FinishedAt.Sub(*run.StartedAt).Milliseconds()
			}
			item["evidence"] = map[string]any{"runId": run.ID, "eventRange": "metadata-only"}
			items = append(items, item)
		}
		return map[string]any{"source": "ScriptBoard Run Manager", "untrustedData": true, "runs": items, "comparisonScope": "bounded metadata; complete logs excluded"}, fmt.Sprintf("Compared %d Runs.", len(items)), false, nil
	}}, nil
}

func (executor *assistantToolExecutor) planSearchSourceLog(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolEvidenceSearchParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	query, err := normalizeAssistantEvidenceQuery(parameters.Query)
	if err != nil {
		return assistantToolPlan{}, err
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	cursor, err := executor.evidenceCursor(invocation, "search_source_log", parameters.ID, query, parameters.Cursor)
	if err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Query, parameters.Limit = query, limit
	return assistantToolPlan{targetSummary: parameters.ID + " source-log", parameterSummary: fmt.Sprintf("literal query, limit %d", limit), normalized: parameters, deepLink: "/monitor/applications/" + url.PathEscape(parameters.ID) + "/logs", execute: func(ctx context.Context) (any, string, bool, error) {
		source, err := executor.app.applicationStatus.LogSource(ctx, parameters.ID)
		if err != nil {
			return nil, "", false, err
		}
		page, err := source.History(ctx, cursor.Page)
		if err != nil {
			return nil, "", false, err
		}
		entries := make([]any, 0, limit)
		index, bytesUsed := cursor.Offset, 0
		needle := strings.ToLower(query)
		for index < len(page.Entries) && len(entries) < limit {
			entry := page.Entries[index]
			index++
			if !strings.Contains(strings.ToLower(entry.Text), needle) {
				continue
			}
			text, truncated := assistantToolBoundText(entry.Text, 40)
			if bytesUsed+len(text) > assistantToolTextBytes {
				index--
				break
			}
			bytesUsed += len(text)
			entries = append(entries, map[string]any{"time": assistantToolPointerTime(entry.Time), "source": entry.Source, "severity": entry.Severity, "text": text, "textTruncated": truncated, "continuation": entry.Continuation, "encodingError": entry.EncodingError})
		}
		next := ""
		if index < len(page.Entries) {
			next = executor.nextEvidenceCursor(cursor, cursor.Page, index)
		} else if page.HasMore {
			next = executor.nextEvidenceCursor(cursor, page.Before, 0)
		}
		return map[string]any{
			"source": "ScriptBoard application source log", "untrustedData": true, "applicationId": parameters.ID, "query": query,
			"sourceVersion": page.SourceVersion, "matches": entries, "matchCount": len(entries), "continuationCursor": next,
		}, fmt.Sprintf("Found %d bounded source-log matches.", len(entries)), next != "", nil
	}}, nil
}

func (executor *assistantToolExecutor) planScheduleHistory(invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolScheduleHistoryParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || !validAssistantToolID(parameters.ID) {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	cursor, err := executor.evidenceCursor(invocation, "get_schedule_history", parameters.ID, "", parameters.Cursor)
	if err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Limit = limit
	return assistantToolPlan{targetSummary: parameters.ID + " schedule", parameterSummary: fmt.Sprintf("trigger history limit %d", limit), normalized: parameters, deepLink: "/config/schedules/" + url.PathEscape(parameters.ID), execute: func(ctx context.Context) (any, string, bool, error) {
		var exists int
		if err := executor.app.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schedules WHERE id = ? AND deleted = 0)`, parameters.ID).Scan(&exists); err != nil || exists != 1 {
			if err == nil {
				err = errAssistantToolNotFound
			}
			return nil, "", false, err
		}
		rows, err := executor.app.db.QueryContext(ctx, `SELECT id, scheduled_for, result, run_id, error FROM schedule_triggers WHERE schedule_id = ? ORDER BY scheduled_for DESC LIMIT ? OFFSET ?`, parameters.ID, limit+1, cursor.Offset)
		if err != nil {
			return nil, "", false, err
		}
		defer rows.Close()
		items := make([]any, 0, limit+1)
		for rows.Next() {
			var id, result, runID, errorText string
			var scheduledFor int64
			if err := rows.Scan(&id, &scheduledFor, &result, &runID, &errorText); err != nil {
				return nil, "", false, err
			}
			errorText, _ = boundEvidenceText(errorText, 512)
			items = append(items, map[string]any{"id": id, "scheduledFor": time.Unix(0, scheduledFor).UTC().Format(time.RFC3339Nano), "result": result, "runId": runID, "errorSummary": errorText})
		}
		if err := rows.Err(); err != nil {
			return nil, "", false, err
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		next := ""
		if hasMore {
			next = executor.nextEvidenceCursor(cursor, "", cursor.Offset+len(items))
		}
		return map[string]any{"source": "ScriptBoard Scheduler", "untrustedData": true, "scheduleId": parameters.ID, "triggers": items, "continuationCursor": next}, fmt.Sprintf("Read %d schedule triggers.", len(items)), hasMore, nil
	}}, nil
}

func (executor *assistantToolExecutor) planAuditEvents(_ assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantToolAuditParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	query := strings.TrimSpace(parameters.Query)
	if query != "" {
		var err error
		query, err = normalizeAssistantEvidenceQuery(query)
		if err != nil {
			return assistantToolPlan{}, err
		}
	}
	limit, err := normalizeAssistantToolLimit(parameters.Limit, 20)
	if err != nil {
		return assistantToolPlan{}, err
	}
	cursor, err := executor.evidenceCursor(invocation, "list_audit_events", "audit", query, parameters.Cursor)
	if err != nil {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Query, parameters.Limit = query, limit
	return assistantToolPlan{targetSummary: "audit history", parameterSummary: fmt.Sprintf("bounded query, limit %d", limit), normalized: parameters, deepLink: "/history/audit", execute: func(ctx context.Context) (any, string, bool, error) {
		like := "%" + escapeAssistantEvidenceLike(query) + "%"
		rows, err := executor.app.db.QueryContext(ctx, `SELECT occurred_at, action, target, result, actor_username, actor_role FROM audit_events
			WHERE action NOT LIKE 'assistant\_tool\_%' ESCAPE '\' AND (? = '' OR action LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\' OR result LIKE ? ESCAPE '\' OR actor_username LIKE ? ESCAPE '\' OR actor_role LIKE ? ESCAPE '\')
			ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`, query, like, like, like, like, like, limit+1, cursor.Offset)
		if err != nil {
			return nil, "", false, err
		}
		defer rows.Close()
		items := make([]any, 0, limit+1)
		for rows.Next() {
			var occurredAt int64
			var action, target, result, actor, role string
			if err := rows.Scan(&occurredAt, &action, &target, &result, &actor, &role); err != nil {
				return nil, "", false, err
			}
			items = append(items, map[string]any{"occurredAt": time.Unix(occurredAt, 0).UTC().Format(time.RFC3339), "action": action, "target": target, "result": result, "actor": actor, "actorRole": role})
		}
		if err := rows.Err(); err != nil {
			return nil, "", false, err
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		next := ""
		if hasMore {
			next = executor.nextEvidenceCursor(cursor, "", cursor.Offset+len(items))
		}
		return map[string]any{"source": "ScriptBoard audit", "untrustedData": false, "query": query, "events": items, "continuationCursor": next, "assistantToolEventsExcluded": true}, fmt.Sprintf("Read %d audit events.", len(items)), hasMore, nil
	}}, nil
}

func boundEvidenceText(value string, maximum int) (string, bool) {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maximum {
		return value, false
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func escapeAssistantEvidenceLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
