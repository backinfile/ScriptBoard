package app

import "testing"

func TestExternalLimitSourceIgnoresEphemeralPorts(t *testing.T) {
	for input, expected := range map[string]string{
		"192.0.2.10:49152":   "192.0.2.10",
		"[2001:db8::10]:443": "2001:db8::10",
		"203.0.113.7":        "203.0.113.7",
		"PROXY.internal":     "proxy.internal",
	} {
		if actual := externalLimitSource(input); actual != expected {
			t.Errorf("externalLimitSource(%q) = %q, want %q", input, actual, expected)
		}
	}
}
