package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestTextPreviewKeepsReadableTerminalContrast(t *testing.T) {
	t.Parallel()

	stylesheet, err := webFiles.ReadFile("ui/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	editorRule := regexp.MustCompile(`(?s)\.editor-page \.text-preview\s*\{([^}]+)\}`).FindStringSubmatch(css)
	if len(editorRule) != 2 {
		t.Fatal("text preview editor override is missing")
	}
	for _, declaration := range []string{"color: var(--terminal-ink)", "background: var(--terminal)"} {
		if !strings.Contains(editorRule[1], declaration) {
			t.Fatalf("text preview editor override is missing %q: %s", declaration, editorRule[1])
		}
	}

	background := cssHexToken(t, css, "terminal")
	foregrounds := []string{cssHexToken(t, css, "terminal-ink")}
	highlightStart, highlightEnd := strings.Index(css, ".hljs {"), strings.Index(css, ".hljs-emphasis")
	if highlightStart < 0 || highlightEnd <= highlightStart {
		t.Fatal("text preview syntax highlight palette is missing")
	}
	highlightRules := css[highlightStart:highlightEnd]
	for _, match := range regexp.MustCompile(`#[0-9a-fA-F]{6}`).FindAllString(highlightRules, -1) {
		foregrounds = append(foregrounds, match)
	}
	for _, foreground := range foregrounds {
		if ratio := contrastRatio(t, foreground, background); ratio < 4.5 {
			t.Errorf("text preview color %s on %s has contrast %.2f:1, want at least 4.5:1", foreground, background, ratio)
		}
	}
}

func cssHexToken(t *testing.T, css, name string) string {
	t.Helper()
	match := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6})`).FindStringSubmatch(css)
	if len(match) != 2 {
		t.Fatalf("CSS token --%s is missing or is not an opaque hex color", name)
	}
	return match[1]
}

func contrastRatio(t *testing.T, foreground, background string) float64 {
	t.Helper()
	foregroundLuminance := relativeLuminance(t, foreground)
	backgroundLuminance := relativeLuminance(t, background)
	lighter := math.Max(foregroundLuminance, backgroundLuminance)
	darker := math.Min(foregroundLuminance, backgroundLuminance)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(t *testing.T, color string) float64 {
	t.Helper()
	channels := make([]float64, 3)
	for index := range channels {
		value, err := strconv.ParseUint(color[1+index*2:3+index*2], 16, 8)
		if err != nil {
			t.Fatalf("parse color %q: %v", color, err)
		}
		channel := float64(value) / 255
		if channel <= 0.04045 {
			channels[index] = channel / 12.92
		} else {
			channels[index] = math.Pow((channel+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}
