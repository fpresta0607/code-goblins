//go:build !windows

package processor

// KeepToFleetThreads changes nothing: the fleet's threads are read on Windows
// alone, so elsewhere a process keeps the threads it was started on.
func KeepToFleetThreads() error {
	return nil
}
