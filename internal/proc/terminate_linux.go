package proc

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Identify reads pid's identity, and proves it is the process created at
// start, through one handle.
func Identify(pid int, start time.Time) (Identity, error) {
	handle, err := hold(pid, start)
	if err != nil {
		return Identity{}, err
	}
	defer unix.Close(handle)
	return identify(handle, pid)
}

// TerminateVerified ends pid only once the process behind the one handle it
// is ended through has proved to be the one meant: created at start, to
// within the second a recorded start time is rounded to, and started with
// arguments accept takes.
func TerminateVerified(pid int, start time.Time, accept func(arguments []string) error) error {
	return TerminateVerifiedIn(pid, start, func(identity Identity) error { return accept(identity.Arguments) })
}

// TerminateVerifiedIn ends pid only once its identity, read while the one
// handle it is ended by held it, was created at start and is one accept
// takes. The handle names the process and never its ID, so if pid exits and
// another program takes the ID between the check and the end, the end
// reaches nothing.
func TerminateVerifiedIn(pid int, start time.Time, accept func(Identity) error) error {
	handle, err := hold(pid, start)
	if err != nil {
		return err
	}
	defer unix.Close(handle)
	identity, err := identify(handle, pid)
	if err != nil {
		return err
	}
	if err := accept(identity); err != nil {
		return err
	}
	return unix.PidfdSendSignal(handle, unix.SIGKILL, nil, 0)
}

// Signal sends signal to pid only once the process behind the one handle it
// is sent through has proved to be the one created at start, to within the
// second a recorded start time is rounded to, so a program that took the ID
// since is never reached.
func Signal(pid int, start time.Time, signal syscall.Signal) error {
	handle, err := hold(pid, start)
	if err != nil {
		return err
	}
	defer unix.Close(handle)
	return unix.PidfdSendSignal(handle, signal, nil, 0)
}

// hold opens a handle on the process that has pid now and proves that
// process was created at start, to within the second a recorded start time
// is rounded to. The handle is opened first: the start read after it is the
// start of the process the handle names, or of a later one that took its ID,
// whose start is later than any recorded before the call.
func hold(pid int, start time.Time) (int, error) {
	handle, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return -1, fmt.Errorf("open process %d: %v", pid, err)
	}
	entry, err := Lookup(pid)
	if err != nil {
		unix.Close(handle)
		return -1, fmt.Errorf("read process %d's start time: %v", pid, err)
	}
	if difference := entry.Start.Sub(start); difference <= -time.Second || difference >= time.Second {
		unix.Close(handle)
		return -1, fmt.Errorf("process %d started at %s, not %s, so it is another program", pid, entry.Start.Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano))
	}
	return handle, nil
}

// identify reads what the process behind handle is from /proc, which names
// it by its ID, and then proves the process still ran: while it does no
// other program can have its ID, so everything read was its own.
func identify(handle, pid int) (Identity, error) {
	var identity Identity
	var err error
	if identity.Image, err = os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err != nil {
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
	if err := unix.PidfdSendSignal(handle, 0, nil, 0); err != nil {
		return Identity{}, fmt.Errorf("process %d ended while it was read: %v", pid, err)
	}
	return identity, nil
}
