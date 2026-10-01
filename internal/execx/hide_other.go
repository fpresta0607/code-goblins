//go:build !windows

package execx

import "os/exec"

// hide does nothing: only Windows opens a window for a console program.
func hide(*exec.Cmd) {}
