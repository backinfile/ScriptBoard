//go:build windows

package hostfiles

import (
	"bytes"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func copyPlatformMetadata(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(sourcePointer)
	if err != nil {
		return err
	}
	const preserved = windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED | windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_TEMPORARY
	current, err := windows.GetFileAttributes(destinationPointer)
	if err != nil {
		return err
	}
	if err := windows.SetFileAttributes(destinationPointer, current&^preserved|attributes&preserved); err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(source, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read source security descriptor: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read source owner: %w", err)
	}
	group, _, err := descriptor.Group()
	if err != nil {
		return fmt.Errorf("read source group: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read source DACL: %w", err)
	}
	securityInformation := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION)
	if control, _, controlErr := descriptor.Control(); controlErr == nil && control&windows.SE_DACL_PROTECTED != 0 {
		securityInformation |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(destination, windows.SE_FILE_OBJECT, securityInformation, owner, group, dacl, nil); err != nil {
		return fmt.Errorf("copy security descriptor: %w", err)
	}
	return nil
}

func verifyCopiedMetadata(source, destination string, expected moveManifestEntry) error {
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != expected.mode.Perm() {
		return errorsNewMetadata("permissions")
	}
	if delta := info.ModTime().Sub(expected.modified); delta < -2*time.Second || delta > 2*time.Second {
		return errorsNewMetadata("modified time")
	}
	sourceAttributes, err := fileAttributes(source)
	if err != nil {
		return err
	}
	destinationAttributes, err := fileAttributes(destination)
	if err != nil {
		return err
	}
	const compared = windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED | windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_TEMPORARY
	if sourceAttributes&compared != destinationAttributes&compared {
		return errorsNewMetadata("Windows attributes")
	}
	sourceDescriptor, err := windows.GetNamedSecurityInfo(source, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	destinationDescriptor, err := windows.GetNamedSecurityInfo(destination, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	equal, err := equivalentWindowsSecurityMetadata(sourceDescriptor, destinationDescriptor)
	if err != nil {
		return err
	}
	if !equal {
		return errorsNewMetadata("Windows security descriptor")
	}
	return nil
}

func equivalentWindowsSecurityMetadata(source, destination *windows.SECURITY_DESCRIPTOR) (bool, error) {
	sourceOwner, _, err := source.Owner()
	if err != nil {
		return false, fmt.Errorf("read source owner: %w", err)
	}
	destinationOwner, _, err := destination.Owner()
	if err != nil {
		return false, fmt.Errorf("read destination owner: %w", err)
	}
	if !sourceOwner.Equals(destinationOwner) {
		return false, nil
	}
	sourceGroup, _, err := source.Group()
	if err != nil {
		return false, fmt.Errorf("read source group: %w", err)
	}
	destinationGroup, _, err := destination.Group()
	if err != nil {
		return false, fmt.Errorf("read destination group: %w", err)
	}
	if !sourceGroup.Equals(destinationGroup) {
		return false, nil
	}
	sourceControl, _, err := source.Control()
	if err != nil {
		return false, fmt.Errorf("read source security descriptor control: %w", err)
	}
	destinationControl, _, err := destination.Control()
	if err != nil {
		return false, fmt.Errorf("read destination security descriptor control: %w", err)
	}
	const comparedControl = windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED
	if sourceControl&comparedControl != destinationControl&comparedControl {
		return false, nil
	}
	sourceDACL, _, err := source.DACL()
	if err != nil {
		return false, fmt.Errorf("read source DACL: %w", err)
	}
	destinationDACL, _, err := destination.DACL()
	if err != nil {
		return false, fmt.Errorf("read destination DACL: %w", err)
	}
	sourceEntries, err := explicitWindowsACEs(sourceDACL)
	if err != nil {
		return false, fmt.Errorf("read source DACL entries: %w", err)
	}
	destinationEntries, err := explicitWindowsACEs(destinationDACL)
	if err != nil {
		return false, fmt.Errorf("read destination DACL entries: %w", err)
	}
	if len(sourceEntries) != len(destinationEntries) {
		return false, nil
	}
	for index := range sourceEntries {
		if !bytes.Equal(sourceEntries[index], destinationEntries[index]) {
			return false, nil
		}
	}
	return true, nil
}

func explicitWindowsACEs(dacl *windows.ACL) ([][]byte, error) {
	if dacl == nil {
		return nil, nil
	}
	entries := make([][]byte, 0, dacl.AceCount)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			return nil, err
		}
		if entry.Header.AceFlags&windows.INHERITED_ACE != 0 {
			continue
		}
		entryBytes := unsafe.Slice((*byte)(unsafe.Pointer(entry)), int(entry.Header.AceSize))
		entries = append(entries, append([]byte(nil), entryBytes...))
	}
	return entries, nil
}

func fileAttributes(path string) (uint32, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.GetFileAttributes(pointer)
}

func errorsNewMetadata(name string) error {
	return fmt.Errorf("copied %s does not match source", name)
}
