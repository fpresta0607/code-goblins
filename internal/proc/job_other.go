//go:build !windows

package proc

// JobProcesses refuses: a job object is a Windows thing, and its stand-in on
// this system is not ported yet.
func JobProcesses(holderPID int) ([]Entry, error) {
	return nil, notPorted("listing a job's processes")
}

// CustodyJobProcesses refuses, as JobProcesses does.
func CustodyJobProcesses(holderPID int) ([]Entry, error) {
	return nil, notPorted("listing a custody job's processes")
}

// HeldJobs would hold the job objects another process holds open, which this
// system has none of.
type HeldJobs struct{}

// HoldJobs refuses, as JobProcesses does.
func HoldJobs(holderPID int) (*HeldJobs, error) {
	return nil, notPorted("holding a process's jobs")
}

// Processes refuses, as JobProcesses does.
func (h *HeldJobs) Processes() ([]Entry, error) {
	return nil, notPorted("listing held jobs' processes")
}

// KeepOnClose refuses, as JobProcesses does.
func (h *HeldJobs) KeepOnClose() error {
	return notPorted("keeping held jobs' processes")
}

// End refuses, as JobProcesses does.
func (h *HeldJobs) End() error {
	return notPorted("ending held jobs")
}

// Close does nothing, since HoldJobs holds nothing.
func (h *HeldJobs) Close() {}
