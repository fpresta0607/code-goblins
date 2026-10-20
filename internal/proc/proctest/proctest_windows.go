// Package proctest starts processes for the tests of what reads other
// processes by pid.
package proctest

import (
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartUnopenable starts command hidden, as a process nobody may open, not
// even to read when it started, as this user may not open a system service's
// process, and returns its pid. The test ends it through the handle its start
// returned, which holds every right whatever its DACL says. A process holding
// SeDebugPrivilege, as a CI runner's can, opens any process whatever its DACL
// says, so the privilege stays off until the test ends, and a test whose
// process opens it anyway is skipped.
func StartUnopenable(t testing.TB, command *exec.Cmd) int {
	t.Helper()
	withoutDebugPrivilege(t)
	descriptor, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		t.Fatal(err)
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:     windows.CREATE_NO_WINDOW,
		ProcessAttributes: &syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: uintptr(unsafe.Pointer(descriptor))},
	}
	err = command.Start()
	runtime.KeepAlive(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	opened, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err == nil {
		_ = windows.CloseHandle(opened)
		t.Skip("this process opens a process whose DACL denies everyone, so it cannot make one it may not open")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("opening the process whose DACL denies everyone failed with %v, want access denied", err)
	}
	return command.Process.Pid
}

// withoutDebugPrivilege disables, until the test ends, this process's
// SeDebugPrivilege.
func withoutDebugPrivilege(t testing.TB) {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = token.Close() })
	var debug windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &debug); err != nil {
		t.Fatal(err)
	}
	disabled := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: debug}}}
	var previous windows.Tokenprivileges
	var size uint32
	if err := windows.AdjustTokenPrivileges(token, false, &disabled, uint32(unsafe.Sizeof(previous)), &previous, &size); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previous.PrivilegeCount > 0 {
			_ = windows.AdjustTokenPrivileges(token, false, &previous, 0, nil, nil)
		}
	})
}
