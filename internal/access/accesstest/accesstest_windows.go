// Package accesstest makes and reads the access lists of folders a test
// makes, so a test of who may open the fleet's folders never touches a real
// one.
package accesstest

import (
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The SIDs a test of folder access names.
const (
	System             = "S-1-5-18"
	Administrators     = "S-1-5-32-544"
	AuthenticatedUsers = "S-1-5-11"
)

// Self is the SID of the account the test runs as.
func Self(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

// DriveRootLike makes a folder that lets Authenticated Users modify
// everything made in it, as a drive's root does, so folders made in it start
// as open as a home or a Dev Drive folder made at a drive's root.
func DriveRootLike(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + Self(t) + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;" + AuthenticatedUsers + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Allowed is every SID path's access list allows anything, sorted, and
// whether the list takes nothing from its folder.
func Allowed(t *testing.T, path string) ([]string, bool) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var sids []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE {
			sids = append(sids, (*windows.SID)(unsafe.Pointer(&entry.SidStart)).String())
		}
	}
	slices.Sort(sids)
	return slices.Compact(sids), control&windows.SE_DACL_PROTECTED != 0
}

// Narrowed is the sorted list of SIDs a narrowed folder allows: the test's
// own account, SYSTEM and Administrators.
func Narrowed(t *testing.T) []string {
	t.Helper()
	return slices.Sorted(slices.Values([]string{Self(t), System, Administrators}))
}
