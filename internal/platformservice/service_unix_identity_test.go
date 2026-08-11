//go:build !windows

package platformservice

import "testing"

func TestLinuxWebServiceIdentityIsDedicated(t *testing.T) {
	if webServiceUser == "" || webServiceUser == "root" || webServiceUser == "nobody" {
		t.Fatalf("Web service identity is not dedicated: %q", webServiceUser)
	}
}

func TestLinuxManagedWebRuntimeRejectsRootAndOtherUsers(t *testing.T) {
	if err := validateLinuxWebRuntimeIdentity(0, 1200); err == nil {
		t.Fatal("root identity was accepted")
	}
	if err := validateLinuxWebRuntimeIdentity(1201, 1200); err == nil {
		t.Fatal("different identity was accepted")
	}
	if err := validateLinuxWebRuntimeIdentity(1200, 1200); err != nil {
		t.Fatalf("dedicated identity rejected: %v", err)
	}
}
