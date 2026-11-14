package custody

import (
	"os/exec"
	"strconv"
)

// lastingProgram is a program that runs for about that many seconds and does
// nothing else, for a test that needs a live process other than itself.
func lastingProgram(seconds int) *exec.Cmd {
	return exec.Command("ping", "-n", strconv.Itoa(seconds), "127.0.0.1")
}
