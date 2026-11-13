//go:build !windows && !linux

package proc

import "time"

// Identify refuses: reading what a process is is not ported yet.
func Identify(pid int, start time.Time) (Identity, error) {
	return Identity{}, notPorted("reading a process's identity")
}

// TerminateVerified refuses, as Identify does, and ends nothing.
func TerminateVerified(pid int, start time.Time, accept func(arguments []string) error) error {
	return notPorted("ending a verified process")
}

// TerminateVerifiedIn refuses, as Identify does, and ends nothing.
func TerminateVerifiedIn(pid int, start time.Time, accept func(Identity) error) error {
	return notPorted("ending a verified process")
}
