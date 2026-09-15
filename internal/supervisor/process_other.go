//go:build !windows

package supervisor

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
