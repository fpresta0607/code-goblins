package pty

import (
	"errors"
	"os/exec"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// startTree starts cmd as a tree (execx.StartTree). macOS keeps no process
// to processor threads, so a mask other than 0 is refused.
func startTree(cmd *exec.Cmd, processors uintptr) (*execx.Tree, error) {
	if processors != 0 {
		return nil, errors.New("macOS keeps no process to processor threads")
	}
	return execx.StartTree(cmd)
}
