package web

import (
	"bytes"
	"testing"

	"scriptboard/internal/uploadinbox"
)

func TestUploadInboxRendersOneHeadingWithThePageTitle(t *testing.T) {
	t.Parallel()

	var rendered bytes.Buffer
	err := uploadInboxTemplate.Execute(&rendered, struct {
		Entries   []uploadinbox.Pending
		CSRFToken string
		Locale    webLocale
	}{
		Entries: []uploadinbox.Pending{{OriginalName: "external-result.txt"}},
		Locale:  localeEnglishUS,
	})
	if err != nil {
		t.Fatal(err)
	}

	if count := bytes.Count(rendered.Bytes(), []byte(">Upload inbox</h")); count != 1 {
		t.Fatalf("upload inbox title heading count=%d, want 1", count)
	}
	if !bytes.Contains(rendered.Bytes(), []byte("<h2>Pending uploads</h2>")) {
		t.Fatal("upload inbox section heading is missing")
	}
}
