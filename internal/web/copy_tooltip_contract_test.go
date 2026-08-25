package web

import (
	"strings"
	"testing"
)

func TestCopyIconTooltipsUseTheTopLayer(t *testing.T) {
	t.Parallel()

	script, err := webFiles.ReadFile("ui/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}

	for _, fragment := range []string{
		`function initIconTooltipLayer()`,
		`tooltip.setAttribute("popover", "manual")`,
		`tooltip.showPopover()`,
		`initIconTooltipLayer();`,
	} {
		if !strings.Contains(string(script), fragment) {
			t.Errorf("copy tooltip layer is missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		`.icon-tooltip-layer`,
		`position: fixed`,
		`z-index: 1300`,
	} {
		if !strings.Contains(string(stylesheet), fragment) {
			t.Errorf("copy tooltip fallback layer is missing %q", fragment)
		}
	}
}
