package hostfiles

import "fmt"

const maxRecursivePermissionEntries = 10_000

// Windows file access masks are kept in the platform-neutral contract so the
// Web can render basic rights without importing a Windows-only package.
const (
	WindowsAccessRead    uint32 = 0x00120089
	WindowsAccessWrite   uint32 = 0x00120116
	WindowsAccessExecute uint32 = 0x001200A0
	WindowsAccessDelete  uint32 = 0x00010000
	WindowsAccessFull    uint32 = 0x001F01FF
)

// Principal identifies an operating-system account without letting the Web
// layer interpret platform-specific user, group, or SID representations.
type Principal struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
}

// AccessRule is one Windows DACL entry. Linux permissions are represented by
// Mode because POSIX owner/group/other bits are not ordered access rules.
type AccessRule struct {
	Principal Principal `json:"principal"`
	Mask      uint32    `json:"mask"`
	Kind      string    `json:"kind"`
	Inherited bool      `json:"inherited"`
	AppliesTo string    `json:"applies_to,omitempty"`
}

type Permissions struct {
	Platform           string       `json:"platform"`
	Path               string       `json:"path"`
	Directory          bool         `json:"directory"`
	Mode               uint32       `json:"mode,omitempty"`
	Owner              Principal    `json:"owner"`
	Group              Principal    `json:"group,omitempty"`
	InheritanceEnabled bool         `json:"inheritance_enabled,omitempty"`
	Rules              []AccessRule `json:"rules,omitempty"`
}

// PermissionChange is intentionally declarative. Nil fields remain unchanged,
// which prevents stale forms from silently rewriting unrelated security data.
type PermissionChange struct {
	Mode                *uint32 `json:"mode,omitempty"`
	Recursive           bool    `json:"recursive,omitempty"`
	Owner               string  `json:"owner,omitempty"`
	ReplaceChildOwners  bool    `json:"replace_child_owners,omitempty"`
	Principal           string  `json:"principal,omitempty"`
	AccessMask          *uint32 `json:"access_mask,omitempty"`
	RemoveRule          bool    `json:"remove_rule,omitempty"`
	ApplyRuleToChildren bool    `json:"apply_rule_to_children,omitempty"`
	InheritanceEnabled  *bool   `json:"inheritance_enabled,omitempty"`
}

func (m *Manager) Permissions(path string) (Permissions, error) {
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return Permissions{}, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Permissions{}, fmt.Errorf("permissions are only available for regular files and directories")
	}
	return m.platformPermissions(target, info)
}

func (m *Manager) SetPermissions(path string, change PermissionChange) (Permissions, error) {
	target, info, err := m.resolveEntry(path)
	if err != nil {
		return Permissions{}, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Permissions{}, fmt.Errorf("permissions can only be changed for regular files and directories")
	}
	if err := m.ensureMutationAllowed(target); err != nil {
		return Permissions{}, err
	}
	if err := m.setPlatformPermissions(target, info, change); err != nil {
		return Permissions{}, err
	}
	return m.Permissions(target)
}
