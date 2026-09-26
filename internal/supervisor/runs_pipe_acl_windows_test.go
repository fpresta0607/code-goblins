package supervisor

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The pipe grants the current user, and nobody else, the right to connect or
// to add an instance of their own.
func TestRunPipeGrantsOnlyTheCurrentUser(t *testing.T) {
	store, h := testStore(t)
	runPipe(t, &Service{Store: store})
	conn := dialRunPipe(t, h.State)
	defer conn.Close()

	descriptor, err := windows.GetSecurityInfo(windows.Handle(conn.Fd()), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
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
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl.AceCount != 1 {
		t.Fatalf("pipe DACL = %s, want one protected entry for %s alone", descriptor, user.User.Sid)
	}
	var entry *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &entry); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
	if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(user.User.Sid) {
		t.Fatalf("pipe DACL = %s, want one protected entry for %s alone", descriptor, user.User.Sid)
	}
}
