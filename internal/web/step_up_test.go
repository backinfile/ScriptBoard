package web

import (
	"scriptboard/internal/identity"
	"testing"
	"time"
)

func TestRecentAuthenticationValidityIsBounded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, test := range []struct {
		name      string
		timestamp int64
		want      bool
	}{
		{"missing", 0, false},
		{"current", now.Unix(), true},
		{"window boundary", now.Add(-identity.RecentAuthenticationWindow).Unix(), true},
		{"expired", now.Add(-identity.RecentAuthenticationWindow - time.Second).Unix(), false},
		{"clock skew", now.Add(time.Minute).Unix(), true},
		{"future", now.Add(time.Minute + time.Second).Unix(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := identity.RecentAuthenticationValid(test.timestamp, now); got != test.want {
				t.Fatalf("valid=%v, want %v", got, test.want)
			}
		})
	}
}

func TestStepUpReturnTargetRejectsExternalAndRecursiveLocations(t *testing.T) {
	for _, unsafe := range []string{"", "https://attacker.example/", "//attacker.example/", "/%2f%2fattacker.example/", "/auth/step-up?return_to=/monitor", "/safe\\unsafe", "/safe%5cunsafe", "/safe\nunsafe"} {
		if got := safeStepUpReturnTo(unsafe); got != "/monitor" {
			t.Errorf("safeStepUpReturnTo(%q)=%q", unsafe, got)
		}
	}
	if got := safeStepUpReturnTo("/settings/users?view=active"); got != "/settings/users?view=active" {
		t.Fatalf("safe local target=%q", got)
	}
}
