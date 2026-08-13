package web

import (
	"strings"
	"testing"
)

func TestPasswordPolicyUsesLengthAndBlocklistWithoutCompositionRules(t *testing.T) {
	for _, password := range []string{
		"short-password", "password123456", "password123456     ",
		strings.Repeat("界", 15), "administrator",
	} {
		if err := validatePasswordPolicy(password, "administrator"); err == nil {
			t.Errorf("accepted weak password %q", password)
		}
	}
	for _, password := range []string{
		"a long passphrase with spaces", "纯中文也可以作为足够长的安全口令短语", "no-uppercase-or-digit-required-here",
	} {
		if err := validatePasswordPolicy(password, "administrator"); err != nil {
			t.Errorf("rejected valid passphrase %q: %v", password, err)
		}
	}
}
