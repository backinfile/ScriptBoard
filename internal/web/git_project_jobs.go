package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"scriptboard/internal/gitproject"
)

type gitJobs struct {
	local  *gitproject.Checkout
	ctx    context.Context
	stop   context.CancelFunc
	slots  chan struct{}
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func newGitJobs(local *gitproject.Checkout) *gitJobs {
	ctx, stop := context.WithCancel(context.Background())
	return &gitJobs{local: local, ctx: ctx, stop: stop, slots: make(chan struct{}, 3)}
}

func (jobs *gitJobs) close() {
	jobs.mu.Lock()
	jobs.closed = true
	jobs.stop()
	jobs.mu.Unlock()
	jobs.wg.Wait()
}

func (jobs *gitJobs) start(ctx context.Context, process func(context.Context)) bool {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed {
		return false
	}
	jobs.wg.Add(1)
	go func() {
		defer jobs.wg.Done()
		jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		stop := context.AfterFunc(jobs.ctx, cancel)
		defer stop()
		process(jobCtx)
	}()
	return true
}

func (a *App) gitApply(ctx context.Context, project gitproject.Project, request gitproject.Request) (gitproject.Result, error) {
	select {
	case <-ctx.Done():
		return gitproject.Result{}, ctx.Err()
	case a.gitJobs.slots <- struct{}{}:
	}
	defer func() { <-a.gitJobs.slots }()
	operationCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.GitProject(operationCtx, request)
	}
	return a.gitJobs.local.Apply(operationCtx, project, request)
}

func recoverGitRuns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id FROM git_runs WHERE finished_at=0`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		run, err := gitproject.ReadRun(ctx, db, id)
		if err != nil {
			return err
		}
		for index := range run.Entries {
			if run.Entries[index].Status == "queued" || run.Entries[index].Status == "running" {
				run.Entries[index].Status = "failed"
				run.Entries[index].Error = "Git operation was interrupted by a service restart"
			}
		}
		run.FinishedAt = time.Now().Unix()
		if err := gitproject.SaveRun(ctx, db, run); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) createGitRun(ctx context.Context, action gitproject.Action, source string, projects []gitproject.Project) (gitproject.Run, error) {
	id, err := randomToken(18)
	if err != nil {
		return gitproject.Run{}, err
	}
	run := gitproject.Run{ID: id, Source: source, Action: action, StartedAt: time.Now().Unix()}
	for _, project := range projects {
		run.Entries = append(run.Entries, gitproject.Entry{ProjectID: project.ID, Name: project.Name, Status: "queued"})
	}
	return run, gitproject.SaveRun(ctx, a.db, run)
}

func (a *App) gitRunPhase(ctx context.Context, run *gitproject.Run, projects []gitproject.Project, requests []gitproject.Request, offset int) error {
	var mutex sync.Mutex
	var workers sync.WaitGroup
	var workErr error
	if len(projects) == 0 {
		return nil
	}
	update := func(index int, entry gitproject.Entry) error {
		mutex.Lock()
		defer mutex.Unlock()
		run.Entries[offset+index] = entry
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err := gitproject.SaveRun(writeCtx, a.db, *run)
		workErr = errors.Join(workErr, err)
		return err
	}

	for lane := 0; lane < min(3, len(projects)); lane++ {
		workers.Go(func() {
			for index := lane; index < len(projects); index += 3 {
				project := projects[index]
				entry := gitproject.Entry{ProjectID: project.ID, Name: project.Name, Status: "running"}
				if err := update(index, entry); err != nil {
					return
				}
				result, err := a.gitApply(ctx, project, requests[index])
				entry.Status = "succeeded"
				entry.Branch, entry.Before, entry.Commit = result.After.Branch, result.Before.Commit, result.After.Commit
				if err != nil {
					entry.Status, entry.Error = "failed", err.Error()
					mutex.Lock()
					workErr = errors.Join(workErr, fmt.Errorf("%s: %w", project.Name, err))
					mutex.Unlock()
				}
				_ = update(index, entry)
			}
		})
	}
	workers.Wait()
	return workErr
}

func (a *App) finishGitRun(ctx context.Context, run *gitproject.Run) error {
	run.FinishedAt = time.Now().Unix()
	for index := range run.Entries {
		entry := &run.Entries[index]
		if entry.Status == "queued" || entry.Status == "running" {
			entry.Status = "skipped"
		}
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return gitproject.SaveRun(writeCtx, a.db, *run)
}

func (a *App) runQuickGitProjects(ctx context.Context, quickID string) error {
	setup, err := gitproject.QuickSetup(ctx, a.db, quickID)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var projects []gitproject.Project
	var requests []gitproject.Request
	add := func(ids []string) error {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			project, err := gitproject.Get(ctx, a.db, id)
			if err != nil {
				return err
			}
			projects = append(projects, project)
			requests = append(requests, gitproject.Request{ProjectID: id, QuickID: quickID, Action: gitproject.Sync})
		}
		return nil
	}
	if err := add(setup.Group); err != nil {
		return err
	}
	groupCount := len(projects)
	if err := add(setup.Own); err != nil {
		return err
	}
	if len(projects) == 0 {
		return nil
	}

	run, err := a.createGitRun(ctx, gitproject.Sync, "quick_run", projects)
	if err != nil {
		return err
	}
	err = a.gitRunPhase(ctx, &run, projects[:groupCount], requests[:groupCount], 0)
	if err == nil {
		err = a.gitRunPhase(ctx, &run, projects[groupCount:], requests[groupCount:], groupCount)
	}
	if err = errors.Join(err, a.finishGitRun(ctx, &run)); err != nil {
		return fmt.Errorf("Git preflight %s: %w", run.ID, err)
	}
	return nil
}
