package supervise

import (
	"os/exec"
	"strconv"
)

// endedProgram is a program that ends at once, for a test that needs the ID
// of a process that is gone.
func endedProgram() *exec.Cmd {
	return exec.Command("cmd", "/c", "exit 0")
}

// lastingProgram is a program that runs for about that many seconds and does
// nothing else, for a test that needs a live process other than itself.
func lastingProgram(seconds int) *exec.Cmd {
	return exec.Command("ping", "-n", strconv.Itoa(seconds), "127.0.0.1")
}
