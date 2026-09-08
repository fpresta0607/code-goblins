package pipeline

import (
	"fmt"
	"syscall"
	"unsafe"
)

// ReplaceFileW replaces atomically and the result keeps the replaced file's
// ACLs, attributes and creation time. A shared config therefore survives an
// interrupted write whole, and keeps the inherited permissions the other
// principals on the machine read it through.
func replaceFile(destination, staged string) error {
	target, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	source, err := syscall.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("ReplaceFileW")
	result, _, callErr := proc.Call(uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(source)), 0, 0, 0, 0)
	if result == 0 {
		return fmt.Errorf("pipeline: replacing %s failed: %v", destination, callErr)
	}
	return nil
}
