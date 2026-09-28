package lifecycle

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func Inventory(ctx context.Context, directories []string, hosts []Identity) ([]Process, error) {
	entries, err := proc.Processes()
	if err != nil {
		return nil, err
	}
	ancestors, err := proc.Ancestry(os.Getpid(), 64)
	if err != nil {
		return nil, fmt.Errorf("identify lifecycle controller: %w", err)
	}
	var jobs []Identity
	for _, identity := range hosts {
		started, exists := proc.StartTime(identity.PID)
		if !exists || !started.Equal(identity.Started) {
			continue
		}
		members, err := proc.JobProcesses(identity.PID)
		if err != nil {
			return nil, fmt.Errorf("read task host %d job: %w", identity.PID, err)
		}
		if current, exists := proc.StartTime(identity.PID); !exists || !current.Equal(identity.Started) {
			continue
		}
		jobs = append(jobs, identity)
		for _, member := range members {
			jobs = append(jobs, Identity{PID: member.PID, Started: member.Start})
		}
	}
	var processes []Process
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		started, exists := proc.StartTime(entry.PID)
		if !exists {
			continue
		}
		entry.Start = started
		if entry.PID == os.Getpid() || slices.ContainsFunc(ancestors, func(ancestor proc.Entry) bool { return ancestor.PID == entry.PID && ancestor.Start.Equal(entry.Start) }) {
			continue
		}
		directory, _ := proc.WorkingDirectory(entry.PID)
		arguments, _ := proc.Arguments(entry.PID)
		// Each read opens a PID again, so discard a process replaced while its
		// evidence was collected. Terminate checks the same identity on a handle.
		if started, exists := proc.StartTime(entry.PID); !exists || !started.Equal(entry.Start) {
			continue
		}
		processes = append(processes, Process{PID: entry.PID, ParentPID: entry.ParentPID, Name: entry.ExeBase, Started: entry.Start, Directory: directory, Arguments: arguments})
	}
	return OwnedProcesses(processes, directories, jobs), nil
}
