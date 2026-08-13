package web

import (
	"fmt"
	"strings"
	"testing"

	"scriptboard/internal/runmanager"
)

func TestFormatRunDownloadIncludesEveryEventWithoutResolvedArguments(t *testing.T) {
	events := make([]runmanager.Event, 1002)
	for index := range events {
		events[index].Data = fmt.Sprintf("event-%04d\n", index)
	}
	run := runmanager.Run{
		ID:                "run-one",
		ScriptPath:        "/scripts/example.sh",
		Status:            "succeeded",
		ArgumentsTemplate: "--token {{API_TOKEN}}",
		Arguments:         []string{"--token", "resolved-secret"},
		Events:            events,
	}

	download := string(formatRunDownload(run))
	for _, expected := range []string{"Run ID: run-one", "Argument template: --token {{API_TOKEN}}", "event-0000", "event-1001"} {
		if !strings.Contains(download, expected) {
			t.Fatalf("formatted Run download missing %q", expected)
		}
	}
	if strings.Contains(download, "resolved-secret") {
		t.Fatal("formatted Run download exposes resolved arguments")
	}
}
