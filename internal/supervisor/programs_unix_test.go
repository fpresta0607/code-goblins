//go:build linux || darwin

package supervisor

import (
	"os/exec"
	"strconv"
)

// lastingProgram is a program that runs for that many seconds and does
// nothing else, for a test that needs a live process other than itself.
func lastingProgram(seconds int) *exec.Cmd {
	return exec.Command("sleep", strconv.Itoa(seconds))
}
