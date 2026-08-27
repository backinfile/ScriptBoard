// Package runcontrol owns published Quick Run execution invariants shared by
// the browser UI and machine-facing protocol adapters.
package runcontrol

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/identity"
	"scriptboard/internal/runmanager"
)

var (
	ErrNotFound             = errors.New("quick run not found")
	ErrPublicationChanged   = errors.New("quick run publication has changed")
	ErrWorkingDirectory     = errors.New("quick run working directory unavailable")
	ErrVariablesUnavailable = errors.New("quick run variables unavailable")
	ErrRunNotFound          = errors.New("run not found")
	ErrForbidden            = errors.New("forbidden")
)

type Actor struct {
	UserID, Username string
	Role             identity.Role
}
type StartRequest struct {
	QuickRunID     string
	ConfirmOverlap bool
	Actor          Actor
}
type ActiveRun struct {
	ID     string `json:"run_id"`
	Status string `json:"status"`
}
type StartResult struct {
	RunID                   string      `json:"run_id,omitempty"`
	Status                  string      `json:"status,omitempty"`
	Conflict                string      `json:"conflict,omitempty"`
	ActiveRuns              []ActiveRun `json:"active_runs,omitempty"`
	RetryWithConfirmOverlap bool        `json:"retry_with_confirm_overlap,omitempty"`
	Revision                int64       `json:"-"`
	Digest                  string      `json:"-"`
	ScriptPath              string      `json:"-"`
}

type Options struct {
	DB               *sql.DB
	Runs             *runmanager.Manager
	PrepareScript    func(context.Context, string) (hostfiles.Script, error)
	PrepareDirectory func(context.Context, string) (hostfiles.PreparedDirectory, error)
	LoadVariables    func() (map[string]string, error)
}
type Controller struct{ options Options }

func New(options Options) *Controller { return &Controller{options: options} }

type quickRun struct {
	ID, Name, ScriptPath, ArgumentsTemplate, ScriptSHA256 string
	TimeoutSeconds                                        int
	Revision                                              int64
	GroupID                                               sql.NullString
}

func (controller *Controller) load(ctx context.Context, id string) (quickRun, error) {
	var q quickRun
	err := controller.options.DB.QueryRowContext(ctx, `SELECT id,name,script_path,arguments_template,timeout_seconds,script_sha256,revision,group_id FROM quick_runs WHERE id=?`, id).Scan(&q.ID, &q.Name, &q.ScriptPath, &q.ArgumentsTemplate, &q.TimeoutSeconds, &q.ScriptSHA256, &q.Revision, &q.GroupID)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	return q, err
}
func (controller *Controller) snapshot(ctx context.Context, q quickRun) string {
	if !q.GroupID.Valid {
		return q.Name
	}
	var group string
	if controller.options.DB.QueryRowContext(ctx, `SELECT name FROM quick_run_groups WHERE id=?`, q.GroupID.String).Scan(&group) != nil || group == "" {
		return q.Name
	}
	return group + " / " + q.Name
}

func (controller *Controller) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	q, err := controller.load(ctx, request.QuickRunID)
	if err != nil {
		return StartResult{}, err
	}
	prepared, err := controller.options.PrepareScript(ctx, q.ScriptPath)
	if err != nil || q.ScriptSHA256 == "" || subtle.ConstantTimeCompare([]byte(prepared.Digest), []byte(q.ScriptSHA256)) != 1 {
		return StartResult{}, ErrPublicationChanged
	}
	if controller.options.Runs.IsActiveScript(q.ScriptPath) && !request.ConfirmOverlap {
		rows, _ := controller.options.DB.QueryContext(ctx, `SELECT id,status FROM runs WHERE script_path_key=(SELECT script_path_key FROM quick_runs WHERE id=?) AND status IN ('starting','running','stopping','timing_out') ORDER BY created_at DESC LIMIT 10`, q.ID)
		result := StartResult{Conflict: "active_run", RetryWithConfirmOverlap: true, Revision: q.Revision, Digest: q.ScriptSHA256, ScriptPath: q.ScriptPath}
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var active ActiveRun
				_ = rows.Scan(&active.ID, &active.Status)
				result.ActiveRuns = append(result.ActiveRuns, active)
			}
		}
		return result, nil
	}
	directory, err := controller.options.PrepareDirectory(ctx, prepared.Directory)
	if err != nil {
		return StartResult{}, ErrWorkingDirectory
	}
	variables, err := controller.options.LoadVariables()
	if err != nil {
		return StartResult{}, fmt.Errorf("%w: %v", ErrVariablesUnavailable, err)
	}
	runID, err := controller.options.Runs.Start(runmanager.StartRequest{ScriptPath: q.ScriptPath, ExpectedDigest: q.ScriptSHA256, ArgumentsTemplate: q.ArgumentsTemplate, TimeoutSeconds: q.TimeoutSeconds, SourceType: "admin/quick-run", SourceName: controller.snapshot(ctx, q), SourceID: q.ID, Variables: variables, InitiatorUserID: request.Actor.UserID, InitiatorUsername: request.Actor.Username, PreparedScript: &prepared, PreparedDirectory: &directory})
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{RunID: runID, Status: "starting", Revision: q.Revision, Digest: q.ScriptSHA256}, nil
}

func (controller *Controller) Stop(_ context.Context, actor Actor, runID string) (runmanager.Run, error) {
	run, err := controller.options.Runs.GetMetadata(runID)
	if err != nil {
		return run, ErrRunNotFound
	}
	if actor.Role == identity.RoleOperator && run.InitiatorUserID != actor.UserID {
		return run, ErrForbidden
	}
	if actor.Role != identity.RoleOperator && actor.Role != identity.RoleMaintainer && actor.Role != identity.RoleAdministrator {
		return run, ErrForbidden
	}
	if err := controller.options.Runs.Stop(runID); err != nil {
		current, _ := controller.options.Runs.GetMetadata(runID)
		if current.Status == "starting" || current.Status == "running" || current.Status == "stopping" || current.Status == "timing_out" {
			return current, err
		}
	}
	return controller.options.Runs.GetMetadata(runID)
}
