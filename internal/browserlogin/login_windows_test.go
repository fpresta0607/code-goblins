package browserlogin

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestALoginsProfileIsReadableByItsOwnerAlone(t *testing.T) {
	stateDir := t.TempDir()

	path, err := MakeProfile(stateDir, "stripe-precisiondocs-ai")
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(stateDir, "browser-logins", "stripe-precisiondocs-ai") {
		t.Fatalf("profile = %s, want it under state/browser-logins", path)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("profile DACL = %s, want one protected entry for the current user", descriptor)
	}
	var entry *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &entry); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
	if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(user.User.Sid) || entry.Mask&windows.FILE_GENERIC_READ != windows.FILE_GENERIC_READ || entry.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
		t.Fatalf("profile DACL = %s, want the current user's entry inherited by its files and folders", descriptor)
	}
	// Making it again keeps it.
	if again, err := MakeProfile(stateDir, "stripe-precisiondocs-ai"); err != nil || again != path {
		t.Fatalf("MakeProfile again = (%s, %v), want the same folder", again, err)
	}
}
