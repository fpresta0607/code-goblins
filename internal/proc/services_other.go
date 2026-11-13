//go:build !windows && !linux

package proc

// runningProcesses refuses: listing the machine's processes is not ported
// yet, so ProcessTree and RunningServicesAmong refuse with it.
func runningProcesses() ([]ServiceProcess, error) {
	return nil, notPorted("listing processes")
}
