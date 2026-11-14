//go:build linux || darwin

package lock

import (
	"os/exec"
	"strconv"
)

// endedProgram is a program that ends at once, for a test that needs the ID
// of a process that is gone.
func endedProgram() *exec.Cmd {
	return exec.Command("true")
}

// lastingProgram is a program that runs for that many seconds and does
// nothing else, for a test that needs a live process other than itself.
func lastingProgram(seconds int) *exec.Cmd {
	return exec.Command("sleep", strconv.Itoa(seconds))
}
