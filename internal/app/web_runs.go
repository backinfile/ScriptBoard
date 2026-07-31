package app

import (
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
)

type runListItemView struct {
	runmanager.Run
	ScriptDirectoryURL string
}

func newRunListItemViews(runs []runmanager.Run) []runListItemView {
	views := make([]runListItemView, len(runs))
	for index, run := range runs {
		views[index] = runListItemView{
			Run:                run,
			ScriptDirectoryURL: scriptDirectoryURL(run.ScriptPath),
		}
	}
	return views
}

func scriptDirectoryURL(scriptPath string) string {
	directory, _ := hostfiles.Parent(scriptPath)
	return filesURL(directory)
}
