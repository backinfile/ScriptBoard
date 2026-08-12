package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"scriptboard/internal/assistant/toolbroker"
	"scriptboard/internal/buildinfo"
)

const assistantUIActionRevisionQuery = `SELECT COALESCE(MAX(id), 0) FROM audit_events WHERE action NOT LIKE 'assistant_tool_%'`

type assistantUIActionParameters struct {
	Action         string            `json:"action"`
	PathParameters map[string]string `json:"pathParameters,omitempty"`
	Form           map[string]any    `json:"form,omitempty"`
}

type assistantUIActionListParameters struct {
	Domain string `json:"domain,omitempty"`
}

type assistantUIActionSpec struct {
	Key, Label, Domain, Path, DeepLink  string
	PathFields, QueryFields, FormFields []string
	FormValueModes                      map[string]assistantUIActionValueMode
	FormAllowedValues                   map[string][]string
	FormFieldGuidance                   map[string]string
	Permission                          permission
	Handler                             http.HandlerFunc
	BrowserOnly                         string
	UnavailableReason                   string
}

type assistantUIActionValueMode string

const (
	assistantUIActionBinary       assistantUIActionValueMode = "boolean-1-or-0"
	assistantUIActionPresence     assistantUIActionValueMode = "checkbox-presence"
	assistantUIActionConfirmation assistantUIActionValueMode = "confirmation-yes-or-no"
	assistantResourceIDHeader                                = "X-ScriptBoard-Resource-ID"
)

func (a *App) assistantUIActions() []assistantUIActionSpec {
	action := func(key, label, domain, path, deepLink string, pathFields, formFields []string, handler http.HandlerFunc) assistantUIActionSpec {
		spec := assistantUIActionSpec{Key: key, Label: label, Domain: domain, Path: path, DeepLink: deepLink, PathFields: pathFields, FormFields: formFields, Permission: a.declaredRoutePermission(http.MethodPost, path), Handler: handler}
		if a.declaredRouteStepUp(http.MethodPost, path) {
			spec.BrowserOnly = "Recent authentication is required in the protected browser session."
			spec.Handler = nil
		}
		switch key {
		case "websites.create", "websites.update":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"verify_tls": assistantUIActionBinary, "follow_redirects": assistantUIActionBinary}
			spec.FormAllowedValues = map[string][]string{
				"frequency_seconds": {"30", "60", "300", "900"},
				"timeout_seconds":   {"3", "5", "10", "30"},
			}
			spec.FormFieldGuidance = map[string]string{
				"frequency_seconds": "Required check interval; use one of the published allowed values.",
				"timeout_seconds":   "Required request timeout; use one of the published allowed values.",
			}
		case "ai.save_defaults":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"enabled": assistantUIActionPresence, "default_auto_approval": assistantUIActionPresence}
		case "quick_runs.lock", "schedules.toggle":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"locked": assistantUIActionBinary, "enabled": assistantUIActionBinary}
		case "schedules.create", "schedules.update":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"disallow_overlap": assistantUIActionPresence}
		case "files.delete":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"confirm_references": assistantUIActionConfirmation}
		case "runs.start_file":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"confirm_overlap": assistantUIActionConfirmation}
		case "websites.delete", "updates.apply", "files.purge_trash", "variables.delete", "quick_run_groups.delete", "schedule_groups.delete", "quick_runs.delete", "schedules.delete":
			spec.FormValueModes = map[string]assistantUIActionValueMode{"confirm": assistantUIActionConfirmation}
		}
		if key == "files.toggle_executable" && runtime.GOOS != "linux" {
			spec.UnavailableReason = "Owner execute mode is only supported on Linux hosts."
		}
		if !buildinfo.Current().ValidRelease() {
			switch key {
			case "ai.runtime_check", "ai.runtime_install":
				spec.UnavailableReason = "Online Runtime release operations are unavailable in development builds."
			case "updates.check", "updates.prepare", "updates.apply":
				spec.UnavailableReason = "Application update operations are unavailable in development builds."
			}
		}
		return spec
	}
	queryAction := func(key, label, domain, path, deepLink string, pathFields, queryFields, formFields []string, handler http.HandlerFunc) assistantUIActionSpec {
		spec := action(key, label, domain, path, deepLink, pathFields, formFields, handler)
		spec.QueryFields = queryFields
		return spec
	}
	browserOnly := func(key, label, domain, path, reason string) assistantUIActionSpec {
		return assistantUIActionSpec{Key: key, Label: label, Domain: domain, Path: path, Permission: a.declaredRoutePermission(http.MethodPost, path), BrowserOnly: reason}
	}
	return []assistantUIActionSpec{
		action("applications.pin", "Pin application", "applications", "/monitor/applications/{id}/pin", "/monitor/applications", []string{"id"}, nil, a.pinApplication),
		action("applications.unpin", "Unpin application", "applications", "/monitor/applications/{id}/unpin", "/monitor/applications", []string{"id"}, nil, a.unpinApplication),
		action("applications.move", "Move pinned application", "applications", "/monitor/applications/{id}/move", "/monitor/applications", []string{"id"}, []string{"direction"}, a.movePinnedApplication),

		action("websites.create", "Create website monitor", "websites", "/monitor/websites", "/monitor/websites", nil, []string{"name", "scope", "kind", "url", "verify_tls", "request_headers", "http_method", "http_content_type", "http_body", "http_success_mode", "response_keyword", "follow_redirects", "websocket_success", "ping_payload_format", "ping_payload", "send_type", "send_payload", "receive_type", "expected_message", "frequency_seconds", "timeout_seconds", "expected_statuses"}, a.createWebsiteMonitor),
		action("websites.reorder", "Reorder website monitors", "websites", "/monitor/websites/reorder", "/monitor/websites", nil, []string{"id"}, a.reorderWebsiteMonitors),
		action("websites.nginx_scan", "Scan Nginx configuration", "websites", "/monitor/websites/nginx/scan", "/monitor/websites/nginx", nil, []string{"config_path"}, a.scanWebsiteMonitorNginx),
		action("websites.nginx_import", "Import Nginx websites", "websites", "/monitor/websites/nginx/import", "/monitor/websites/nginx", nil, []string{"config_path", "digest"}, a.importWebsiteMonitorNginx),
		action("websites.update", "Update website monitor", "websites", "/monitor/websites/{id}", "/monitor/websites", []string{"id"}, []string{"name", "scope", "kind", "url", "verify_tls", "request_headers", "http_method", "http_content_type", "http_body", "http_success_mode", "response_keyword", "follow_redirects", "websocket_success", "ping_payload_format", "ping_payload", "send_type", "send_payload", "receive_type", "expected_message", "frequency_seconds", "timeout_seconds", "expected_statuses"}, a.updateWebsiteMonitor),
		action("websites.check", "Check website now", "websites", "/monitor/websites/{id}/check", "/monitor/websites", []string{"id"}, nil, a.checkWebsiteMonitorNow),
		action("websites.pause", "Pause website monitor", "websites", "/monitor/websites/{id}/pause", "/monitor/websites", []string{"id"}, nil, a.pauseWebsiteMonitor),
		action("websites.resume", "Resume website monitor", "websites", "/monitor/websites/{id}/resume", "/monitor/websites", []string{"id"}, nil, a.resumeWebsiteMonitor),
		action("websites.move", "Move website monitor", "websites", "/monitor/websites/{id}/move", "/monitor/websites", []string{"id"}, []string{"direction"}, a.moveWebsiteMonitor),
		action("websites.delete", "Delete website monitor", "websites", "/monitor/websites/{id}/delete", "/monitor/websites", []string{"id"}, []string{"confirm"}, a.deleteWebsiteMonitor),

		browserOnly("users.create", "Create user", "users", "/settings/users", "The generated credential is only shown in the protected browser task."),
		action("users.disable", "Disable user", "users", "/settings/users/{id}/disable", "/settings/users", []string{"id"}, nil, a.disableUser),
		action("users.enable", "Enable user", "users", "/settings/users/{id}/enable", "/settings/users", []string{"id"}, nil, a.enableUser),
		action("users.update", "Update user", "users", "/settings/users/{id}/update", "/settings/users", []string{"id"}, []string{"username", "role"}, a.updateUser),
		browserOnly("users.reset_password", "Reset user password", "users", "/settings/users/{id}/reset-password", "The generated credential is only shown in the protected browser task."),
		browserOnly("account.change_credentials", "Change account credentials", "account", "/settings/account", "Passwords and session identity never pass through Pi."),
		browserOnly("account.rename", "Change account username", "account", "/settings/account/username", "Session identity changes remain browser-only."),
		browserOnly("account.change_password", "Change account password", "account", "/settings/account/password", "Passwords never pass through Pi."),

		browserOnly("ai.save_llm", "Create or update LLM configuration", "ai", "/settings/ai/llms", "Provider credentials never pass through Pi tools."),
		action("ai.test_llm", "Test LLM configuration", "ai", "/settings/ai/llms/{id}/test", "/settings/ai", []string{"id"}, nil, a.testAssistantModel),
		action("ai.default_llm", "Set default LLM", "ai", "/settings/ai/llms/{id}/default", "/settings/ai", []string{"id"}, nil, a.setDefaultAssistantModel),
		action("ai.delete_llm", "Delete LLM configuration", "ai", "/settings/ai/llms/{id}/delete", "/settings/ai", []string{"id"}, nil, a.deleteAssistantModel),
		action("ai.save_defaults", "Save AI defaults", "ai", "/settings/ai/defaults", "/settings/ai", nil, []string{"enabled", "max_active_conversations", "default_auto_approval"}, a.saveAssistantDefaults),
		action("ai.runtime_check", "Check Pi Runtime", "ai", "/settings/ai/runtime/check", "/settings/ai", nil, nil, a.checkAssistantRuntime),
		action("ai.runtime_install", "Install Pi Runtime", "ai", "/settings/ai/runtime/install", "/settings/ai", nil, nil, a.installAssistantRuntime),
		action("ai.runtime_rollback", "Roll back Pi Runtime", "ai", "/settings/ai/runtime/rollback", "/settings/ai", nil, nil, a.rollbackAssistantRuntime),

		action("updates.check", "Check application updates", "updates", "/settings/updates/check", "/settings/updates", nil, nil, a.checkUpdate),
		action("updates.prepare", "Prepare application update", "updates", "/settings/updates/prepare", "/settings/updates", nil, nil, a.prepareUpdate),
		action("updates.apply", "Apply application update", "updates", "/settings/updates/apply", "/settings/updates", nil, []string{"operation_id", "confirm"}, a.applyUpdate),

		action("files.mkdir", "Create directory", "files", "/resources/files/mkdir", "/resources/files", nil, []string{"path", "name"}, a.createDirectory),
		browserOnly("files.upload", "Upload files", "files", "/resources/files/upload", "Binary and multipart uploads remain browser-only."),
		browserOnly("files.resolve_upload_conflicts", "Resolve upload conflicts", "files", "/resources/files/conflicts", "Multipart upload state remains browser-only."),
		action("files.delete", "Delete file or directory", "files", "/resources/files/delete", "/resources/files", nil, []string{"path", "confirm_references"}, a.deleteFile),
		action("files.move", "Move file or directory", "files", "/resources/files/move", "/resources/files", nil, []string{"working_directory", "name", "source", "destination", "new_name", "conflict_action"}, a.moveFile),
		action("files.toggle_executable", "Toggle executable mode", "files", "/resources/files/toggle-executable", "/resources/files", nil, []string{"path"}, a.toggleExecutable),
		action("files.cancel_operation", "Cancel file operation", "files", "/resources/files/operations/{id}/cancel", "/resources/files", []string{"id"}, nil, a.cancelFileOperation),
		action("files.restore_trash", "Restore trash entries", "files", "/resources/trash/restore", "/resources/trash", nil, []string{"id", "conflict_action", "new_name"}, a.restoreTrash),
		action("files.purge_trash", "Purge trash entries", "files", "/resources/trash/purge", "/resources/trash", nil, []string{"id", "confirm"}, a.purgeTrash),
		queryAction("files.save_text", "Save managed text", "files", "/resources/files/edit", "/resources/files", nil, []string{"path"}, []string{"content", "digest"}, a.saveText),
		action("runs.start_file", "Start a managed file", "runs", "/history/runs/start", "/history/runs", nil, []string{"script", "arguments", "timeout_seconds", "confirm_overlap"}, a.startRun),
		action("runs.stop", "Stop Run", "runs", "/history/runs/{id}/stop", "/history/runs", []string{"id"}, nil, a.stopRun),
		browserOnly("variables.create", "Create variable", "variables", "/resources/variables", "Variable values may contain credentials and remain browser-only."),
		browserOnly("variables.update", "Update variable", "variables", "/resources/variables/{name}/update", "Variable values may contain credentials and remain browser-only."),
		action("variables.delete", "Delete variable", "variables", "/resources/variables/{name}/delete", "/resources/variables", []string{"name"}, []string{"confirm"}, a.deleteVariable),

		action("quick_runs.save_from_run", "Save Run as Quick Run", "quick_runs", "/history/runs/{id}/quick-run", "/config/quick-runs", []string{"id"}, []string{"name", "group_id"}, a.saveQuickRun),
		action("quick_runs.create_from_file", "Create Quick Run from file", "quick_runs", "/config/quick-runs", "/config/quick-runs", nil, []string{"script", "name", "arguments", "timeout_seconds", "group_id", "return_to"}, a.createQuickRunFromFile),
		action("quick_runs.one_time", "Start one-time source Run", "quick_runs", "/config/quick-runs/one-time", "/config/quick-runs", nil, []string{"language", "source", "working_directory", "arguments", "timeout_seconds"}, a.startOneTimeRun),
		action("quick_runs.create_from_source", "Create Quick Run from source", "quick_runs", "/config/quick-runs/from-source", "/config/quick-runs", nil, []string{"language", "source", "working_directory", "file_name", "name", "timeout_seconds", "arguments", "group_id", "conflict_action", "rename_file_name"}, a.createQuickRunFromSource),
		action("quick_run_groups.create", "Create Quick Run group", "quick_runs", "/config/quick-runs/groups", "/config/quick-runs", nil, []string{"name"}, a.createQuickRunGroup),
		action("quick_run_groups.update", "Update Quick Run group", "quick_runs", "/config/quick-runs/groups/{id}/update", "/config/quick-runs", []string{"id"}, []string{"name"}, a.updateQuickRunGroup),
		action("quick_run_groups.move", "Move Quick Run group", "quick_runs", "/config/quick-runs/groups/{id}/move", "/config/quick-runs", []string{"id"}, []string{"direction"}, a.moveQuickRunGroup),
		action("quick_run_groups.delete", "Delete Quick Run group", "quick_runs", "/config/quick-runs/groups/{id}/delete", "/config/quick-runs", []string{"id"}, []string{"confirm"}, a.deleteQuickRunGroup),
		action("quick_runs.move_group", "Move Quick Run to group", "quick_runs", "/config/quick-runs/{id}/move-group", "/config/quick-runs", []string{"id"}, []string{"group_id"}, a.moveQuickRunToGroup),
		action("quick_runs.update", "Update Quick Run", "quick_runs", "/config/quick-runs/{id}/update", "/config/quick-runs", []string{"id"}, []string{"name", "arguments", "timeout_seconds"}, a.updateQuickRun),
		action("quick_runs.copy", "Copy Quick Run", "quick_runs", "/config/quick-runs/{id}/copy", "/config/quick-runs", []string{"id"}, []string{"name", "arguments", "timeout_seconds", "group_id"}, a.copyQuickRun),
		action("quick_runs.lock", "Set Quick Run lock", "quick_runs", "/config/quick-runs/{id}/lock", "/config/quick-runs", []string{"id"}, []string{"locked"}, a.setQuickRunLocked),
		action("quick_runs.start", "Start Quick Run", "quick_runs", "/config/quick-runs/{id}/start", "/config/quick-runs", []string{"id"}, nil, a.startQuickRun),
		action("quick_runs.move", "Move Quick Run", "quick_runs", "/config/quick-runs/{id}/move", "/config/quick-runs", []string{"id"}, []string{"direction"}, a.moveQuickRun),
		action("quick_runs.delete", "Delete Quick Run", "quick_runs", "/config/quick-runs/{id}/delete", "/config/quick-runs", []string{"id"}, []string{"confirm"}, a.deleteQuickRun),

		action("schedule_groups.create", "Create schedule group", "schedules", "/config/schedules/groups", "/config/schedules", nil, []string{"name"}, a.createScheduleGroup),
		action("schedule_groups.update", "Update schedule group", "schedules", "/config/schedules/groups/{id}/update", "/config/schedules", []string{"id"}, []string{"name"}, a.updateScheduleGroup),
		action("schedule_groups.move", "Move schedule group", "schedules", "/config/schedules/groups/{id}/move", "/config/schedules", []string{"id"}, []string{"direction"}, a.moveScheduleGroup),
		action("schedule_groups.delete", "Delete schedule group", "schedules", "/config/schedules/groups/{id}/delete", "/config/schedules", []string{"id"}, []string{"confirm"}, a.deleteScheduleGroup),
		action("schedules.preview", "Preview schedule expression", "schedules", "/config/schedules/preview", "/config/schedules", nil, []string{"expression"}, a.previewScheduleCron),
		action("schedules.preview_existing", "Preview schedule expression while editing", "schedules", "/config/schedules/{id}/preview", "/config/schedules", []string{"id"}, []string{"expression"}, a.previewScheduleCron),
		action("schedules.create", "Create schedule", "schedules", "/config/schedules", "/config/schedules", nil, []string{"name", "script", "arguments", "expression", "timeout_seconds", "disallow_overlap", "group_id"}, a.createSchedule),
		action("schedules.update", "Update schedule", "schedules", "/config/schedules/{id}/update", "/config/schedules", []string{"id"}, []string{"name", "script", "arguments", "expression", "timeout_seconds", "disallow_overlap", "group_id"}, a.updateSchedule),
		action("schedules.toggle", "Toggle schedule", "schedules", "/config/schedules/{id}/toggle", "/config/schedules", []string{"id"}, []string{"enabled"}, a.toggleSchedule),
		action("schedules.run", "Run schedule now", "schedules", "/config/schedules/{id}/run", "/config/schedules", []string{"id"}, nil, a.runScheduleNow),
		action("schedules.delete", "Delete schedule", "schedules", "/config/schedules/{id}/delete", "/config/schedules", []string{"id"}, []string{"confirm"}, a.deleteSchedule),

		browserOnly("session.login", "Log in", "session", "/login", "Authentication remains browser-only."),
		browserOnly("session.locale", "Change language", "session", "/settings/locale", "Session presentation remains browser-only."),
		browserOnly("session.logout", "Log out", "session", "/logout", "Session termination remains browser-only."),
		browserOnly("ai.conversation.create", "Create AI conversation", "ai", "/ai/conversations", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.message", "Send AI conversation message", "ai", "/ai/conversations/{id}/messages", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.abort", "Abort AI response", "ai", "/ai/conversations/{id}/abort", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.model", "Select AI conversation model", "ai", "/ai/conversations/{id}/model", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.approval_mode", "Set AI conversation approval mode", "ai", "/ai/conversations/{id}/approval-mode", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.resolve_approval", "Resolve AI tool approval", "ai", "/ai/conversations/{id}/approvals/{approval_id}", "Pi cannot recursively approve its own tool request."),
		browserOnly("ai.conversation.archive", "Archive AI conversation", "ai", "/ai/conversations/{id}/archive", "Pi cannot recursively operate its own conversation session."),
		browserOnly("ai.conversation.restore", "Restore AI conversation", "ai", "/ai/conversations/{id}/restore", "Pi cannot recursively operate its own conversation session."),
	}
}

func (executor *assistantToolExecutor) planListUIActions(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantUIActionListParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil || len(parameters.Domain) > 48 {
		return assistantToolPlan{}, errAssistantToolParameters
	}
	parameters.Domain = strings.TrimSpace(parameters.Domain)
	if strings.EqualFold(parameters.Domain, "scriptboard") || strings.EqualFold(parameters.Domain, "all") || strings.EqualFold(parameters.Domain, "scriptboard/all") {
		parameters.Domain = ""
	}
	items := make([]map[string]any, 0)
	for _, spec := range executor.app.assistantUIActions() {
		if parameters.Domain != "" && spec.Domain != parameters.Domain || !roleAllows(authorization.Role, spec.Permission) {
			continue
		}
		item := map[string]any{
			"action": spec.Key, "label": spec.Label, "domain": spec.Domain, "pathTemplate": spec.Path,
			"pathParameters": spec.PathFields, "queryParameters": spec.QueryFields, "formFields": spec.FormFields,
			"requiresApproval": spec.Handler != nil && spec.BrowserOnly == "" && spec.UnavailableReason == "",
			"available":        spec.Handler != nil && spec.BrowserOnly == "" && spec.UnavailableReason == "", "deepLink": spec.DeepLink,
		}
		if len(spec.FormValueModes) > 0 {
			item["formValueModes"] = spec.FormValueModes
		}
		if len(spec.FormAllowedValues) > 0 {
			item["formAllowedValues"] = spec.FormAllowedValues
		}
		if len(spec.FormFieldGuidance) > 0 {
			item["formFieldGuidance"] = spec.FormFieldGuidance
		}
		if spec.Key == "quick_runs.one_time" || spec.Key == "quick_runs.create_from_source" {
			item["formDefaults"] = map[string]string{"working_directory": executor.app.defaultHostDirectory(ctx)}
			guidance := map[string]string{
				"working_directory": "Optional absolute, writable, unprotected host directory. The same server-selected default as the web form is used when omitted.",
			}
			for key, value := range spec.FormFieldGuidance {
				guidance[key] = value
			}
			item["formFieldGuidance"] = guidance
		}
		if spec.BrowserOnly != "" {
			item["browserOnlyReason"] = spec.BrowserOnly
		}
		if spec.UnavailableReason != "" {
			item["unavailableReason"] = spec.UnavailableReason
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["action"].(string) < items[j]["action"].(string) })
	return assistantToolPlan{
		targetSummary: "ui-actions current", parameterSummary: "role-filtered action contracts", normalized: parameters, deepLink: "/monitor",
		execute: func(context.Context) (any, string, bool, error) {
			return map[string]any{"source": "ScriptBoard web action catalog", "actions": items}, fmt.Sprintf("Listed %d available and browser-only UI actions.", len(items)), false, nil
		},
	}, nil
}

func (executor *assistantToolExecutor) planPerformUIAction(ctx context.Context, authorization assistantToolAuthorization, invocation toolbroker.Invocation) (assistantToolPlan, error) {
	var parameters assistantUIActionParameters
	if err := decodeAssistantToolParameters(invocation.Request.Parameters, &parameters); err != nil {
		return assistantToolPlan{}, err
	}
	parameters.Action = strings.TrimSpace(parameters.Action)
	var selected *assistantUIActionSpec
	for _, candidate := range executor.app.assistantUIActions() {
		if candidate.Key == parameters.Action {
			copy := candidate
			selected = &copy
			break
		}
	}
	if selected == nil {
		return assistantToolPlan{}, errAssistantToolNotFound
	}
	if !roleAllows(authorization.Role, selected.Permission) || selected.BrowserOnly != "" || selected.Handler == nil {
		return assistantToolPlan{}, errAssistantToolForbidden
	}
	if selected.UnavailableReason != "" {
		return assistantToolPlan{}, fmt.Errorf("%w: %s", errAssistantToolUnavailable, selected.UnavailableReason)
	}
	if selected.Key == "quick_runs.one_time" || selected.Key == "quick_runs.create_from_source" {
		if parameters.Form == nil {
			parameters.Form = make(map[string]any)
		}
		workingDirectory, present := parameters.Form["working_directory"]
		if !present || strings.TrimSpace(fmt.Sprint(workingDirectory)) == "" {
			parameters.Form["working_directory"] = executor.app.defaultHostDirectory(ctx)
		}
	}
	path, form, err := normalizeAssistantUIAction(*selected, parameters)
	if err != nil {
		return assistantToolPlan{}, err
	}
	var revision int64
	if err := executor.app.db.QueryRowContext(ctx, assistantUIActionRevisionQuery).Scan(&revision); err != nil {
		return assistantToolPlan{}, err
	}
	parameters.PathParameters = normalizedStringMap(parameters.PathParameters)
	targetState := map[string]any{"auditRevision": revision}
	if selected.Key == "files.save_text" {
		relative, fileErr := executor.app.hostCanonicalExisting(ctx, parameters.PathParameters["path"])
		if fileErr != nil {
			return assistantToolPlan{}, errAssistantToolNotFound
		}
		document, fileErr := executor.app.hostReadText(ctx, relative, 1<<20)
		if fileErr != nil {
			return assistantToolPlan{}, fileErr
		}
		if strings.TrimSpace(form.Get("digest")) == "" {
			form.Set("digest", document.Digest)
			if parameters.Form == nil {
				parameters.Form = make(map[string]any)
			}
			parameters.Form["digest"] = document.Digest
		}
		targetState["fileDigest"] = document.Digest
	}
	return assistantToolPlan{
		targetSummary: selected.Key + " ui-action", parameterSummary: assistantUIActionParameterSummary(parameters),
		normalized: parameters, targetState: targetState, deepLink: selected.DeepLink,
		approvalTitle: "Run ScriptBoard action", approvalMessage: fmt.Sprintf("Run %q through the same validation and audit path as the web interface?", selected.Label),
		execute: func(ctx context.Context) (any, string, bool, error) {
			return executor.executeAssistantUIAction(ctx, authorization, *selected, path, form)
		},
	}, nil
}

func normalizeAssistantUIAction(spec assistantUIActionSpec, parameters assistantUIActionParameters) (string, url.Values, error) {
	if len(parameters.PathParameters) != len(spec.PathFields)+len(spec.QueryFields) || len(parameters.Form) > 32 {
		return "", nil, errAssistantToolParameters
	}
	path := spec.Path
	for _, field := range spec.PathFields {
		value, ok := parameters.PathParameters[field]
		if !ok || !validAssistantToolID(value) {
			return "", nil, errAssistantToolParameters
		}
		path = strings.ReplaceAll(path, "{"+field+"}", url.PathEscape(strings.TrimSpace(value)))
	}
	query := make(url.Values, len(spec.QueryFields))
	for _, field := range spec.QueryFields {
		value, ok := parameters.PathParameters[field]
		value = strings.TrimSpace(value)
		if !ok || value == "" || len(value) > 4096 {
			return "", nil, errAssistantToolParameters
		}
		query.Set(field, value)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	allowed := make(map[string]bool, len(spec.FormFields))
	for _, field := range spec.FormFields {
		allowed[field] = true
	}
	form := make(url.Values, len(parameters.Form)+1)
	for key, raw := range parameters.Form {
		if !allowed[key] || key == "csrf_token" {
			return "", nil, errAssistantToolParameters
		}
		values, err := assistantUIActionValues(raw)
		if err != nil {
			return "", nil, err
		}
		if mode := spec.FormValueModes[key]; mode != "" {
			if len(values) != 1 {
				return "", nil, errAssistantToolParameters
			}
			value, include, normalizeErr := normalizeAssistantUIActionValue(mode, values[0])
			if normalizeErr != nil {
				return "", nil, normalizeErr
			}
			if include {
				form.Set(key, value)
			}
			continue
		}
		for _, value := range values {
			if len(value) > 32768 {
				return "", nil, errAssistantToolParameters
			}
			form.Add(key, value)
		}
	}
	return path, form, nil
}

func normalizeAssistantUIActionValue(mode assistantUIActionValueMode, raw string) (string, bool, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	truthy := value == "1" || value == "true" || value == "yes" || value == "on"
	falsy := value == "0" || value == "false" || value == "no" || value == "off" || value == ""
	if !truthy && !falsy {
		return "", false, errAssistantToolParameters
	}
	switch mode {
	case assistantUIActionBinary:
		if truthy {
			return "1", true, nil
		}
		return "0", true, nil
	case assistantUIActionPresence:
		return "1", truthy, nil
	case assistantUIActionConfirmation:
		if truthy {
			return "yes", true, nil
		}
		return "no", true, nil
	default:
		return "", false, errAssistantToolParameters
	}
}

func assistantUIActionValues(raw any) ([]string, error) {
	switch value := raw.(type) {
	case string:
		return []string{value}, nil
	case bool:
		return []string{strconv.FormatBool(value)}, nil
	case float64:
		return []string{strconv.FormatFloat(value, 'f', -1, 64)}, nil
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			items, err := assistantUIActionValues(item)
			if err != nil || len(items) != 1 {
				return nil, errAssistantToolParameters
			}
			result = append(result, items[0])
		}
		return result, nil
	default:
		return nil, errAssistantToolParameters
	}
}

func normalizedStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = strings.TrimSpace(value)
	}
	return result
}

func assistantUIActionParameterSummary(parameters assistantUIActionParameters) string {
	return fmt.Sprintf("path %d, form %d fields", len(parameters.PathParameters), len(parameters.Form))
}

func (executor *assistantToolExecutor) executeAssistantUIAction(ctx context.Context, authorization assistantToolAuthorization, spec assistantUIActionSpec, path string, form url.Values) (any, string, bool, error) {
	form.Set("csrf_token", "assistant-ui-action")
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode())).WithContext(context.WithValue(ctx, sessionContextKey, session{
		userID: authorization.Actor.UserID, username: authorization.Actor.Username, role: authorization.Role,
		authVersion: authorization.AuthVersion, csrfToken: "assistant-ui-action",
	}))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.RemoteAddr = "assistant"
	for field, value := range extractAssistantPathParameters(spec, path) {
		request.SetPathValue(field, value)
	}
	recorder := httptest.NewRecorder()
	spec.Handler.ServeHTTP(recorder, request)
	status := recorder.Code
	if status < 200 || status >= 400 {
		return nil, "", false, assistantUIActionHTTPError{Action: spec.Key, Status: status, Detail: assistantUIActionResponseDetail(recorder)}
	}
	result := map[string]any{"accepted": true, "action": spec.Key, "statusCode": status, "location": recorder.Header().Get("Location")}
	if resourceID := strings.TrimSpace(recorder.Header().Get(assistantResourceIDHeader)); resourceID != "" {
		result["resourceId"] = resourceID
	}
	var payload map[string]any
	if recorder.Body.Len() > 0 && json.Unmarshal(recorder.Body.Bytes(), &payload) == nil {
		for key, value := range payload {
			if _, reserved := result[key]; !reserved {
				result[key] = value
			}
		}
	}
	return result, fmt.Sprintf("Completed %s.", spec.Label), false, nil
}

type assistantUIActionHTTPError struct {
	Action string
	Status int
	Detail string
}

func (err assistantUIActionHTTPError) Error() string {
	return fmt.Sprintf("UI action %s returned HTTP %d: %s", err.Action, err.Status, err.Detail)
}

func (err assistantUIActionHTTPError) toolResponse() (string, string) {
	code := "ui_action_failed"
	switch err.Status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code = "ui_action_invalid"
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "ui_action_forbidden"
	case http.StatusNotFound:
		code = "ui_action_not_found"
	case http.StatusConflict:
		code = "ui_action_conflict"
	case http.StatusTooManyRequests:
		code = "ui_action_rate_limited"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = "ui_action_unavailable"
	}
	summary := fmt.Sprintf("Action %s failed with HTTP %d.", err.Action, err.Status)
	if err.Detail != "" {
		summary = fmt.Sprintf("Action %s failed with HTTP %d: %s", err.Action, err.Status, err.Detail)
	}
	return code, summary
}

func assistantUIActionResponseDetail(recorder *httptest.ResponseRecorder) string {
	detail := strings.Join(strings.Fields(recorder.Body.String()), " ")
	if detail == "" {
		return ""
	}
	runes := []rune(detail)
	if len(runes) > 512 {
		detail = string(runes[:512]) + "..."
	}
	return detail
}

func extractAssistantPathParameters(spec assistantUIActionSpec, path string) map[string]string {
	result := make(map[string]string, len(spec.PathFields))
	templateParts, pathParts := strings.Split(strings.Trim(spec.Path, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	for index, part := range templateParts {
		if index >= len(pathParts) || !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		value, err := url.PathUnescape(pathParts[index])
		if err == nil {
			result[name] = value
		}
	}
	return result
}
