package supervise

import "syscall"

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// STILL_ACTIVE is the exit code Windows reports for a running process.
const stillActive = 259

// pidAlive reports whether pid currently identifies a running process. This
// is a plain existence check, not the full identity verification
// lock.Info.Alive performs: the epoch ledger records only owner_pid, with no
// creation time to defend against PID reuse, so there is nothing here to
// match a creation time against. It is enough to tell a fresh rewake
// stamped by a still-running auto-arm from one whose author has since
// exited, which is pidAlive's only caller.
//
// An unverifiable process (query denied, or already gone) reads as NOT
// alive. That is the opposite of lock.Info.Alive's fail-closed-toward-not-
// stealing default, and deliberately so: a health predicate that cannot
// prove a pid is running must not count as proof that recovery is under
// way.
func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}
