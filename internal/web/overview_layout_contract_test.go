package web

import (
	"strings"
	"testing"
)

func TestOverviewWideDetailTablesStackInSeparateRows(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	const stackedDetailRule = `.overview-page .grid-2:has(.overview-detail-section--flat) { grid-template-columns: 1fr; }`
	if !strings.Contains(string(stylesheet), stackedDetailRule) {
		t.Fatal("storage and network detail tables must each occupy a full row")
	}
}
