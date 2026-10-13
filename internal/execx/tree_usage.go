package execx

import "time"

// TreeUsage separates cumulative job accounting from a sampled working set.
// CPU includes exited descendants. Commitment is private committed memory,
// not resident memory, and PeakCommitBytes is the kernel's job peak.
type TreeUsage struct {
	CPU                           time.Duration `json:"cpu_nanoseconds"`
	CommitBytes                   uint64        `json:"commit_bytes"`
	PeakCommitBytes               uint64        `json:"peak_commit_bytes"`
	SampledPrivateWorkingSetBytes uint64        `json:"sampled_private_working_set_bytes"`
	ActiveProcesses               uint32        `json:"active_processes"`
	TotalProcesses                uint32        `json:"total_processes"`
	// UnreadableMembers are the processes in the job whose private working
	// set this process may not read, as a sandboxed browser's, which the
	// sampled working set leaves out.
	UnreadableMembers uint32 `json:"unreadable_members,omitempty"`
}
