package app

import (
	"testing"

	"scriptboard/internal/runmanager"
)

func TestRunListItemViewBuildsScriptDirectoryURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scriptPath string
		want       string
	}{
		{name: "managed root", scriptPath: "inspect.sh", want: "/resources/files/"},
		{name: "nested directory", scriptPath: "ops/inspect.sh", want: "/resources/files/ops/"},
		{name: "reserved path characters", scriptPath: "ops #1/inspect.sh", want: "/resources/files/ops%20%231/"},
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
