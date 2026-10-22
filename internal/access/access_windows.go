// Package access keeps the fleet's folders to the accounts their work needs:
// the person the fleet runs as, SYSTEM and Administrators. A folder made at
// a drive's root, or under one, takes Authenticated Users Modify from it, so
// any second account on the machine could read and write it.
package access

import (
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The accounts every narrowed folder admits beside the one the fleet runs
// as, by their well-known SIDs.
const (
	systemSID         = "S-1-5-18"
	administratorsSID = "S-1-5-32-544"
)

// fileAllAccess is FILE_ALL_ACCESS, the full control an access list's FA
// stands for.
const fileAllAccess = 0x1F01FF

// Limit gives path, and everything made in it, to the accounts sids name
// alone, each with full control: its access list names no one else and
// inherits nothing from the folder above it. Windows hands the new list on
// to everything already in path that takes its access from its folder, and
// never through a link to what the link points at.
func Limit(path string, sids ...string) error {
	dacl, err := fullControl(sids)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// Narrow limits path to SYSTEM, Administrators and the account this process
// runs as. Windows walks everything in path to hand the list on, which takes
// minutes for a folder holding hundreds of thousands of files.
func Narrow(path string) error {
	sids, err := narrowed()
	if err != nil {
		return err
	}
	return Limit(path, sids...)
}

// IsNarrow reports whether path is limited to SYSTEM, Administrators and the
// account this process runs as already, reading only path itself.
func IsNarrow(path string) (bool, error) {
	sids, err := narrowed()
	if err != nil {
		return false, err
	}
	return isLimited(path, sids)
}

// narrowed are the SIDs a narrowed folder admits.
func narrowed() ([]string, error) {
	self, err := currentUser()
	if err != nil {
		return nil, err
	}
	return []string{systemSID, administratorsSID, self}, nil
}

// currentUser is the SID of the account this process runs as.
func currentUser() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// fullControl is the access list that allows each of sids full control of a
// folder and everything made in it.
func fullControl(sids []string) (*windows.ACL, error) {
	var sddl strings.Builder
	sddl.WriteString("D:P")
	for _, sid := range sids {
		sddl.WriteString("(A;OICI;FA;;;" + sid + ")")
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl.String())
	if err != nil {
		return nil, err
	}
	dacl, _, err := descriptor.DACL()
	return dacl, err
}

// isLimited reports whether path's access list takes nothing from its
// folder and allows sids, and no one else, full control of it and of
// everything made in it.
func isLimited(path string, sids []string) (bool, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || int(dacl.AceCount) != len(sids) {
		return false, err
	}
	var allowed []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			return false, err
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE || entry.Mask != fileAllAccess {
			return false, nil
		}
		allowed = append(allowed, (*windows.SID)(unsafe.Pointer(&entry.SidStart)).String())
	}
	slices.Sort(allowed)
	return slices.Equal(allowed, slices.Sorted(slices.Values(sids))), nil
}
