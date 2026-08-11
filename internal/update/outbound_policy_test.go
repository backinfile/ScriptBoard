package update

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDefaultUpdateClientUsesSharedOutboundPolicy(t *testing.T) {
	source := NewGitHubSource()
	transport, ok := source.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("update transport type=%T", source.Client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("default update client must not use environment proxy")
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:443"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("loopback update dial error=%v", err)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "169.254.169.254:443"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata update dial error=%v", err)
	}
}
