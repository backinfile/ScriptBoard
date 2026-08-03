package app

import (
	"path/filepath"
	"testing"

	"scriptboard/internal/runmanager"
)

func TestRunListItemViewBuildsScriptDirectoryURL(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	tests := []struct {
		name       string
		scriptPath string
		want       string
	}{
		{name: "host root", scriptPath: filepath.Join(root, "inspect.sh"), want: filesURL(root)},
		{name: "nested directory", scriptPath: filepath.Join(root, "ops", "inspect.sh"), want: filesURL(filepath.Join(root, "ops"))},
		{name: "reserved path characters", scriptPath: filepath.Join(root, "ops #1", "inspect.sh"), want: filesURL(filepath.Join(root, "ops #1"))},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			views := newRunListItemViews([]runmanager.Run{{ScriptPath: test.scriptPath}})
			if views[0].ScriptDirectoryURL != test.want {
				t.Fatalf("ScriptDirectoryURL=%q, want %q", views[0].ScriptDirectoryURL, test.want)
			}
		})
	}
}
