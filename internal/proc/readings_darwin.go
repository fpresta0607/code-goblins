package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// WorkingDirectory returns the directory pid is currently running in, which
// only a process of the same user, or root, may read.
func WorkingDirectory(pid int) (string, error) {
	directory, err := workingDirectoryOf(pid)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDirectoryUnreadable, err)
	}
	return directory, nil
}

// Arguments returns the arguments pid was started with, as the kernel keeps
// them, each one whole.
func Arguments(pid int) ([]string, error) {
	arguments, _, err := startedWith(pid)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	return arguments, nil
}

// Environment returns the environment pid was started with: what the process
// set for itself since is in its own memory, which the kernel does not show.
// Environment is private input to a scoped connection check. Callers must
// verify process ownership and must never serialize or log this value.
func Environment(pid int) ([]string, error) {
	_, environment, err := startedWith(pid)
	if err != nil {
		return nil, fmt.Errorf("process environment unavailable: %v", err)
	}
	return environment, nil
}

// startedWith reads the arguments and the environment pid was started with
// (kern.procargs2), which only a process of the same user, or root, may
// read.
func startedWith(pid int) (arguments, environment []string, err error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, nil, err
	}
	return parseStartedWith(raw)
}

// parseStartedWith reads what kern.procargs2 gives: the count of arguments
// as a 32-bit number, the path the program was started from, the zero bytes
// that pad it, and then the arguments and the environment, each string ended
// by a zero byte. The environment ends at the first empty string or at the
// end of what was read.
func parseStartedWith(raw []byte) (arguments, environment []string, err error) {
	if len(raw) < 4 {
		return nil, nil, errors.New("its arguments are cut short")
	}
	count := int(binary.NativeEndian.Uint32(raw))
	rest := raw[4:]
	pathEnd := bytes.IndexByte(rest, 0)
	if pathEnd < 0 {
		return nil, nil, errors.New("its arguments name no program")
	}
	rest = bytes.TrimLeft(rest[pathEnd:], "\x00")
	values := bytes.Split(rest, []byte{0})
	if len(values) < count {
		return nil, nil, fmt.Errorf("it names %d arguments and holds %d", count, len(values))
	}
	for _, argument := range values[:count] {
		arguments = append(arguments, string(argument))
	}
	for _, variable := range values[count:] {
		if len(variable) == 0 {
			break
		}
		environment = append(environment, string(variable))
	}
	return arguments, environment, nil
}
