//go:build windows

package hostfiles

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCopyPlatformMetadataPreservesExplicitWindowsSecurityAcrossInheritance(t *testing.T) {
	root := t.TempDir()
	sourceParent := filepath.Join(root, "source-parent")
	destinationParent := filepath.Join(root, "destination-parent")
	if err := os.MkdirAll(sourceParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationParent, 0o700); err != nil {
		t.Fatal(err)
	}
	addInheritableWorldRead(t, sourceParent)

	source := filepath.Join(sourceParent, "source.txt")
	destination := filepath.Join(destinationParent, "destination.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyPlatformMetadata(source, destination); err != nil {
		t.Fatal(err)
	}

	sourceDescriptor := windowsSecurityDescriptor(t, source)
	destinationDescriptor := windowsSecurityDescriptor(t, destination)
	if sourceDescriptor.String() == destinationDescriptor.String() {
		t.Fatal("fixture did not produce different inherited ACLs")
	}
	equal, err := equivalentWindowsSecurityMetadata(sourceDescriptor, destinationDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatalf("explicit security metadata differs\nsource:      %s\ndestination: %s", sourceDescriptor.String(), destinationDescriptor.String())
	}
}

func addInheritableWorldRead(t *testing.T, path string) {
	t.Helper()

	descriptor := windowsSecurityDescriptor(t, path)
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(world),
		},
	}}, dacl)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, updated, nil); err != nil {
		t.Fatal(err)
	}
}

func windowsSecurityDescriptor(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}
