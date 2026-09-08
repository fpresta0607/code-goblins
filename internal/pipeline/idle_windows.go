package pipeline

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// no-mistakes v1.48 and v1.64 lock byte 0xffffffff of daemon.lock.
// Holding the same OS lock prevents a daemon starting between the idle check
// and config replacement. Closing the handle releases it, including on crash.
func lockDaemon(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	overlap := syscall.Overlapped{Offset: 0xffffffff}
	result, _, callErr := proc.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlap)))
	if result == 0 {
		f.Close()
		return nil, fmt.Errorf("%w: daemon lock unavailable: %v", ErrBusy, callErr)
	}
	return f.Close, nil
}
