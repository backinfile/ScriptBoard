package web_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrimaryNavigationPagesShareHeadingContract(t *testing.T) {
	t.Parallel()

	templates := []string{
		"applications.html", "containers.html", "files.html", "quick-access.html", "variables.html", "runs.html",
		"audit.html", "service-logs.html", "quick-runs.html", "schedules.html", "external-interfaces.html",
	}
	for _, name := range templates {
		source, err := os.ReadFile(filepath.Join("ui", "templates", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Contains(source, []byte(`class="page-heading primary-page-heading`)) {
			t.Errorf("%s does not use the shared primary page heading", name)
		}
	}

	stylesheet, err := os.ReadFile(filepath.Join("ui", "assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range [][]byte{
		[]byte(`.primary-page-heading h1 {`),
		[]byte(`font-size: clamp(1.8rem, 3vw, 2.4rem);`),
		[]byte(`.primary-page-heading .page-eyebrow {`),
		[]byte(`.primary-page-heading > div > p:last-child {`),
	} {
		if !bytes.Contains(stylesheet, expected) {
			t.Errorf("shared heading CSS is missing %q", expected)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`[data-variables-page] .page-heading h1`),
		[]byte(`[data-audit-page] .page-heading h1`),
		[]byte(`[data-schedules-page] .page-heading h1`),
		[]byte(`[data-external-interfaces-page] .page-heading h1`),
	} {
		if bytes.Contains(stylesheet, forbidden) {
			t.Errorf("primary page retains a compact heading override %q", forbidden)
		}
	}
}

func TestQuickAccessGroupActionPrecedesBrowseFilesInHeading(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join("ui", "templates", "quick-access.html"))
	if err != nil {
		t.Fatal(err)
	}

	headingEnd := bytes.Index(source, []byte(`</header>`))
	if headingEnd < 0 {
		t.Fatal("Quick access heading is missing its closing tag")
	}
	heading := source[:headingEnd]
	groupAction := []byte(`/config/groups/new?return_to=%2Fconfig%2Fquick-access`)
	browseFilesAction := []byte(`href="/resources/files"`)
	groupIndex := bytes.Index(heading, groupAction)
	browseFilesIndex := bytes.Index(heading, browseFilesAction)
	if groupIndex < 0 || browseFilesIndex < 0 || groupIndex >= browseFilesIndex {
		t.Fatalf("Quick access heading actions must place New group before Browse files")
	}
	if bytes.Contains(source[headingEnd:], groupAction) {
		t.Fatal("New group action must not be duplicated inside the Quick access panel")
	}
}
