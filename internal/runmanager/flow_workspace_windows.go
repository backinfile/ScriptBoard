//go:build windows

package runmanager

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"runtime"
	"scriptboard/internal/windowsidentity"
)

func grantFlowWorkspace(path string) error {
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
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, current)
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
