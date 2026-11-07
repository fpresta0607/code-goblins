package pipeline

import "syscall"

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// STILL_ACTIVE is the exit code Windows reports for a running process.
const stillActive = 259

// readAgentProcessStatus reports pid's liveness with one cheap, nonblocking
// query. A pid that names no process is dead. A query that cannot be
// answered, such as one denied for a protected process, is unknown, and the
// guard refuses an unknown rather than read it as permission.
func readAgentProcessStatus(pid int) agentProcessStatus {
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER is Windows' answer for a pid no process
		// holds. Any other error, such as ERROR_ACCESS_DENIED, leaves
		// liveness unread.
		if err == syscall.Errno(87) {
			return agentProcessDead
		}
		return agentProcessUnknown
	}
	defer syscall.CloseHandle(handle)

	// A pid can still be opened after its process exited while a handle keeps
	// the process object alive, so the exit code is read too.
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return agentProcessUnknown
	}
	if exitCode != stillActive {
		return agentProcessDead
	}
	return agentProcessAlive
}
