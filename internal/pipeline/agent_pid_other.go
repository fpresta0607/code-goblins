//go:build !windows

package pipeline

import (
	"errors"
	"syscall"
)

// readAgentProcessStatus reports pid's liveness with signal 0, which asks the
// kernel about the process and delivers nothing. ESRCH is a pid no process
// holds. A permission error means the process exists but cannot be inspected,
// which stays unknown, and the guard refuses an unknown rather than read it
// as permission.
func readAgentProcessStatus(pid int) agentProcessStatus {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return agentProcessAlive
	case errors.Is(err, syscall.ESRCH):
		return agentProcessDead
	default:
		return agentProcessUnknown
	}
}
