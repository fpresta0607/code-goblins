//go:build linux || darwin

package verify

import (
	"os/exec"
	"syscall"
)

// inGroupOfItsOwn starts command in a process group of its own, which an
// interrupt reaches without reaching this process, and which its tree keeps
// what it starts in (execx.StartTree).
func inGroupOfItsOwn(command *exec.Cmd) {
	command.SysProcAttr.Setpgid = true
}

// interruptGroup sends SIGINT, as a Ctrl+C at a terminal does, to the process
// group process pid leads.
func interruptGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGINT)
}
