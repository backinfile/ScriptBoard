package websitemonitor

import "testing"

func TestWebsiteMonitorScopeDoesNotChangeOutboundAccess(t *testing.T) {
	for _, scope := range []Scope{ScopeLocal, ScopeExternal} {
		policy := outboundPolicy(Config{Scope: scope})
		if !policy.AllowAnyPort || !policy.AllowPrivate {
			t.Fatalf("website monitor scope %q changed outbound access: %#v", scope, policy)
		}
	}
}
