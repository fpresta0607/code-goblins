//go:build !cgo

package proc

import (
	"errors"
	"fmt"
)

// A macOS build without cgo, such as one cross-built from another system,
// still builds, and each reading that goes through libproc refuses with
// what it lacks: the lock then records no holder, and a process is never
// ended unchecked.

func startTicks(pid int) (uint64, error) {
	return 0, withoutCgo("a process's start in ticks")
}

func useOf(pid int) (Use, error) {
	return Use{}, withoutCgo("what a process uses")
}

func workingDirectoryOf(pid int) (string, error) {
	return "", withoutCgo("a process's working directory")
}

func imageOf(pid int) (string, error) {
	return "", withoutCgo("a process's image")
}

// withoutCgo is the error of a reading that needs libproc in a build
// without cgo.
func withoutCgo(what string) error {
	return fmt.Errorf("proc: reading %s on macOS needs a build with cgo: %w", what, errors.ErrUnsupported)
}
