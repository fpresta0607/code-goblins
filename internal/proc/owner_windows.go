package proc

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OwnerOf reads whose pid is, and proves it is the process created at start,
// through one handle.
func OwnerOf(pid int, start time.Time) (Owner, error) {
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return Owner{}, fmt.Errorf("open process %d: %v", pid, err)
	}
	defer syscall.CloseHandle(handle)
	if err := createdAt(handle, pid, start); err != nil {
		return Owner{}, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.Handle(handle), windows.TOKEN_QUERY, &token); err != nil {
		return Owner{}, fmt.Errorf("open process %d's token: %v", pid, err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return Owner{}, fmt.Errorf("read process %d's user: %v", pid, err)
	}
	var session, size uint32
	if err := windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session)), &size); err != nil {
		return Owner{}, fmt.Errorf("read process %d's session: %v", pid, err)
	}
	return Owner{User: user.User.Sid.String(), Session: session}, nil
}
