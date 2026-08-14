//go:build windows

package privatepath

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

func ProtectDirectory(path string) error {
	// The elevated installer owns ACL construction for managed services. Their
	// low-privilege runtime tokens deliberately lack WRITE_DAC and, on some
	// Windows hosts, cannot even query the protected external-secrets DACL.
	// Only an SCM-issued ScriptBoard service SID may rely on that installation
	// boundary; ordinary LocalService processes do not bypass hardening.
	if managedScriptBoardServiceToken() {
		return nil
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		// A managed service can have READ_CONTROL through its per-service SID
		// while Windows still refuses the combined owner query. The DACL alone
		// contains everything needed to recognize an installer-hardened path.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			daclDescriptor, daclErr := windows.GetNamedSecurityInfo(
				path,
				windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION,
			)
			if daclErr == nil && protectedPrivateDescriptor(daclDescriptor) {
				return nil
			}
		}
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
		// Managed services intentionally receive file modification rights but
		// not WRITE_DAC. Accept an installer-hardened directory only after
		// verifying that inheritance is blocked and no broad trustee is present.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) && protectedPrivateDescriptor(descriptor) {
			return nil
		}
		return fmt.Errorf("protect directory ACL: %w", err)
	}
	return nil
}

func managedScriptBoardServiceToken() bool {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return false
	}
	localService, err := windows.StringToSid("S-1-5-19")
	if err != nil || !user.User.Sid.Equals(localService) {
		return false
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return false
	}
	allowed := make([]*windows.SID, 0, 3)
	for _, account := range []string{`NT SERVICE\ScriptBoard`, `NT SERVICE\ScriptBoardAI`, `NT SERVICE\ScriptBoardRunner`} {
		sid, _, _, lookupErr := windows.LookupSID("", account)
		if lookupErr == nil {
			allowed = append(allowed, sid)
		}
	}
	for _, group := range groups.AllGroups() {
		if group.Sid == nil || group.Attributes&windows.SE_GROUP_ENABLED == 0 {
			continue
		}
		for _, sid := range allowed {
			if group.Sid.Equals(sid) {
				return true
			}
		}
	}
	return false
}

func protectedPrivateDescriptor(descriptor *windows.SECURITY_DESCRIPTOR) bool {
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	sddl := descriptor.String()
	for _, broadTrustee := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;BG)", ";;;AN)"} {
		if strings.Contains(sddl, broadTrustee) {
			return false
		}
	}
	return true
}
