package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// defaultProcessPlan reads what the fleet's process sweeps would do in home h
// now. It only reads: the machine's processes, the home's terminals and task
// records, the janitor's last record and the browser sessions' folders. The
// sweep's plan is the one the janitor acts on (janitor.PlanProcesses), with
// the reader that forgets nothing, and each task's is the one its cleanup
// and its relaunch act on (lifecycle.Left).
func defaultProcessPlan(ctx context.Context, h home.Home) (processPlan, error) {
	now := time.Now()
	plan := processPlan{At: now, Flags: map[int]string{}}
	previous, _ := janitor.ReadRecord(h.State)
	browserSessions := ""
	if profile, err := os.UserHomeDir(); err == nil {
		browserSessions = filepath.Join(profile, ".chrome-devtools-axi", "sessions")
	}
	cfg := janitor.Config{
		Home:            h,
		Now:             now,
		Processes:       janitor.ReadAllProcesses,
		Owners:          func() ([]janitor.Owner, []string) { return janitor.Owners(h) },
		Watched:         previous.Processes.Watched,
		BrowserSessions: browserSessions,
	}
	sweep, err := cfg.PlanProcesses(ctx)
	if err != nil {
		return plan, fmt.Errorf("read the machine's processes: %w", err)
	}
	plan.Sweep = sweep
	// A sweep an hour on, with what this one would go on watching and every
	// goblin as it is now: a tree that did something between the two
	// readings is at work and stays, and so does all of a goblin that works.
	cfg.Watched, cfg.Now = sweep.Watching, now.Add(janitor.DetachedIdleFor+time.Minute)
	later, err := cfg.PlanProcesses(ctx)
	if err != nil {
		return plan, fmt.Errorf("read the machine's processes again: %w", err)
	}
	for _, item := range later.Ending {
		if !slices.ContainsFunc(sweep.Ending, func(now janitor.ProcessItem) bool { return now.PID == item.PID && now.Started.Equal(item.Started) }) {
			plan.Later = append(plan.Later, item)
		}
	}

	standing, err := readStanding(ctx, sweep.Owners)
	if err != nil {
		return plan, fmt.Errorf("read whose each process is: %w", err)
	}
	for _, items := range [][]janitor.ProcessItem{plan.Sweep.Ending, plan.Later} {
		for _, item := range items {
			if flag := standing.sweepFlag(item, sweep.Owners); flag != "" {
				plan.Flags[item.PID] = flag
			}
		}
	}

	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return plan, fmt.Errorf("list the home's tasks: %w", err)
	}
	for _, id := range scan.MetaIDs {
		task := taskProcessPlan{ID: id}
		if index := slices.IndexFunc(sweep.Owners, func(owner janitor.Owner) bool { return owner.ID == id }); index >= 0 {
			task.HostPID = sweep.Owners[index].HostPID
		}
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			task.Err = err.Error()
			plan.Tasks = append(plan.Tasks, task)
			continue
		}
		left, err := lifecycle.Left(ctx, h, meta)
		if err != nil {
			task.Err = err.Error()
		}
		for _, process := range left {
			task.Ending = append(task.Ending, plannedProcess{
				PID:    process.PID,
				Name:   process.Name,
				Memory: standing.memory(process.PID, process.Started),
				By:     process.By,
				Flag:   standing.taskFlag(id, process.PID, process.Started),
			})
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	return plan, nil
}

// processStanding is whose each running process looks to be, apart from any
// rule that would end it: what a would-end list is checked against.
type processStanding struct {
	processes map[int]janitor.Process
	// isDesktop is a program a person uses, or what one started.
	isDesktop map[int]bool
	// marked is the terminal whose proofs hold the mark a process carries,
	// and under the terminal whose running host reaches it by its parents.
	marked map[int]string
	under  map[int]string
}

func readStanding(ctx context.Context, owners []janitor.Owner) (processStanding, error) {
	processes, err := janitor.ReadAllProcesses(ctx)
	if err != nil {
		return processStanding{}, err
	}
	standing := processStanding{processes: map[int]janitor.Process{}, marked: map[int]string{}, under: map[int]string{}}
	evidence := make([]lifecycle.Process, len(processes))
	children := map[int][]janitor.Process{}
	for index, process := range processes {
		evidence[index] = process.Process
		standing.processes[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
		for _, owner := range owners {
			if !process.IsGateAgent && slices.Contains(owner.Marks, process.Mark) {
				standing.marked[process.PID] = owner.ID
			}
		}
	}
	standing.isDesktop = lifecycle.DesktopPrograms(evidence)
	for _, owner := range owners {
		host, isRunning := standing.processes[owner.HostPID]
		if owner.HostPID == 0 || !isRunning {
			continue
		}
		for queue := []janitor.Process{host}; len(queue) > 0; queue = queue[1:] {
			if _, seen := standing.under[queue[0].PID]; seen {
				continue
			}
			standing.under[queue[0].PID] = owner.ID
			for _, child := range children[queue[0].PID] {
				if !child.Started.Before(queue[0].Started) {
					queue = append(queue, child)
				}
			}
		}
	}
	return standing, nil
}

// known is the process a pid named when the standing was read, when it is
// still the one that started at started.
func (s processStanding) known(pid int, started time.Time) (janitor.Process, bool) {
	process, isRunning := s.processes[pid]
	return process, isRunning && process.Started.Equal(started)
}

func (s processStanding) memory(pid int, started time.Time) uint64 {
	process, isKnown := s.known(pid, started)
	if !isKnown {
		return 0
	}
	return process.Memory
}

// sweepFlag is why a process the janitor's sweep would end looks like the
// Overlord's, the CFO terminal's or a working goblin's, and empty when it
// looks like none of them. What a goblin that has delivered and rests left
// detached is the sweep's to end, and is not flagged.
func (s processStanding) sweepFlag(item janitor.ProcessItem, owners []janitor.Owner) string {
	if _, isKnown := s.known(item.PID, item.Started); isKnown {
		switch {
		case s.isDesktop[item.PID]:
			return "HIS: a desktop program, or what one started"
		case s.marked[item.PID] == supervisor.NativeCFOTerminal || s.under[item.PID] == supervisor.NativeCFOTerminal:
			return "CFO: a process of the CFO's terminal"
		case s.under[item.PID] != "":
			return "LIVE: under the running host of terminal " + s.under[item.PID]
		}
	}
	if item.Owner == supervisor.NativeCFOTerminal {
		return "CFO: a process of the CFO's terminal"
	}
	if index := slices.IndexFunc(owners, func(owner janitor.Owner) bool { return owner.ID == item.Owner }); index >= 0 && owners[index].HostPID != 0 && owners[index].AtRestSince.IsZero() {
		return "LIVE: detached from terminal " + item.Owner + ", whose goblin works"
	}
	return ""
}

// taskFlag is why a process a task's cleanup or relaunch would end looks
// like somebody else's: the Overlord's, the CFO terminal's or another
// terminal's.
func (s processStanding) taskFlag(task string, pid int, started time.Time) string {
	if _, isKnown := s.known(pid, started); !isKnown {
		return ""
	}
	switch marked, under := s.marked[pid], s.under[pid]; {
	case s.isDesktop[pid]:
		return "HIS: a desktop program, or what one started"
	case task != supervisor.NativeCFOTerminal && (marked == supervisor.NativeCFOTerminal || under == supervisor.NativeCFOTerminal):
		return "CFO: a process of the CFO's terminal"
	case marked != "" && marked != task:
		return "OTHER: it carries the mark of terminal " + marked
	case under != "" && under != task:
		return "OTHER: under the running host of terminal " + under
	}
	return ""
}
