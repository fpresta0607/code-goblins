//go:build !windows

package processor

// KeepToFleetThreads changes nothing: off Windows a process keeps the
// threads it was started on, and on Linux a goblin's terminal is kept to the
// fleet's threads by the host that starts it.
func KeepToFleetThreads() error {
	return nil
}
