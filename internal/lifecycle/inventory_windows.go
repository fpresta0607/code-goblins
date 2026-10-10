package lifecycle

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Reading a process's directory and arguments waits on its address space, and
// on a busy host each read takes milliseconds, so one reader spends seconds per
// sweep. Parallel readers keep Pause and Stop within their deadline.
const inventoryReaders = 64

// gateAgentVariable is set in the environment of every process a gate's agent
// starts.
const gateAgentVariable = "NO_MISTAKES_GATE"

// Inventory lists the running processes that are a task's own: those at work
// in directories or running a program from them, those job names, the
// task's terminals and what their jobs hold, as the stop that holds those
// jobs read them, and those carrying one of marks, its terminals' proofs
// (OwnedProcesses).
func Inventory(ctx context.Context, directories []string, job []Identity, marks []Mark) ([]Process, error) {
	entries, err := proc.Processes()
	if err != nil {
		return nil, err
	}
	// A desktop that cannot be read shows no program to be none of the
	// Overlord's, so every process then counts as one he uses: nothing is
	// the task's by its mark or its parent, only by its job and its place.
	windowOwners, windowsErr := proc.WindowOwners()
	ancestors, err := proc.Ancestry(os.Getpid(), 64)
	if err != nil {
		return nil, fmt.Errorf("identify lifecycle controller: %w", err)
	}
	processes := make([]Process, len(entries))
	indexes := make(chan int)
	var readers sync.WaitGroup
	for range inventoryReaders {
		readers.Go(func() {
			for index := range indexes {
				entry := entries[index]
				if entry.PID == os.Getpid() || slices.ContainsFunc(ancestors, func(ancestor proc.Entry) bool { return ancestor.PID == entry.PID && ancestor.Start.Equal(entry.Start) }) {
					continue
				}
				process := Process{PID: entry.PID, ParentPID: entry.ParentPID, Name: entry.ExeBase, Started: entry.Start, HasWindow: windowsErr != nil || windowOwners[entry.PID]}
				// The mark costs one more read of the process's memory, taken
				// through the handle its directory and arguments are read by.
				if len(marks) > 0 {
					var environment []string
					process.Directory, process.Arguments, environment, _ = proc.ParametersAndEnvironment(entry.PID)
					process.Mark, process.IsGateAgent = markOf(environment)
				} else {
					process.Directory, process.Arguments, _ = proc.Parameters(entry.PID)
				}
				// Each read opens a PID again, so discard a process replaced while its
				// evidence was collected. Terminate checks the same identity on a handle.
				if started, exists := proc.StartTime(entry.PID); !exists || !started.Equal(entry.Start) {
					continue
				}
				processes[index] = process
			}
		})
	}
	for index := range entries {
		if ctx.Err() != nil {
			break
		}
		indexes <- index
	}
	close(indexes)
	readers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Entries skipped above stay zero, and OwnedProcesses ignores them.
	return OwnedProcesses(processes, directories, job, marks), nil
}

// markOf reads the mark a process carries from its environment, and whether
// that environment is a gate agent's.
func markOf(environment []string) (Mark, bool) {
	var terminal, proof string
	isGateAgent := false
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case host.IDVariable:
			terminal = value
		case host.ProofVariable:
			proof = value
		case gateAgentVariable:
			isGateAgent = value != ""
		}
	}
	if terminal == "" || proof == "" {
		return Mark{}, isGateAgent
	}
	return Mark{Terminal: terminal, ProofSum: host.ProofSum(proof)}, isGateAgent
}
