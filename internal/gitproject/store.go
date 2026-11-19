package gitproject

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"scriptboard/internal/hostfiles"
)

const MaxBatch = 30

var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS git_projects (
		id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		remote TEXT NOT NULL, directory TEXT NOT NULL, path_key TEXT NOT NULL UNIQUE,
		branch TEXT NOT NULL, skip_tls INTEGER NOT NULL CHECK (skip_tls IN (0,1)),
		existing INTEGER NOT NULL CHECK (existing IN (0,1))
	)`,
	`CREATE TABLE IF NOT EXISTS git_runs (
		id TEXT PRIMARY KEY, action TEXT NOT NULL, source TEXT NOT NULL,
		started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0,
		results TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS git_runs_started ON git_runs(started_at DESC, id DESC)`,
	`CREATE TABLE IF NOT EXISTS quick_run_git_options (
		quick_id TEXT PRIMARY KEY REFERENCES quick_runs(id) ON DELETE CASCADE,
		pinned INTEGER NOT NULL CHECK (pinned IN (0,1))
	)`,
	`CREATE TABLE IF NOT EXISTS quick_run_git_projects (
		quick_id TEXT NOT NULL REFERENCES quick_runs(id) ON DELETE CASCADE,
		project_id TEXT NOT NULL REFERENCES git_projects(id), ordinal INTEGER NOT NULL,
		PRIMARY KEY(quick_id, project_id)
	)`,
	`CREATE TABLE IF NOT EXISTS quick_group_git_projects (
		group_id TEXT NOT NULL REFERENCES quick_run_groups(id) ON DELETE CASCADE,
		project_id TEXT NOT NULL REFERENCES git_projects(id), ordinal INTEGER NOT NULL,
		PRIMARY KEY(group_id, project_id)
	)`,
}

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const projectColumns = "id,name,remote,directory,path_key,branch,skip_tls,existing"

func Get(ctx context.Context, db Queryer, id string) (Project, error) {
	var project Project
	err := db.QueryRowContext(ctx, "SELECT "+projectColumns+" FROM git_projects WHERE id=?", id).
		Scan(&project.ID, &project.Name, &project.Remote, &project.Directory, &project.PathKey, &project.Branch, &project.SkipTLS, &project.Existing)
	return project, err
}

func List(ctx context.Context, db Queryer) ([]Project, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+projectColumns+" FROM git_projects ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Remote, &p.Directory, &p.PathKey, &p.Branch, &p.SkipTLS, &p.Existing); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func Add(ctx context.Context, db *sql.DB, project Project) error {
	if project.Name == "" || len(project.Name) > 128 || strings.ContainsAny(project.Name, "\r\n\x00") || !ValidID(project.ID) {
		return errors.New("invalid Git project name")
	}
	if project.Branch != "" && !ValidBranch(project.Branch) {
		return errors.New("invalid Git branch")
	}
	if project.Remote != "" || !project.Existing {
		if err := ValidateRemote(project.Remote, project.SkipTLS); err != nil {
			return err
		}
	}
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	projects, err := List(ctx, transaction)
	if err != nil {
		return err
	}
	for _, existing := range projects {
		_, outside := hostfiles.Rebase(existing.Directory, existing.Directory, project.Directory)
		_, contains := hostfiles.Rebase(project.Directory, project.Directory, existing.Directory)
		if outside == nil || contains == nil {
			return errors.New("checkout directory overlaps an existing Git project")
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO git_projects (`+projectColumns+`) VALUES (?,?,?,?,?,?,?,?)`,
		project.ID, project.Name, project.Remote, project.Directory, project.PathKey, project.Branch, project.SkipTLS, project.Existing); err != nil {
		return err
	}
	return transaction.Commit()
}

type Setup struct {
	Pinned bool
	Group  []string
	Own    []string
}

func QuickSetup(ctx context.Context, db Queryer, quickID string) (Setup, error) {
	var setup Setup
	var groupID sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT q.group_id, COALESCE(o.pinned, 0)
		FROM quick_runs q LEFT JOIN quick_run_git_options o ON o.quick_id=q.id WHERE q.id=?`, quickID).Scan(&groupID, &setup.Pinned); err != nil {
		return setup, err
	}
	var err error
	if groupID.Valid {
		setup.Group, err = GroupProjects(ctx, db, groupID.String)
		if err != nil {
			return setup, err
		}
	}
	setup.Own, err = selectedIDs(ctx, db, `SELECT project_id FROM quick_run_git_projects WHERE quick_id=? ORDER BY ordinal`, quickID)
	return setup, err
}

func GroupProjects(ctx context.Context, db Queryer, groupID string) ([]string, error) {
	return selectedIDs(ctx, db, `SELECT project_id FROM quick_group_git_projects WHERE group_id=? ORDER BY ordinal`, groupID)
}

func selectedIDs(ctx context.Context, db Queryer, query, id string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var selected []string
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			return nil, err
		}
		selected = append(selected, projectID)
	}
	return selected, rows.Err()
}

func SaveQuickSetup(ctx context.Context, tx *sql.Tx, id string, pinned bool, projects []string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO quick_run_git_options (quick_id,pinned) VALUES (?,?)
		ON CONFLICT(quick_id) DO UPDATE SET pinned=excluded.pinned`, id, pinned); err != nil {
		return err
	}
	return saveSelections(ctx, tx, id, projects, "quick_run_git_projects", "quick_id")
}

func SaveGroupProjects(ctx context.Context, tx *sql.Tx, id string, projects []string) error {
	return saveSelections(ctx, tx, id, projects, "quick_group_git_projects", "group_id")
}

func saveSelections(ctx context.Context, tx *sql.Tx, id string, projects []string, table, column string) error {
	if len(projects) > MaxBatch {
		return errors.New("too many selected Git projects")
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+"=?", id); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for index, projectID := range projects {
		if !ValidID(projectID) {
			return errors.New("invalid selected Git project")
		}
		if seen[projectID] {
			continue
		}
		seen[projectID] = true
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" ("+column+",project_id,ordinal) VALUES (?,?,?)", id, projectID, index); err != nil {
			return err
		}
	}
	return nil
}

type Entry struct {
	ProjectID, Name, Status, Branch, Before, Commit, Error string
}

type Run struct {
	ID, Source            string
	Action                Action
	StartedAt, FinishedAt int64
	Entries               []Entry
}

func SaveRun(ctx context.Context, db *sql.DB, run Run) error {
	payload, err := json.Marshal(run.Entries)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO git_runs (id,action,source,started_at,finished_at,results)
		VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at, results=excluded.results`,
		run.ID, run.Action, run.Source, run.StartedAt, run.FinishedAt, string(payload))
	return err
}

func ReadRun(ctx context.Context, db *sql.DB, id string) (Run, error) {
	return scanRun(db.QueryRowContext(ctx, `SELECT id,action,source,started_at,finished_at,results FROM git_runs WHERE id=?`, id))
}

func RecentRuns(ctx context.Context, db *sql.DB) ([]Run, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,action,source,started_at,finished_at,results FROM git_runs ORDER BY started_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var run Run
	var payload string
	err := row.Scan(&run.ID, &run.Action, &run.Source, &run.StartedAt, &run.FinishedAt, &payload)
	if err == nil {
		err = json.Unmarshal([]byte(payload), &run.Entries)
	}
	return run, err
}
