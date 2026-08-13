package mfa

import (
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238SHA1Vector(t *testing.T) {
	code, err := TOTPCode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code=%q, want 287082", code)
	}
}

func TestTOTPRejectsMalformedSecrets(t *testing.T) {
	for _, secret := range []string{"", "not base32!", "MY"} {
		if _, err := TOTPCode(secret, time.Unix(59, 0)); err == nil {
			t.Errorf("secret %q was accepted", secret)
		}
	}
}
