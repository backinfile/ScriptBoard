package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Run struct {
	ID        string                     `json:"id"`
	EntryID   string                     `json:"entryId"`
	Status    string                     `json:"status"`
	Snapshot  Snapshot                   `json:"snapshot"`
	Result    map[string]json.RawMessage `json:"result"`
	Error     string                     `json:"error"`
	Actor     string                     `json:"actor"`
	CreatedAt int64                      `json:"createdAt"`
	UpdatedAt int64                      `json:"updatedAt"`
	Steps     []Step                     `json:"steps"`
}
type Attempt struct {
	Number     int    `json:"number"`
	ProcessID  string `json:"processId,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}
type Step struct {
	Attempts  []Attempt                  `json:"attempts"`
	Outlets   []string                   `json:"outlets"`
	NodeID    string                     `json:"nodeId"`
	Status    string                     `json:"status"`
	ProcessID string                     `json:"processId"`
	Result    map[string]json.RawMessage `json:"result"`
	Error     string                     `json:"error"`
}

func (r Repository) Enqueue(ctx context.Context, entryID, actor string) (Run, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var raw string
	var snapshot Snapshot
	if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='entry' AND id=?", entryID).Scan(&raw); err != nil {
		return Run{}, err
	}
	if err = json.Unmarshal([]byte(raw), &snapshot.Entry); err != nil {
		return Run{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT body FROM workflow_documents WHERE kind='published' AND id=? AND revision=?", snapshot.Entry.WorkflowID, snapshot.Entry.WorkflowRevision).Scan(&raw); err != nil {
		return Run{}, fmt.Errorf("%w: entry requires current published revision", ErrInvalid)
	}
	if err = json.Unmarshal([]byte(raw), &snapshot.Workflow); err != nil {
		return Run{}, err
	}
	if err = validateEntry(snapshot.Entry, snapshot.Workflow); err != nil {
		return Run{}, err
	}
	values, err := ResolveInputs(Graph{Nodes: []Node{{ID: "entry", Kind: "entry", Inputs: snapshot.Workflow.Parameters, Values: snapshot.Entry.Values}}}, "entry", nil)
	if err != nil {
		return Run{}, err
	}
	snapshot.Entry.Values = values
	if err = captureLookups(ctx, tx, &snapshot); err != nil {
		return Run{}, err
	}
	order, err := Compile(snapshot.Workflow.Graph)
	if err != nil {
		return Run{}, err
	}
	bytes, err := json.Marshal(snapshot)
	if err != nil {
		return Run{}, err
	}
	now := time.Now().UnixNano()
	run := Run{ID: newID(), EntryID: entryID, Actor: actor, Status: "queued", Snapshot: snapshot, CreatedAt: now, UpdatedAt: now, Result: map[string]json.RawMessage{}}
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_runs(id,entry_id,status,snapshot,actor,created_at,updated_at) VALUES(?,?,'queued',?,?,?,?)", run.ID, entryID, string(bytes), actor, now, now); err != nil {
		return Run{}, err
	}
	for _, label := range snapshot.Entry.Locks {
		if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_run_locks(run_id,label) VALUES(?,?)", run.ID, label); err != nil {
			return Run{}, err
		}
	}
	for _, id := range order {
		if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_steps(run_id,node_id,status) VALUES(?,?,'pending')", run.ID, id); err != nil {
			return Run{}, err
		}
	}
	return run, tx.Commit()
}
func (r Repository) Run(ctx context.Context, id string) (Run, error) {
	var run Run
	var snapshot, result string
	err := r.DB.QueryRowContext(ctx, "SELECT id,entry_id,status,snapshot,result,error,actor,created_at,updated_at FROM workflow_runs WHERE id=?", id).Scan(&run.ID, &run.EntryID, &run.Status, &snapshot, &result, &run.Error, &run.Actor, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return run, err
	}
	if err = json.Unmarshal([]byte(snapshot), &run.Snapshot); err != nil {
		return run, err
	}
	if err = json.Unmarshal([]byte(result), &run.Result); err != nil {
		return run, err
	}
	rows, err := r.DB.QueryContext(ctx, "SELECT s.node_id,s.status,s.process_id,s.result,s.error,COALESCE(t.attempts,'[]'),COALESCE(t.outlets,'[]') FROM workflow_steps s LEFT JOIN workflow_step_runtime t ON t.run_id=s.run_id AND t.node_id=s.node_id WHERE s.run_id=? ORDER BY s.rowid", id)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	run.Steps = []Step{}
	for rows.Next() {
		var step Step
		var raw, attempts, outlets string
		if err = rows.Scan(&step.NodeID, &step.Status, &step.ProcessID, &raw, &step.Error, &attempts, &outlets); err != nil {
			return run, err
		}
		if err = json.Unmarshal([]byte(raw), &step.Result); err != nil {
			return run, err
		}
		if err = json.Unmarshal([]byte(attempts), &step.Attempts); err != nil {
			return run, err
		}
		if err = json.Unmarshal([]byte(outlets), &step.Outlets); err != nil {
			return run, err
		}
		run.Steps = append(run.Steps, step)
	}
	return run, rows.Err()
}
func (r Repository) Runs(ctx context.Context) ([]Run, error) {
	rows, err := r.DB.QueryContext(ctx, "SELECT id FROM workflow_runs ORDER BY created_at DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Run{}
	for _, id := range ids {
		run, err := r.Run(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// Claim acquires every mutex label and changes state in one transaction.
// An earlier queued owner of an overlapping label takes precedence.
func (r Repository) Claim(ctx context.Context, id string) (bool, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	var created int64
	if err = tx.QueryRowContext(ctx, "SELECT status,created_at FROM workflow_runs WHERE id=?", id).Scan(&status, &created); err != nil {
		return false, err
	}
	if status != "queued" {
		return false, nil
	}
	var conflicts int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_run_locks wanted
 WHERE wanted.run_id=? AND (
 EXISTS(SELECT 1 FROM workflow_resource_locks held WHERE held.label=wanted.label)
 OR EXISTS(SELECT 1 FROM workflow_run_locks prior JOIN workflow_runs run ON run.id=prior.run_id
 WHERE prior.label=wanted.label AND run.status='queued' AND (run.created_at<? OR (run.created_at=? AND run.id<?))))`, id, created, created, id).Scan(&conflicts); err != nil {
		return false, err
	}
	if conflicts > 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_resource_locks(label,run_id) SELECT label,run_id FROM workflow_run_locks WHERE run_id=?", id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_runs SET status='running',updated_at=? WHERE id=? AND status='queued'", time.Now().UnixNano(), id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (r Repository) Finish(ctx context.Context, id, status, message string, result map[string]json.RawMessage) error {
	switch status {
	case "succeeded", "failed", "cancelled", "needs_attention":
	default:
		return fmt.Errorf("invalid terminal state")
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE workflow_runs SET status=?,error=?,result=?,updated_at=? WHERE id=? AND status IN ('running','cancelling','queued')", status, message, string(raw), time.Now().UnixNano(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	if status != "needs_attention" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_resource_locks WHERE run_id=?", id); err != nil {
			return err
		}
	}
	if status != "succeeded" {
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_steps SET status='skipped' WHERE run_id=? AND status='pending'", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Inspect and transition under one transaction so cancellation cannot release a newly claimed run.
func (r Repository) RequestCancel(ctx context.Context, id string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM workflow_runs WHERE id=?", id).Scan(&status); err != nil {
		return err
	}
	switch status {
	case "queued":
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_runs SET status='cancelled',error='cancelled before dispatch',updated_at=? WHERE id=?", time.Now().UnixNano(), id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_steps SET status='skipped' WHERE run_id=? AND status='pending'", id); err != nil {
			return err
		}
	case "running":
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_runs SET status='cancelling',updated_at=? WHERE id=?", time.Now().UnixNano(), id); err != nil {
			return err
		}
	default:
		return ErrConflict
	}
	return tx.Commit()
}
func (r Repository) Recover(ctx context.Context) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE workflow_runs SET status='needs_attention',error='Service restarted; inspect previous process before releasing resources',updated_at=? WHERE status IN ('running','cancelling')", time.Now().UnixNano())
	return err
}
func (r Repository) ResolveAttention(ctx context.Context, id string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE workflow_runs SET status='cancelled',updated_at=? WHERE id=? AND status='needs_attention'", time.Now().UnixNano(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_resource_locks WHERE run_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r Repository) BeginStep(ctx context.Context, runID, nodeID string) error {
	res, err := r.DB.ExecContext(ctx, "UPDATE workflow_steps SET status='dispatching' WHERE run_id=? AND node_id=? AND status='pending' AND EXISTS(SELECT 1 FROM workflow_runs WHERE id=? AND status='running')", runID, nodeID, runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (r Repository) StepProcess(ctx context.Context, runID, nodeID, processID string) error {
	res, err := r.DB.ExecContext(ctx, "UPDATE workflow_steps SET process_id=?,status='running' WHERE run_id=? AND node_id=? AND status='dispatching'", processID, runID, nodeID)
	if err == nil {
		count, _ := res.RowsAffected()
		if count != 1 {
			return ErrConflict
		}
	}
	return err
}
func (r Repository) FinishStep(ctx context.Context, runID, nodeID, status, message string, result map[string]json.RawMessage) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, "UPDATE workflow_steps SET status=?,error=?,result=? WHERE run_id=? AND node_id=?", status, message, string(raw), runID, nodeID)
	return err
}

func (r Repository) Queued(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, "SELECT id FROM workflow_runs WHERE status='queued' ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r Repository) SkipPending(ctx context.Context, id string) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE workflow_steps SET status='skipped' WHERE run_id=? AND status='pending'", id)
	return err
}

func (r Repository) StepRuntime(ctx context.Context, runID, nodeID string, attempts []Attempt, outlets []string) error {
	a, err := json.Marshal(attempts)
	if err != nil {
		return err
	}
	o, err := json.Marshal(outlets)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, "INSERT INTO workflow_step_runtime(run_id,node_id,attempts,outlets) VALUES(?,?,?,?) ON CONFLICT(run_id,node_id) DO UPDATE SET attempts=excluded.attempts,outlets=excluded.outlets", runID, nodeID, string(a), string(o))
	return err
}
func (r Repository) RetryStep(ctx context.Context, runID, nodeID string) error {
	res, err := r.DB.ExecContext(ctx, "UPDATE workflow_steps SET status='dispatching',process_id='' WHERE run_id=? AND node_id=? AND EXISTS(SELECT 1 FROM workflow_runs WHERE id=? AND status='running')", runID, nodeID, runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
