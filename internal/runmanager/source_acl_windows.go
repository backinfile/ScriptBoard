//go:build windows

package runmanager

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
	"scriptboard/internal/windowsidentity"
)

func protectOneTimeSourceForRunner(path string) error {
	// Avoid account lookup while the managed service is starting; its virtual
	// service SID is deterministic and can be derived without contacting LSA.
	sid, err := windowsidentity.ResolveSID(`NT SERVICE\ScriptBoardRunner`)
	if err != nil {
		// Portable installations do not provision the managed Runner service SID.
		return os.Chmod(path, 0o400)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	current, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_GENERIC_READ, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, current)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func protectOneTimeRunDirectory(path string) error {
	sid, err := windowsidentity.ResolveSID(`NT SERVICE\ScriptBoardRunner`)
	if err != nil {
		return nil
	}
	for _, directory := range []string{filepath.Dir(path), path} {
		descriptor, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		current, _, err := descriptor.DACL()
		if err != nil {
			return err
		}
		var pinner runtime.Pinner
		pinner.Pin(sid)
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, current)
		pinner.Unpin()
		if err != nil {
			return err
		}
		if err := windows.SetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			return err
		}
	}
	return nil
}
