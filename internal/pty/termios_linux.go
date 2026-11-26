package pty

import "golang.org/x/sys/unix"

// The requests that read and set a terminal's mode.
const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)
