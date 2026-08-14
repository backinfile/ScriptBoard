package web

import (
	"time"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
)

type runListItemView struct {
	runmanager.Run
	ScriptDirectoryURL string
	Duration           string
	HasDuration        bool
}

func newRunListItemViews(runs []runmanager.Run, locale webLocale, loadedAt time.Time) []runListItemView {
	views := make([]runListItemView, len(runs))
	for index, run := range runs {
		view := runListItemView{
			Run:                run,
			ScriptDirectoryURL: scriptDirectoryURL(run.ScriptPath),
		}
		if run.StartedAt != nil {
			var durationEnd *time.Time
			switch {
			case run.FinishedAt != nil:
				durationEnd = run.FinishedAt
			case quickRunHistoryActive(run.Status):
				durationEnd = &loadedAt
			}
			if durationEnd != nil && !durationEnd.Before(*run.StartedAt) {
				view.Duration = quickRunDuration(locale, durationEnd.Sub(*run.StartedAt))
				view.HasDuration = true
			}
		}
		views[index] = view
	}
	return views
}

func scriptDirectoryURL(scriptPath string) string {
	directory, _ := hostfiles.Parent(scriptPath)
	return filesURL(directory)
}
