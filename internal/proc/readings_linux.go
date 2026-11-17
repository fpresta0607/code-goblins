package proc

import (
	"fmt"
	"os"
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
// them, each one whole.
func Arguments(pid int) ([]string, error) {
	arguments, err := readList(pid, "cmdline")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	return arguments, nil
}

// Environment returns the environment pid was started with: what the process
// set for itself since is in its own memory, which /proc does not show.
// Environment is private input to a scoped connection check. Callers must
// verify process ownership and must never serialize or log this value.
// /proc shows a process part-way through its exec, whose new program has no
// environment yet, as started with none, so such a process is read again
// until its exec has set the environment up, for at most a second.
func Environment(pid int) ([]string, error) {
	deadline := time.Now().Add(time.Second)
	for {
		environment, err := readList(pid, "environ")
		if err != nil {
			return nil, fmt.Errorf("process environment unavailable: %v", err)
		}
		if len(environment) > 0 || time.Now().After(deadline) {
			return environment, nil
		}
		if read, err := readStatus(pid); err != nil || !read.isEnvironmentPending {
			return environment, nil
		}
		time.Sleep(time.Millisecond)
	}
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
