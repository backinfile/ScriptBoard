package web

import (
	"fmt"
	"net/http"
	"strings"

	"scriptboard/internal/hostfiles"
)

type linuxPermissionRowView struct {
	Label, Technical     string
	Read, Write, Execute bool
}

type windowsAccessRuleView struct {
	Name, ID, PrincipalType, Kind, AppliesTo, Summary       string
	Inherited, Editable, Full, Modify, Read, Write, Execute bool
}

type filePermissionsView struct {
	Platform, Path, Name, OwnerName, OwnerID, GroupName, GroupID string
	ModeOctal, ModeSymbolic                                      string
	Directory, InheritanceEnabled                                bool
	LinuxRows                                                    []linuxPermissionRowView
	WindowsRules                                                 []windowsAccessRuleView
}

func newFilePermissionsView(value hostfiles.Permissions) filePermissionsView {
	view := filePermissionsView{
		Platform: value.Platform, Path: value.Path, Name: hostfiles.Base(value.Path), Directory: value.Directory,
		OwnerName: value.Owner.Name, OwnerID: value.Owner.ID, GroupName: value.Group.Name, GroupID: value.Group.ID,
		InheritanceEnabled: value.InheritanceEnabled,
	}
	if value.Platform == "linux" {
		view.ModeOctal = fmt.Sprintf("%04o", value.Mode)
		view.ModeSymbolic = symbolicPermissionMode(value.Mode)
		view.LinuxRows = []linuxPermissionRowView{
			{Label: "files.permissions.owner", Technical: value.Owner.Name, Read: value.Mode&0o400 != 0, Write: value.Mode&0o200 != 0, Execute: value.Mode&0o100 != 0},
			{Label: "files.permissions.group", Technical: value.Group.Name, Read: value.Mode&0o040 != 0, Write: value.Mode&0o020 != 0, Execute: value.Mode&0o010 != 0},
			{Label: "files.permissions.others", Technical: "everyone else", Read: value.Mode&0o004 != 0, Write: value.Mode&0o002 != 0, Execute: value.Mode&0o001 != 0},
		}
		return view
	}
	view.WindowsRules = make([]windowsAccessRuleView, 0, len(value.Rules))
	for _, rule := range value.Rules {
		full := rule.Mask&hostfiles.WindowsAccessFull == hostfiles.WindowsAccessFull
		read := rule.Mask&hostfiles.WindowsAccessRead == hostfiles.WindowsAccessRead
		write := rule.Mask&hostfiles.WindowsAccessWrite == hostfiles.WindowsAccessWrite
		execute := rule.Mask&hostfiles.WindowsAccessExecute == hostfiles.WindowsAccessExecute
		modify := read && write && execute && rule.Mask&hostfiles.WindowsAccessDelete != 0
		summary := "files.permissions.special"
		switch {
		case full:
			summary = "files.permissions.full_control"
		case modify:
			summary = "files.permissions.modify"
		case read && execute:
			summary = "files.permissions.read_execute"
		case read:
			summary = "files.permissions.read"
		case write:
			summary = "files.permissions.write"
		}
		view.WindowsRules = append(view.WindowsRules, windowsAccessRuleView{
			Name: rule.Principal.Name, ID: rule.Principal.ID, PrincipalType: rule.Principal.Type, Kind: rule.Kind, AppliesTo: rule.AppliesTo, Summary: summary,
			Inherited: rule.Inherited, Editable: !rule.Inherited && rule.Kind == "allow", Full: full, Modify: modify, Read: read, Write: write, Execute: execute,
		})
	}
	return view
}

func symbolicPermissionMode(mode uint32) string {
	bits := []uint32{0o400, 0o200, 0o100, 0o040, 0o020, 0o010, 0o004, 0o002, 0o001}
	letters := []byte("rwxrwxrwx")
	for index, bit := range bits {
		if mode&bit == 0 {
			letters[index] = '-'
		}
	}
	return string(letters)
}

func (a *App) filePermissionsTask(response http.ResponseWriter, request *http.Request) {
	path, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "无法打开权限设置", err)
		return
	}
	_, canMutate, err := a.hostInfo(request.Context(), path)
	if err != nil || !canMutate {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	permissions, err := a.hostPermissions(request.Context(), path)
	if err != nil {
		writeHostFileError(response, "无法读取文件权限", err)
		return
	}
	parent, _ := hostPathParent(path)
	a.renderTaskPage(response, request, taskPageData{
		Kind: "file-permissions", Title: webText(resolveWebLocale(request), "files.permissions.title"),
		Description: webText(resolveWebLocale(request), "files.permissions.description"), BackURL: filesURL(parent),
		Action: "/resources/files/permissions", Path: path, IsDirectory: permissions.Directory, Permissions: newFilePermissionsView(permissions),
	})
}

func (a *App) setFilePermissions(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) {
		http.Error(response, "CSRF Token 无效", http.StatusForbidden)
		return
	}
	path, err := a.hostCanonicalExisting(request.Context(), request.FormValue("path"))
	if err != nil {
		writeHostFileError(response, "权限目标无效", err)
		return
	}
	current, err := a.hostPermissions(request.Context(), path)
	if err != nil {
		writeHostFileError(response, "无法读取当前权限", err)
		return
	}
	change, err := parsePermissionChange(request, current)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := a.acquireFileMutationLease(path)
	if err != nil {
		http.Error(response, "条目正在使用中："+err.Error(), http.StatusConflict)
		return
	}
	defer release()
	if _, err := a.hostSetPermissions(request.Context(), path, change); err != nil {
		writeHostFileError(response, "无法保存文件权限", err)
		return
	}
	a.recordAuditForRequest(request, "set_file_permissions", path, "succeeded")
	parent, _ := hostPathParent(path)
	http.Redirect(response, request, filesURL(parent), http.StatusSeeOther)
}

func parsePermissionChange(request *http.Request, current hostfiles.Permissions) (hostfiles.PermissionChange, error) {
	if current.Platform == "linux" {
		mode := uint32(0)
		for name, bit := range map[string]uint32{
			"owner_read": 0o400, "owner_write": 0o200, "owner_execute": 0o100,
			"group_read": 0o040, "group_write": 0o020, "group_execute": 0o010,
			"other_read": 0o004, "other_write": 0o002, "other_execute": 0o001,
		} {
			if request.FormValue(name) == "1" {
				mode |= bit
			}
		}
		return hostfiles.PermissionChange{Mode: &mode, Recursive: current.Directory && request.FormValue("recursive") == "1"}, nil
	}
	if current.Platform != "windows" {
		return hostfiles.PermissionChange{}, fmt.Errorf("unsupported permission platform")
	}
	change := hostfiles.PermissionChange{
		Owner: strings.TrimSpace(request.FormValue("owner")), ReplaceChildOwners: current.Directory && request.FormValue("replace_child_owners") == "1",
		Principal: strings.TrimSpace(request.FormValue("principal")), RemoveRule: request.FormValue("remove_rule") == "1",
		ApplyRuleToChildren: current.Directory && request.FormValue("apply_rule_to_children") == "1",
	}
	inheritance := request.FormValue("inheritance_enabled") == "1"
	change.InheritanceEnabled = &inheritance
	if change.Principal != "" && !change.RemoveRule {
		mask := uint32(0)
		if request.FormValue("full_control") == "1" {
			mask = hostfiles.WindowsAccessFull
		} else {
			if request.FormValue("read") == "1" {
				mask |= hostfiles.WindowsAccessRead
			}
			if request.FormValue("write") == "1" {
				mask |= hostfiles.WindowsAccessWrite
			}
			if request.FormValue("execute") == "1" {
				mask |= hostfiles.WindowsAccessExecute
			}
			if request.FormValue("modify") == "1" {
				mask |= hostfiles.WindowsAccessRead | hostfiles.WindowsAccessWrite | hostfiles.WindowsAccessExecute | hostfiles.WindowsAccessDelete
			}
		}
		if mask == 0 {
			return hostfiles.PermissionChange{}, fmt.Errorf("请至少选择一项访问权限")
		}
		change.AccessMask = &mask
	}
	if change.RemoveRule {
		change.ApplyRuleToChildren = false
	}
	if change.Owner == "" && change.Principal == "" && inheritance == current.InheritanceEnabled {
		return hostfiles.PermissionChange{}, fmt.Errorf("请至少修改一项权限")
	}
	return change, nil
}
