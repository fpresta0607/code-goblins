package supervisor

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	advapi32                = syscall.NewLazyDLL("advapi32.dll")
	procGetSecurityInfo     = advapi32.NewProc("GetSecurityInfo")
	procConvertSDToSDDL     = advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	procLocalFree           = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
	seKernelObject          = uintptr(6)
	daclSecurityInformation = uintptr(4)
	sddlRevision1           = uintptr(1)
)

// The pipe grants the current user, and nobody else, the right to connect or
// to add an instance of their own.
func TestRunPipeGrantsOnlyTheCurrentUser(t *testing.T) {
	store, h := testStore(t)
	runPipe(t, &Service{Store: store})
	conn := dialRunPipe(t, h.State)
	defer conn.Close()

	var descriptor uintptr
	if code, _, _ := procGetSecurityInfo.Call(conn.Fd(), seKernelObject, daclSecurityInformation, 0, 0, 0, 0, uintptr(unsafe.Pointer(&descriptor))); code != 0 {
		t.Fatalf("GetSecurityInfo = %d", code)
	}
	defer procLocalFree.Call(descriptor)
	var text *uint16
	if ok, _, err := procConvertSDToSDDL.Call(descriptor, sddlRevision1, daclSecurityInformation, uintptr(unsafe.Pointer(&text)), 0); ok == 0 {
		t.Fatalf("the pipe's descriptor could not be read: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(text)))
	sddl := syscall.UTF16ToString(unsafe.Slice(text, 1024))

	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sddl, "(") != 1 || !strings.Contains(sddl, ";;;"+sid+")") || !strings.HasPrefix(sddl, "D:P") {
		t.Fatalf("pipe DACL = %s, want one protected entry for %s alone", sddl, sid)
	}
}
