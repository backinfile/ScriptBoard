//go:build windows

package privatepath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProtectDirectoryUsesAProtectedPrivateACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(everyone)
	publicACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		pinner.Unpin()
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		root,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		publicACL,
		nil,
	); err != nil {
		pinner.Unpin()
		t.Fatal(err)
	}
	pinner.Unpin()

	child := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(child, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ProtectDirectory(root); err != nil {
		t.Fatalf("protect directory: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		root,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("directory DACL is not protected: %s", descriptor)
	}
	sddl := descriptor.String()
	for _, publicTrustee := range []string{";;;WD)", ";;;BU)", ";;;AU)"} {
		if strings.Contains(sddl, publicTrustee) {
			t.Fatalf("directory ACL grants a public trustee: %s", sddl)
		}
	}

	childDescriptor, err := windows.GetNamedSecurityInfo(
		child,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	childSDDL := childDescriptor.String()
	for _, publicTrustee := range []string{";;;WD)", ";;;BU)", ";;;AU)"} {
		if strings.Contains(childSDDL, publicTrustee) {
			t.Fatalf("child ACL grants a public trustee: %s", childSDDL)
		}
	}
}

func TestProtectedPrivateDescriptorRejectsBroadOrInheritedACLs(t *testing.T) {
	for _, fixture := range []struct {
		name string
		sddl string
		want bool
	}{
		{name: "private service ACL", sddl: "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;0x1301bf;;;S-1-5-80-1234)", want: true},
		{name: "inherited ACL", sddl: "D:(A;;FA;;;SY)(A;;FA;;;BA)", want: false},
		{name: "everyone", sddl: "D:P(A;;FA;;;SY)(A;;FR;;;WD)", want: false},
		{name: "builtin users", sddl: "D:P(A;;FA;;;SY)(A;;FR;;;BU)", want: false},
		{name: "authenticated users", sddl: "D:P(A;;FA;;;SY)(A;;FR;;;AU)", want: false},
		{name: "guests", sddl: "D:P(A;;FA;;;SY)(A;;FR;;;BG)", want: false},
		{name: "anonymous", sddl: "D:P(A;;FA;;;SY)(A;;FR;;;AN)", want: false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(fixture.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if got := protectedPrivateDescriptor(descriptor); got != fixture.want {
				t.Fatalf("protectedPrivateDescriptor(%q) = %t, want %t", fixture.sddl, got, fixture.want)
			}
		})
	}
}
