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

func TestExternalUploadNeverAllowsActiveOrDoubleExtensionContent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		allowed []string
	}{
		{"payload.sh", []string{".sh"}},
		{"payload.exe", []string{".exe"}},
		{"payload.html", []string{".html"}},
		{"payload.svg", []string{".svg"}},
		{"payload.txt.exe", []string{".exe"}},
		{"invoice.pdf.txt", []string{".txt"}},
	} {
		if externalExtensionAllowed(test.name, test.allowed) {
			t.Errorf("dangerous external upload allowed: %s", test.name)
		}
	}
}
