//go:build !windows

package execx

import "os/exec"

// Tree is a started process. Only on Windows is it the process with
// everything it starts: elsewhere it is the process alone.
type Tree struct{ cmd *exec.Cmd }

// StartTree starts cmd.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{cmd: cmd}, nil
}

// End ends the process, and names nothing it ended.
func (t *Tree) End() ([]EndedProcess, error) {
	_ = t.cmd.Process.Kill()
	return nil, nil
}

// Release does nothing: the process is no part of anything to let go of.
func (*Tree) Release() error { return nil }
