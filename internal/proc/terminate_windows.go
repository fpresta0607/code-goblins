package proc

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const processTerminate = 0x0001

// Identity is what a process is, read through one handle: the program image
// it runs now, which follows a rename of its file, the arguments it was
// started with, its working directory and its environment.
type Identity struct {
	Image       string
	Arguments   []string
	Directory   string
	Environment []string
}

// Getenv is the value of name in the identity's environment, empty when it
// is not set.
func (i Identity) Getenv(name string) string {
	for _, entry := range i.Environment {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// Identify reads pid's identity, and proves it is the process created at
// start, through one handle.
func Identify(pid int, start time.Time) (Identity, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return Identity{}, fmt.Errorf("open process %d: %v", pid, err)
	}
	defer syscall.CloseHandle(handle)
	return identify(handle, pid, start)
}

// TerminateVerified ends pid only once the process behind the one handle it
// is ended through has proved to be the one meant: created at start, to
// within the second a recorded start time is rounded to, and started with
// arguments accept takes.
func TerminateVerified(pid int, start time.Time, accept func(arguments []string) error) error {
	return TerminateVerifiedIn(pid, start, func(identity Identity) error { return accept(identity.Arguments) })
}

// TerminateVerifiedIn ends pid only once its identity, read through the one
// handle it is ended by, was created at start and is one accept takes.
// Checking and ending through one handle leaves no moment in which pid could
// exit and be reused by another program between the check and the end.
func TerminateVerifiedIn(pid int, start time.Time, accept func(Identity) error) error {
	handle, err := syscall.OpenProcess(processTerminate|processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %v", pid, err)
	}
	defer syscall.CloseHandle(handle)
	identity, err := identify(handle, pid, start)
	if err != nil {
		return err
	}
	if err := accept(identity); err != nil {
		return err
	}
	return syscall.TerminateProcess(handle, 1)
}

// identify proves the process behind handle was created at start and reads
// what it is.
func identify(handle syscall.Handle, pid int, start time.Time) (Identity, error) {
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return Identity{}, fmt.Errorf("read process %d's start time: %v", pid, err)
	}
	created := time.Unix(0, creation.Nanoseconds())
	if difference := created.Sub(start); difference <= -time.Second || difference >= time.Second {
		return Identity{}, fmt.Errorf("process %d started at %s, not %s, so it is another program", pid, created.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano))
	}
	var identity Identity
	image := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(windows.Handle(handle), 0, &image[0], &size); err != nil {
		return Identity{}, fmt.Errorf("read process %d's image: %v", pid, err)
	}
	identity.Image = syscall.UTF16ToString(image[:size])

	parameters, err := parameterBlock(handle, pid)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	read := func(offset uintptr) (string, error) {
		descriptor := make([]byte, 16)
		if err := readMemory(handle, parameters+offset, descriptor); err != nil {
			return "", err
		}
		return unicodeString(handle, pid, descriptor, 0, nil)
	}
	line, err := read(paramsOffsetCommandLine)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	if identity.Arguments, err = splitCommandLine(line); err != nil {
		return Identity{}, err
	}
	if identity.Directory, err = read(paramsOffsetCurrentDirectory); err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrDirectoryUnreadable, err)
	}
	address, err := readPointer(handle, parameters+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment))
	if err != nil || address == 0 {
		return Identity{}, fmt.Errorf("read process %d's environment: %v", pid, err)
	}
	if identity.Environment, err = environmentAt(handle, address); err != nil {
		return Identity{}, err
	}
	return identity, nil
}
