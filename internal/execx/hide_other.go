//go:build !windows

package execx

import (
	"os/exec"
	"syscall"
)

// hide gives cmd attributes of its own for a caller to add to, as Windows
// does, and nothing more: only Windows opens a window for a console program.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
}

// detach does nothing: only Windows starts a console host with a program.
func detach(*exec.Cmd) {}
