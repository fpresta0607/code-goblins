package proc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// WorkingDirectory returns the directory pid is currently running in, which
// only a process of the same user, or root, may read.
func WorkingDirectory(pid int) (string, error) {
	directory, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDirectoryUnreadable, err)
	}
	return directory, nil
}

// Arguments returns the arguments pid was started with, as the kernel keeps
// them, each one whole. /proc shows a process part-way through its exec,
// whose new program has no arguments yet, as started with none, so such a
// process is read again until its exec has set them up, for at most a
// second. It also shows an exited process as having none, which is
// therefore an error.
func Arguments(pid int) ([]string, error) {
	deadline := time.Now().Add(time.Second)
	for {
		arguments, err := readList(pid, "cmdline")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
		}
		if len(arguments) > 0 {
			return arguments, nil
		}
		read, err := readStatus(pid)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
		}
		if !read.isExecSettingUp || time.Now().After(deadline) {
			// The exec may have set the arguments up after they were read
			// empty and before its line was read, so they are read once more.
			arguments, err := readList(pid, "cmdline")
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
			}
			return arguments, nil
		}
		time.Sleep(time.Millisecond)
	}
}

// Environment returns the environment pid was started with: what the process
// set for itself since is in its own memory, which /proc does not show.
// Environment is private input to a scoped connection check. Callers must
// verify process ownership and must never serialize or log this value.
// /proc shows a process part-way through its exec, whose new program has no
// environment yet, as started with none, or refuses it to its own user, so
// such a process is read again until its exec has set the environment up,
// for at most a second. It also shows an exited process as having none,
// which is therefore an error.
func Environment(pid int) ([]string, error) {
	deadline := time.Now().Add(time.Second)
	for {
		environment, readErr := readList(pid, "environ")
		if readErr == nil && len(environment) > 0 {
			return environment, nil
		}
		read, err := readStatus(pid)
		if err != nil {
			return nil, fmt.Errorf("process environment unavailable: %v", err)
		}
		// A process this one may not read shows no environment's end, so
		// only an empty reading tells an exec by it.
		isExecPossible := read.isExecSettingUp
		if readErr != nil {
			isExecPossible = errors.Is(readErr, fs.ErrPermission) && isStartingAsThisUser(pid, read)
		}
		if !isExecPossible || time.Now().After(deadline) {
			if readErr != nil {
				return nil, fmt.Errorf("process environment unavailable: %v", readErr)
			}
			// The exec may have set the environment up after it was read
			// empty and before its line was read, so it is read once more.
			environment, err := readList(pid, "environ")
			if err != nil {
				return nil, fmt.Errorf("process environment unavailable: %v", err)
			}
			return environment, nil
		}
		time.Sleep(time.Millisecond)
	}
}

// isStartingAsThisUser reports whether pid, whose line of /proc is read,
// runs as this process's user and started less than a second ago, as a
// process does whose exec still refuses its environment to its own user: an
// exec follows its fork at once, and an older refusal, or another user's,
// is for good.
func isStartingAsThisUser(pid int, read status) bool {
	uptime, err := fsx.ReadKernelFile("/proc/uptime")
	if err != nil {
		return false
	}
	lines, err := fsx.ReadKernelFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	return isStartingAs(uptime, lines, read.started, os.Getuid())
}

// isStartingAs reports whether a process started that many ticks after the
// machine, whose /proc/<pid>/status reads as lines, started less than a
// second before the machine's /proc/uptime read as uptime, and runs wholly
// as user: a setuid program keeps the user's real ID and still refuses its
// environment for good.
func isStartingAs(uptime, lines []byte, started uint64, user int) bool {
	seconds, _, _ := bytes.Cut(bytes.TrimSpace(uptime), []byte(" "))
	now, err := strconv.ParseFloat(string(seconds), 64)
	if err != nil || now*ticksPerSecond-float64(started) >= ticksPerSecond {
		return false
	}
	for line := range bytes.Lines(lines) {
		if value, isUser := bytes.CutPrefix(line, []byte("Uid:")); isUser {
			users := bytes.Fields(value)
			return len(users) > 0 && !slices.ContainsFunc(users, func(id []byte) bool { return string(id) != strconv.Itoa(user) })
		}
	}
	return false
}

// readList reads a file of /proc/<pid> that holds strings each ended by a
// zero byte.
func readList(pid int, name string) ([]string, error) {
	raw, err := fsx.ReadKernelFile(fmt.Sprintf("/proc/%d/%s", pid, name))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), nil
}
