//go:build windows

package memorysettings

import (
	"golang.org/x/sys/windows"
	"runtime"
	"scriptboard/internal/windowsidentity"
	"unsafe"
)

func shareWithRunner(root string) error {
	sid, err := windowsidentity.ResolveSID(`NT SERVICE\ScriptBoardRunner`)
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(Path(root), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	current, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	// Existing service grants need no WRITE_DAC permission on later starts.
	if current != nil {
		for i := uint32(0); i < uint32(current.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(current, i, &ace); err != nil {
				return err
			}
			if ace != nil && ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 && ace.Mask&windows.FILE_GENERIC_READ == windows.FILE_GENERIC_READ {
				trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
				if trustee.Equals(sid) {
					return nil
				}
			}
		}
	}
	var pin runtime.Pinner
	pin.Pin(sid)
	defer pin.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.FILE_GENERIC_READ, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, current)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(Path(root), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
