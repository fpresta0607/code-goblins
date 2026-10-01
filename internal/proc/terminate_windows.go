package proc

import (
	"fmt"
	"syscall"
	"time"
)

const processTerminate = 0x0001

// TerminateVerified ends pid only once the process behind the one handle it
// is ended through has proved to be the one meant: created at start, to
// within the second a recorded start time is rounded to, and started with
// arguments accept takes. Checking and ending through one handle leaves no
// moment in which pid could exit and be reused by another program between
// the check and the end.
func TerminateVerified(pid int, start time.Time, accept func(arguments []string) error) error {
	handle, err := syscall.OpenProcess(processTerminate|processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %v", pid, err)
	}
	defer syscall.CloseHandle(handle)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return fmt.Errorf("read process %d's start time: %v", pid, err)
	}
	created := time.Unix(0, creation.Nanoseconds())
	if difference := created.Sub(start); difference <= -time.Second || difference >= time.Second {
		return fmt.Errorf("process %d started at %s, not %s, so it is another program", pid, created.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano))
	}

	parameters, err := parameterBlock(handle, pid)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	descriptor := make([]byte, 16)
	if err := readMemory(handle, parameters+paramsOffsetCommandLine, descriptor); err != nil {
		return fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	line, err := unicodeString(handle, pid, descriptor, 0, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	arguments, err := splitCommandLine(line)
	if err != nil {
		return err
	}
	if err := accept(arguments); err != nil {
		return err
	}
	return syscall.TerminateProcess(handle, 1)
}
