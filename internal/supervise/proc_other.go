//go:build !windows

package supervise

// pidAlive reads every pid as not alive, as an unverifiable process does on
// Windows: checking that a process runs is not ported to this system yet.
func pidAlive(pid int) bool { return false }
