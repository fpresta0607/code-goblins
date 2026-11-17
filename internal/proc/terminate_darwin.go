package proc

import (
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// macOS has no handle that names a process rather than its ID, as a pidfd
// does on Linux. A process is held instead by its Instance, which names one
// run of a program: while a reading made after another gives the same
// instance, the process ran throughout, so no other program had its ID and
// everything read in between was its own.

// Identify reads pid's identity, and proves it is the process created at
// start.
func Identify(pid int, start time.Time) (Identity, error) {
	instance, err := hold(pid, start)
	if err != nil {
		return Identity{}, err
	}
	return identify(pid, instance)
}

// TerminateVerified ends pid only once the process has proved to be the one
// meant: created at start, to within the second a recorded start time is
// rounded to, and started with arguments accept takes.
func TerminateVerified(pid int, start time.Time, accept func(arguments []string) error) error {
	return TerminateVerifiedIn(pid, start, func(identity Identity) error { return accept(identity.Arguments) })
}

// TerminateVerifiedIn ends pid only once its identity was created at start
// and is one accept takes. The end goes to the ID, after the process's
// instance is read once more, so a few microseconds stay in which the
// process could exit and another program take its ID. A child this process
// started and has not collected keeps its ID throughout, so it cannot be
// mistaken that way.
func TerminateVerifiedIn(pid int, start time.Time, accept func(Identity) error) error {
	instance, err := hold(pid, start)
	if err != nil {
		return err
	}
	identity, err := identify(pid, instance)
	if err != nil {
		return err
	}
	if err := accept(identity); err != nil {
		return err
	}
	if err := stillRuns(pid, instance); err != nil {
		return err
	}
	return unix.Kill(pid, unix.SIGKILL)
}

// Signal sends signal to pid only once the process has proved to be the one
// created at start, to within the second a recorded start time is rounded
// to. The signal goes to the ID, so a few microseconds stay in which the
// process could exit and another program take its ID, as in
// TerminateVerifiedIn.
func Signal(pid int, start time.Time, signal syscall.Signal) error {
	if _, err := hold(pid, start); err != nil {
		return err
	}
	return unix.Kill(pid, signal)
}

// hold reads the instance of the process that has pid now and proves that
// process was created at start, to within the second a recorded start time
// is rounded to. The instance is read first: the start read after it is the
// start of the process it names, or of a later one that took its ID, whose
// start is later than any recorded before the call.
func hold(pid int, start time.Time) (string, error) {
	instance, err := Instance(pid)
	if err != nil {
		return "", fmt.Errorf("read process %d: %v", pid, err)
	}
	entry, err := Lookup(pid)
	if err != nil {
		return "", fmt.Errorf("read process %d's start time: %v", pid, err)
	}
	if difference := entry.Start.Sub(start); difference <= -time.Second || difference >= time.Second {
		return "", fmt.Errorf("process %d started at %s, not %s, so it is another program", pid, entry.Start.Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano))
	}
	return instance, nil
}

// identify reads what pid is, and then proves the process instance names
// still ran, so everything read was its own.
func identify(pid int, instance string) (Identity, error) {
	var identity Identity
	var err error
	if identity.Image, err = imageOf(pid); err != nil {
		return Identity{}, fmt.Errorf("read process %d's image: %v", pid, err)
	}
	if identity.Arguments, err = Arguments(pid); err != nil {
		return Identity{}, err
	}
	if identity.Directory, err = WorkingDirectory(pid); err != nil {
		return Identity{}, err
	}
	if identity.Environment, err = Environment(pid); err != nil {
		return Identity{}, err
	}
	if err := stillRuns(pid, instance); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

// stillRuns proves the process that has pid is still instance.
func stillRuns(pid int, instance string) error {
	now, err := Instance(pid)
	if err != nil {
		return fmt.Errorf("process %d could not be read again: %v", pid, err)
	}
	if now != instance {
		return fmt.Errorf("process %d ended while it was read", pid)
	}
	return nil
}
