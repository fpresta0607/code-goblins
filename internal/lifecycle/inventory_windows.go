package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
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
	processes, err := ReadProcesses(ctx, len(marks) > 0)
	if err != nil {
		return nil, err
	}
	return OwnedProcesses(processes, directories, job, marks), nil
}

// ReadProcesses reads every running process this one may open, with the
// evidence of whose it is: where it works, what it was started with, whether
// it shows a window and, withMarks, the mark it carries. This process and
// its ancestors are left out, so nothing judged on the reading ends the
// command that took it. A process that could not be read, or that was left
// out, is a zero entry, which OwnedProcesses ignores.
func ReadProcesses(ctx context.Context, withMarks bool) ([]Process, error) {
	return readProcesses(ctx, withMarks, false)
}

// ReadAllProcesses is ReadProcesses with this process and its ancestors read
// like any other, for a plan that ends nothing: left out, the terminal the
// plan was asked from would read as having no host, and everything it runs
// as detached from it.
func ReadAllProcesses(ctx context.Context, withMarks bool) ([]Process, error) {
	return readProcesses(ctx, withMarks, true)
}

func readProcesses(ctx context.Context, withMarks, withSelf bool) ([]Process, error) {
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
				if !withSelf && (entry.PID == os.Getpid() || slices.ContainsFunc(ancestors, func(ancestor proc.Entry) bool { return ancestor.PID == entry.PID && ancestor.Start.Equal(entry.Start) })) {
					continue
				}
				process := Process{PID: entry.PID, ParentPID: entry.ParentPID, Name: entry.ExeBase, Started: entry.Start, HasWindow: windowsErr != nil || windowOwners[entry.PID]}
				// The mark costs one more read of the process's memory, taken
				// through the handle its directory and arguments are read by.
				if withMarks {
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
	return processes, nil
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

// placesEvery is how often OwnerOf reads the home's tasks again, so a task
// started since is a place from then on.
const placesEvery = 30 * time.Second

// OwnerOf reads whose a process is in home h by its mark or its place
// (ownerOf): the terminal its environment names, once the proof value beside
// it is one a host of that terminal gave it (host.Proofs), or else the task
// whose folders it is at work in or runs a program from (TaskDirectories),
// of the tasks whose terminals ran since the machine started. A Git Bash
// tool carries no mark, and a script can unset it for what it starts, so
// the place is what still says such a process is a task's. It is empty for
// a process that is no terminal's or task's of this home, and for one that
// cannot be read.
func OwnerOf(h home.Home) func(pid int) string {
	var (
		mu     sync.Mutex
		places map[string][]string
		readAt time.Time
	)
	return ownerReader(h.State, func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		if places == nil || time.Since(readAt) >= placesEvery {
			places, readAt = taskPlaces(h), time.Now()
		}
		return places
	})
}

// ownerReader reads a process's mark and place and says whose it is, with
// the proofs under stateDir and the folders places gives each task.
func ownerReader(stateDir string, places func() map[string][]string) func(pid int) string {
	return func(pid int) string {
		directory, arguments, environment, _ := proc.ParametersAndEnvironment(pid)
		mark, isGateAgent := markOf(environment)
		process := Process{PID: pid, Directory: directory, Arguments: arguments, Mark: mark, IsGateAgent: isGateAgent}
		if len(arguments) > 0 {
			process.Name = filepath.Base(arguments[0])
		}
		return ownerOf(process, func(terminal string) []string {
			proofs, _ := host.Proofs(stateDir, terminal)
			return proofs
		}, places())
	}
}

// taskPlaces are the folders of each task of home h whose terminal ran since
// the machine started. A task whose record cannot be read, or names folders
// that are not its own, has none.
func taskPlaces(h home.Home) map[string][]string {
	places := map[string][]string{}
	ids, _ := host.Terminals(h.State)
	for _, id := range ids {
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			continue
		}
		if directories, err := TaskDirectories(h, meta); err == nil {
			places[id] = directories
		}
	}
	return places
}
