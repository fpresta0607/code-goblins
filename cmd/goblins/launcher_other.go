//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os/exec"
)

// startDetachedAfter refuses: starting a process detached from this one is
// not ported to this system yet.
func startDetachedAfter(before func(pid int) error, environment []string, executable, dir, logPath string, args ...string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("starting %s detached is not ported to this system yet: %w", executable, errors.ErrUnsupported)
}

// openWindow finds no desktop window, which this system has none of yet, so
// the browser shows the board instead.
func openWindow(board, stateDir string, background bool) error {
	return errNoWindow
}
