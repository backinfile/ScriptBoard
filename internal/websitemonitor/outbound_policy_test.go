package websitemonitor

import "testing"

func TestExternalWebsiteMonitorAllowsAnyPortWithoutPrivateNetworkAccess(t *testing.T) {
	policy := outboundPolicy(Config{Scope: ScopeExternal})

	if !policy.AllowAnyPort {
		t.Fatal("external website monitors should allow non-standard ports")
	}
	if policy.AllowPrivate {
		t.Fatal("external website monitors should not gain private network access")
	}
}
