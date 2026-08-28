//go:build windows

package hostfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func (m *Manager) platformPermissions(target string, info os.FileInfo) (Permissions, error) {
	descriptor, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return Permissions{}, fmt.Errorf("read Windows security descriptor: %w", err)
	}
	if descriptor == nil {
		return Permissions{}, fmt.Errorf("Windows security descriptor is unavailable")
	}
	ownerSID, _, err := descriptor.Owner()
	if err != nil {
		return Permissions{}, fmt.Errorf("read Windows owner: %w", err)
	}
	groupSID, _, err := descriptor.Group()
	if err != nil {
		return Permissions{}, fmt.Errorf("read Windows primary group: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return Permissions{}, fmt.Errorf("read Windows DACL: %w", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return Permissions{}, fmt.Errorf("read Windows security descriptor control: %w", err)
	}
	rules, err := windowsAccessRules(dacl)
	if err != nil {
		return Permissions{}, err
	}
	return Permissions{
		Platform: "windows", Path: target, Directory: info.IsDir(), Owner: windowsPrincipal(ownerSID), Group: windowsPrincipal(groupSID),
		InheritanceEnabled: control&windows.SE_DACL_PROTECTED == 0, Rules: rules,
	}, nil
}

func windowsAccessRules(dacl *windows.ACL) ([]AccessRule, error) {
	if dacl == nil {
		return nil, nil
	}
	rules := make([]AccessRule, 0, dacl.AceCount)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return nil, fmt.Errorf("read Windows DACL entry: %w", err)
		}
		kind := ""
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			kind = "allow"
		case windows.ACCESS_DENIED_ACE_TYPE:
			kind = "deny"
		default:
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		appliesTo := "this_item"
		inheritFlags := ace.Header.AceFlags & (windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
		switch inheritFlags {
		case windows.OBJECT_INHERIT_ACE:
			appliesTo = "files"
		case windows.CONTAINER_INHERIT_ACE:
			appliesTo = "folders"
		case windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE:
			appliesTo = "children"
		}
		rules = append(rules, AccessRule{
			Principal: windowsPrincipal(sid), Mask: uint32(ace.Mask), Kind: kind,
			Inherited: ace.Header.AceFlags&windows.INHERITED_ACE != 0, AppliesTo: appliesTo,
		})
	}
	return rules, nil
}

func windowsPrincipal(sid *windows.SID) Principal {
	if sid == nil {
		return Principal{}
	}
	id := sid.String()
	account, domain, accountType, err := sid.LookupAccount("")
	name := id
	if err == nil && account != "" {
		name = account
		if domain != "" {
			name = domain + `\` + account
		}
	}
	kind := "unknown"
	switch accountType {
	case windows.SidTypeUser:
		kind = "user"
	case windows.SidTypeGroup, windows.SidTypeAlias, windows.SidTypeWellKnownGroup:
		kind = "group"
	case windows.SidTypeComputer:
		kind = "computer"
	}
	return Principal{Name: name, ID: id, Type: kind}
}

func resolveWindowsPrincipal(value string) (*windows.SID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("Windows account or SID is required")
	}
	if strings.HasPrefix(strings.ToUpper(value), "S-1-") {
		sid, err := windows.StringToSid(value)
		if err != nil {
			return nil, fmt.Errorf("resolve Windows SID: %w", err)
		}
		return sid, nil
	}
	sid, _, _, err := windows.LookupSID("", value)
	if err != nil {
		return nil, fmt.Errorf("resolve Windows account: %w", err)
	}
	return sid, nil
}

func (m *Manager) setPlatformPermissions(target string, info os.FileInfo, change PermissionChange) error {
	if change.Mode != nil || change.Recursive {
		return fmt.Errorf("POSIX mode fields are not valid on Windows")
	}
	if change.ReplaceChildOwners && (!info.IsDir() || strings.TrimSpace(change.Owner) == "") {
		return fmt.Errorf("replacing child owners requires a directory and a new owner")
	}
	if change.ApplyRuleToChildren && (!info.IsDir() || strings.TrimSpace(change.Principal) == "") {
		return fmt.Errorf("applying an access rule to children requires a directory and an account")
	}
	descriptor, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return fmt.Errorf("read current Windows security descriptor: %w", err)
	}
	previousOwner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read current Windows owner: %w", err)
	}
	previousOwner, err = previousOwner.Copy()
	if err != nil {
		return fmt.Errorf("copy current Windows owner: %w", err)
	}
	previousDACL, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read current Windows DACL: %w", err)
	}
	previousControl, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read current Windows descriptor control: %w", err)
	}

	var ownerSID *windows.SID
	if strings.TrimSpace(change.Owner) != "" {
		ownerSID, err = resolveWindowsPrincipal(change.Owner)
		if err != nil {
			return err
		}
	}
	var principalSID *windows.SID
	if strings.TrimSpace(change.Principal) != "" {
		principalSID, err = resolveWindowsPrincipal(change.Principal)
		if err != nil {
			return err
		}
	}

	if ownerSID != nil {
		if err := windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, ownerSID, nil, nil, nil); err != nil {
			return fmt.Errorf("set Windows owner: %w", err)
		}
	}
	rollbackRoot := func() {
		if ownerSID != nil {
			_ = windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, previousOwner, nil, nil, nil)
		}
		information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
		if previousControl&windows.SE_DACL_PROTECTED != 0 {
			information |= windows.PROTECTED_DACL_SECURITY_INFORMATION
		} else {
			information |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
		}
		_ = windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, information, nil, nil, previousDACL, nil)
	}

	if principalSID != nil || change.InheritanceEnabled != nil {
		updatedDACL := previousDACL
		if principalSID != nil {
			mode := windows.ACCESS_MODE(windows.SET_ACCESS)
			mask := windows.ACCESS_MASK(0)
			if change.RemoveRule {
				mode = windows.ACCESS_MODE(windows.REVOKE_ACCESS)
			} else {
				if change.AccessMask == nil {
					rollbackRoot()
					return fmt.Errorf("Windows access mask is required")
				}
				mask = windows.ACCESS_MASK(*change.AccessMask)
			}
			inheritance := uint32(windows.NO_INHERITANCE)
			if change.ApplyRuleToChildren {
				inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
			}
			var pinner runtime.Pinner
			pinner.Pin(principalSID)
			entry := windows.EXPLICIT_ACCESS{
				AccessPermissions: mask, AccessMode: mode, Inheritance: inheritance,
				Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(principalSID)},
			}
			updatedDACL, err = windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, previousDACL)
			pinner.Unpin()
			if err != nil {
				rollbackRoot()
				return fmt.Errorf("build Windows DACL: %w", err)
			}
		}
		information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
		if change.InheritanceEnabled != nil {
			if *change.InheritanceEnabled {
				information |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
			} else {
				information |= windows.PROTECTED_DACL_SECURITY_INFORMATION
			}
		}
		if err := windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, information, nil, nil, updatedDACL, nil); err != nil {
			rollbackRoot()
			return fmt.Errorf("set Windows DACL: %w", err)
		}
	}

	if ownerSID != nil && change.ReplaceChildOwners {
		if err := m.replaceWindowsChildOwners(target, ownerSID); err != nil {
			rollbackRoot()
			return err
		}
	}
	return nil
}

func (m *Manager) replaceWindowsChildOwners(root string, owner *windows.SID) error {
	paths := make([]string, 0)
	previous := make([]*windows.SID, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if len(paths) >= maxRecursivePermissionEntries {
			return fmt.Errorf("recursive owner change exceeds %d entries", maxRecursivePermissionEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if restrictedEntry(path, info) || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("recursive owner change encountered a restricted entry")
		}
		if _, _, err := m.resolveEntry(path); err != nil {
			return err
		}
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil || descriptor == nil {
			return fmt.Errorf("read child owner: %w", err)
		}
		current, _, err := descriptor.Owner()
		if err != nil {
			return err
		}
		current, err = current.Copy()
		if err != nil {
			return err
		}
		paths = append(paths, path)
		previous = append(previous, current)
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := windows.SetNamedSecurityInfo(paths[index], windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); err != nil {
			for rollback := index + 1; rollback < len(paths); rollback++ {
				_ = windows.SetNamedSecurityInfo(paths[rollback], windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, previous[rollback], nil, nil, nil)
			}
			return fmt.Errorf("set child owner: %w", err)
		}
	}
	return nil
}
