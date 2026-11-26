package pty

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// unlock grants and unlocks the terminal a /dev/ptmx descriptor opened and
// returns its device's path, as grantpt, unlockpt and ptsname do.
func unlock(fd int) (string, error) {
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		return "", fmt.Errorf("pty: grant the terminal: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		return "", fmt.Errorf("pty: unlock the terminal: %w", err)
	}
	// TIOCPTYGNAME writes the device's path, at most 128 bytes with its NUL.
	name := make([]byte, 128)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		return "", fmt.Errorf("pty: read the terminal's name: %w", errno)
	}
	return unix.ByteSliceToString(name), nil
}
