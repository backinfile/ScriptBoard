package web

import (
	"strings"
	"testing"
)

func TestApplicationBrandAllowsCJKGlyphHeight(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatalf("read application stylesheet: %v", err)
	}

	css := string(stylesheet)
	if !strings.Contains(css, "font: 680 1.05rem/1.25 var(--font-display);") {
		t.Fatal("application brand line height must leave room for CJK glyph metrics")
	}
}
