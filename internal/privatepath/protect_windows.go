//go:build windows

package privatepath

import (
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

func ProtectDirectory(path string) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read directory owner: %w", err)
	}
	if descriptor == nil {
		return errors.New("directory has no security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read directory owner SID: %w", err)
	}
	if owner == nil {
		return errors.New("directory has no owner SID")
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current process SID: %w", err)
	}
	grants := make(map[string]struct{})
	for _, value := range []string{owner.String(), tokenUser.User.Sid.String(), "S-1-5-18", "S-1-5-32-544"} {
		grants[value] = struct{}{}
	}

	entries := make([]windows.EXPLICIT_ACCESS, 0, len(grants))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for value := range grants {
		if value == "" {
			continue
		}
		sid, err := windows.StringToSid(value)
		if err != nil {
			return fmt.Errorf("create trustee SID %q: %w", value, err)
		}
		pinner.Pin(sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build private directory ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("protect directory ACL: %w", err)
	}
	return nil
}
