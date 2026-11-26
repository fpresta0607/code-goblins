//go:build linux || darwin

package pty

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Raw puts the terminal fd names in raw mode, as a program that reads each
// key does: keys arrive one at a time as typed, unechoed, with Enter as a
// carriage return, and Ctrl-C, Ctrl-Z and Ctrl-\ as bytes rather than
// signals. With isOutputRaw, output passes as written too, as a terminal
// emulator's does; without it a newline still starts its line at the left.
// It returns what puts the terminal back as it was.
func Raw(fd int, isOutputRaw bool) (func() error, error) {
	was, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return nil, fmt.Errorf("pty: read the terminal's mode: %w", err)
	}
	raw := *was
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	if isOutputRaw {
		raw.Oflag &^= unix.OPOST
	}
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, setTermios, &raw); err != nil {
		return nil, fmt.Errorf("pty: put the terminal in raw mode: %w", err)
	}
	return func() error {
		if err := unix.IoctlSetTermios(fd, setTermios, was); err != nil {
			return fmt.Errorf("pty: put the terminal's mode back: %w", err)
		}
		return nil
	}, nil
}

// Size is the size in cells of the terminal fd names.
func Size(fd int) (cols, rows int, err error) {
	size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, fmt.Errorf("pty: read the terminal's size: %w", err)
	}
	return int(size.Col), int(size.Row), nil
}
