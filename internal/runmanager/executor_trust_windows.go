//go:build windows

package runmanager

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validateExecutorOwnership(path string, _ os.FileInfo) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect executor security descriptor: %w", err)
	}
	if descriptor == nil {
		return errors.New("executor has no security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("inspect executor owner: %w", err)
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect service identity: %w", err)
	}
	trusted := map[string]struct{}{
		tokenUser.User.Sid.String(): {},
		"S-1-5-18":                  {}, // LocalSystem
		"S-1-5-32-544":              {}, // Built-in Administrators
		"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464": {}, // TrustedInstaller
	}
	if _, ok := trusted[owner.String()]; !ok {
		return fmt.Errorf("executor is not owned by the service identity or a trusted Windows principal: %s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("inspect executor DACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("executor has no protective DACL: %s", path)
	}
	writeMask := windows.ACCESS_MASK(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES |
		windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return fmt.Errorf("inspect executor DACL entry: %w", err)
		}
		if ace == nil || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask&writeMask == 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("executor DACL contains an unsupported writable ACE type %d: %s", ace.Header.AceType, path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return fmt.Errorf("executor DACL contains an invalid SID: %s", path)
		}
		if _, ok := trusted[sid.String()]; !ok {
			return fmt.Errorf("executor is writable by an untrusted Windows principal %s: %s", sid.String(), path)
		}
	}
	return nil
}
