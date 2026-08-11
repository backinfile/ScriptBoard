//go:build !windows

package platformservice

import "testing"

func TestLinuxWebServiceIdentityIsDedicated(t *testing.T) {
	if webServiceUser == "" || webServiceUser == "root" || webServiceUser == "nobody" {
		t.Fatalf("Web service identity is not dedicated: %q", webServiceUser)
	}
}
