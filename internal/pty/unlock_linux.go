package pty

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

// unlock unlocks the terminal a /dev/ptmx descriptor opened and returns its
// device's path. Linux's devpts gives the terminal to its opener, so it needs
// no grant.
func unlock(fd int) (string, error) {
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return "", fmt.Errorf("pty: unlock the terminal: %w", err)
	}
	number, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		return "", fmt.Errorf("pty: read the terminal's number: %w", err)
	}
	return "/dev/pts/" + strconv.FormatUint(uint64(number), 10), nil
}
