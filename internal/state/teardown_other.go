//go:build !windows

package state

func pendingTeardown(processes []TeardownProcess) []TeardownProcess {
	return processes
}
