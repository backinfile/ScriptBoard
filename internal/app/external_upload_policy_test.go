package app

import "testing"

func TestExternalUploadRequiresAnExplicitExtensionAllowlist(t *testing.T) {
	t.Parallel()

	if externalExtensionAllowed("payload.txt", nil) {
		t.Fatal("an empty extension allowlist allowed an external upload")
	}
	if !externalExtensionAllowed("report.TXT", []string{".txt"}) {
		t.Fatal("an explicitly allowed extension was rejected")
	}
}
