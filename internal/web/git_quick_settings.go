package web

import (
	"context"
	"net/http"
	"slices"

	"scriptboard/internal/gitproject"
)

type gitProjectPick struct {
	ID, Name            string
	Selected, Inherited bool
}

func (a *App) fillGitTaskSettings(request *http.Request, data *taskPageData) error {
	ctx := request.Context()
	var own, inherited []string
	var err error
	switch data.Kind {
	case "quick-edit":
		var setup gitproject.Setup
		setup, err = gitproject.QuickSetup(ctx, a.db, data.ID)
		own, inherited, data.GitPinned = setup.Own, setup.Group, setup.Pinned
	case "quick-create":
		if data.GroupID != "" {
			inherited, err = gitproject.GroupProjects(ctx, a.db, data.GroupID)
		}
		if request.Method == http.MethodPost && request.FormValue("git_settings") == "1" {
			own, data.GitPinned = request.Form["git_project"], request.FormValue("follow_file") != "1"
		}
	case "record-group-new", "record-group-edit", "quick-group-new", "quick-group-edit":
		if data.BackURL != "/config/quick-runs" {
			return nil
		}
		data.GitGroup = true
		if data.ID != "" {
			own, err = gitproject.GroupProjects(ctx, a.db, data.ID)
		}
	default:
		return nil
	}
	if err != nil {
		return err
	}
	data.GitSettings = true
	projects, err := gitproject.List(ctx, a.db)
	if err != nil {
		return err
	}
	for _, project := range projects {
		data.GitProjects = append(data.GitProjects, gitProjectPick{ID: project.ID, Name: project.Name,
			Selected: slices.Contains(own, project.ID), Inherited: slices.Contains(inherited, project.ID)})
	}
	return nil
}

func (a *App) gitProjectSetupStats(ctx context.Context) (map[string]int, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, (
		SELECT COUNT(*) FROM (
			SELECT project_id FROM quick_run_git_projects WHERE quick_id=q.id
			UNION
			SELECT project_id FROM quick_group_git_projects WHERE group_id=q.group_id
		)
	) FROM quick_runs q`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}
