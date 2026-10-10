package janitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ReadProcesses reads every running process this one may open, with the
// evidence of whose it is and what it costs: the processor time it has used
// and its private memory, from one list of the machine's processes.
func ReadProcesses(ctx context.Context) ([]Process, error) {
	evidence, err := lifecycle.ReadProcesses(ctx, true)
	if err != nil {
		return nil, err
	}
	costs, err := fleettree.Processes()
	if err != nil {
		return nil, err
	}
	costOf := make(map[int]fleettree.Process, len(costs))
	for _, cost := range costs {
		costOf[cost.PID] = cost
	}
	processes := make([]Process, 0, len(evidence))
	for _, process := range evidence {
		if process.PID == 0 {
			continue
		}
		read := Process{Process: process}
		// A pid the second list gives another process costs nothing known.
		if cost, isRunning := costOf[process.PID]; isRunning && cost.Started.Equal(process.Started) {
			read.CPU, read.Memory = cost.CPU, cost.Memory
		}
		processes = append(processes, read)
	}
	return processes, nil
}

// changeLocks are the locks a command holds while it changes terminal id: a
// pause, resume or stop, a switch, and a cleanup.
func changeLocks(id string) []string {
	return []string{".lifecycle-" + id + ".lock", ".switch-" + id + ".lock", state.CleanupLockName(id)}
}

// ReadOwners lists the terminals of home h that were given a proof since the
// machine started: each with its proofs, the host that runs it now, the
// folders of the task it is, and whether a command is changing it. It first
// forgets the proofs from before the machine started, which no process
// carries any more. A terminal that cannot be read is named in the notes and
// left out, so nothing is ended on its account.
func ReadOwners(h home.Home) ([]Owner, []string) {
	var notes []string
	if err := host.ForgetProofs(h.State, time.Now().Add(-windows.DurationSinceBoot())); err != nil {
		notes = append(notes, "proofs from before the machine started could not be forgotten: "+err.Error())
	}
	ids, err := host.Terminals(h.State)
	if err != nil {
		return nil, append(notes, "the terminals' records could not be listed, so no process was judged: "+err.Error())
	}
	var owners []Owner
	for _, id := range ids {
		owner := Owner{ID: id}
		proofs, err := host.Proofs(h.State, id)
		if err != nil {
			notes = append(notes, fmt.Sprintf("terminal %s's proofs could not be read, so its processes were left: %v", id, err))
			continue
		}
		for _, proof := range proofs {
			owner.Marks = append(owner.Marks, lifecycle.Mark{Terminal: id, ProofSum: proof})
		}
		record, err := host.ReadRecord(h.State, id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			notes = append(notes, fmt.Sprintf("terminal %s's record could not be read, so its processes were left: %v", id, err))
			continue
		}
		if err == nil && host.Running(record) {
			owner.HostPID = record.HostPID
		}
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			notes = append(notes, fmt.Sprintf("task %s's record could not be read, so its processes were left: %v", id, err))
			continue
		}
		if err == nil {
			// A record that names folders that are not the task's own names
			// none, and the terminal's mark still says what is its own.
			owner.Directories, _ = lifecycle.TaskDirectories(h, meta)
		}
		owner.IsChanging = slices.ContainsFunc(changeLocks(id), func(name string) bool {
			held, err := lock.ReadNamed(h.State, name)
			return err == nil && held.Alive()
		})
		owners = append(owners, owner)
	}
	return owners, notes
}

// EndProcess ends the one process item names, proven by its start time on
// the handle it is ended through, so a pid another program took is left.
func EndProcess(ctx context.Context, item ProcessItem) error {
	_, err := lifecycle.Terminate(ctx, lifecycle.Identity{PID: item.PID, Started: item.Started})
	return err
}
