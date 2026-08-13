package runtimeinstall

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeReleaseURLsArePinnedToOfficialRepository(t *testing.T) {
	validAsset := "https://github.com/backinfile/ScriptBoard/releases/download/v1.2.3/" + ManifestFilename
	if err := validateRuntimeReleaseAssetURL(validAsset, "v1.2.3", ManifestFilename); err != nil {
		t.Fatalf("valid asset URL: %v", err)
	}
	validPage := "https://github.com/backinfile/ScriptBoard/releases/tag/v1.2.3"
	if err := validateRuntimeReleasePageURL(validPage, "v1.2.3"); err != nil {
		t.Fatalf("valid release URL: %v", err)
	}
	for _, rawURL := range []string{
		"http://github.com/backinfile/ScriptBoard/releases/download/v1.2.3/" + ManifestFilename,
		"https://evil.example/backinfile/ScriptBoard/releases/download/v1.2.3/" + ManifestFilename,
		"https://user@github.com/backinfile/ScriptBoard/releases/download/v1.2.3/" + ManifestFilename,
		"https://github.com/backinfile/Other/releases/download/v1.2.3/" + ManifestFilename,
		validAsset + "?download=1",
		validAsset + "#fragment",
	} {
		if err := validateRuntimeReleaseAssetURL(rawURL, "v1.2.3", ManifestFilename); err == nil {
			t.Fatalf("untrusted asset URL accepted: %s", rawURL)
		}
	}
}

func TestRuntimeDownloadUsesSharedOutboundPolicy(t *testing.T) {
	transport, ok := NewGitHubSource().Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("runtime transport type=%T", NewGitHubSource().Client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("runtime downloads must not use the environment proxy")
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "169.254.169.254:443"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata runtime dial error=%v", err)
	}
}

func TestRuntimeDownloadRedirectsStayOnAllowlist(t *testing.T) {
	check := NewGitHubSource().Client.CheckRedirect
	request, err := http.NewRequest(http.MethodGet, "https://release-assets.githubusercontent.com/github-production-release-asset/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "secret")
	if err := check(request, []*http.Request{{}}); err != nil {
		t.Fatalf("official redirect rejected: %v", err)
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatal("authorization header was forwarded to runtime asset host")
	}
	for _, rawURL := range []string{
		"http://github.com/backinfile/ScriptBoard/releases/download/v1.2.3/file",
		"https://evil.example/file",
		"https://user@github.com/file",
		"https://github.com:444/file",
	} {
		redirect, requestErr := http.NewRequest(http.MethodGet, rawURL, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if err := check(redirect, []*http.Request{{}}); err == nil {
			t.Fatalf("untrusted redirect accepted: %s", rawURL)
		}
	}
	tooMany := make([]*http.Request, 5)
	if err := check(request, tooMany); err == nil {
		t.Fatal("redirect chain limit was not enforced")
	}
}
