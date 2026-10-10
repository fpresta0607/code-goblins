package janitor

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// DetachedIdleFor is how long a detached tree of a running terminal must
// have done nothing, with its goblin at rest all the while, before the sweep
// ends it. A sweep runs at most once an hour, so a tree is ended no earlier
// than the second sweep that finds it idle.
const DetachedIdleFor = time.Hour

// idleProcessorTime is the processor time under which a tree's processes
// have done nothing between two sweeps. A server waiting for a request and a
// pipeline waiting on its reader use none, while a loop that looks at
// something once a minute uses a few milliseconds each time, which is work
// its owner waits on.
const idleProcessorTime = 100 * time.Millisecond

// Process is a running process as the sweep reads it: whose it is, by the
// evidence a goblin's teardown reads, and what it costs.
type Process struct {
	lifecycle.Process
	CPU    time.Duration
	Memory uint64
	// LastUsed is when a browser bridge's session was last used, zero for
	// any other process and for a bridge whose session is not known.
	LastUsed time.Time
}

// Owner is a terminal of this home: a task's, the CFO's or a run item's.
type Owner struct {
	ID string
	// Marks are the proofs the terminal was given since the machine started.
	Marks []lifecycle.Mark
	// Directories are the folders of the task the terminal is, none for a
	// terminal that is no task's or whose task's record is gone.
	Directories []string
	// HostPID is the host that runs the terminal now, 0 when none does.
	HostPID int
	// IsChanging says a pause, a resume or a stop of the terminal is under
	// way, whose own teardown decides what ends.
	IsChanging bool
	// AtRestSince is when the terminal's goblin was last seen at work, for a
	// goblin that has delivered and rests: its last report is done or failed
	// and no turn of its is in progress. It is zero for a goblin that works
	// or waits on something or somebody, for the CFO's terminal and a run
	// item's, and whenever it cannot be read: nothing of theirs is idle.
	AtRestSince time.Time
}

// owned are the processes among evidence that are the terminal's own, by the
// rule its teardown ends them by (lifecycle.OwnedProcesses): its mark, its
// task's folders, or a parent that is its own. It is the one place a sweep
// asks whose a process is: the process sweep ends and keeps by it
// (planProcesses), and the orphan sweep raises no finding for what it gives
// a terminal that runs (sightings).
func (owner Owner) owned(evidence []lifecycle.Process) []lifecycle.Process {
	return lifecycle.OwnedProcesses(evidence, owner.Directories, nil, owner.Marks)
}

// sightings is what one reading of the machine says of each process in
// running, for the orphan sweep (reap.Collector): the terminal among owners
// whose mark it carries, and the terminal among them that runs and owns it.
// processes are the ones whose evidence could be read; a process in running
// with none is sighted with neither, since it runs and nothing says whose it
// is. A process two running terminals own is the first one's, in the order
// of owners.
func sightings(running []proc.Entry, processes []Process, owners []Owner) []reap.Sighting {
	evidence := make([]lifecycle.Process, len(processes))
	read := make(map[int]Process, len(processes))
	for index, process := range processes {
		evidence[index] = process.Process
		read[process.PID] = process
	}
	owner := map[int]string{}
	for _, terminal := range owners {
		if terminal.HostPID == 0 {
			continue
		}
		for _, process := range terminal.owned(evidence) {
			if _, isOwned := owner[process.PID]; !isOwned {
				owner[process.PID] = terminal.ID
			}
		}
	}
	sighted := make([]reap.Sighting, 0, len(running))
	for _, entry := range running {
		sighting := reap.Sighting{PID: entry.PID, Started: entry.Start}
		if process, isRead := read[entry.PID]; isRead && process.Started.Equal(entry.Start) {
			sighting.Owner, sighting.IsGateAgent = owner[entry.PID], process.IsGateAgent
			for _, terminal := range owners {
				// A gate's agent carries the mark of whichever goblin started
				// the daemon, which says nothing of whose it is.
				if !process.IsGateAgent && process.Mark != (lifecycle.Mark{}) && slices.Contains(terminal.Marks, process.Mark) {
					sighting.Terminal = terminal.ID
				}
			}
		}
		sighted = append(sighted, sighting)
	}
	return sighted
}

// ProcessItem is one process the sweep ended or left for the CFO. Memory is
// its own and that of everything under it.
type ProcessItem struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Name    string    `json:"name"`
	Owner   string    `json:"owner,omitempty"`
	Memory  uint64    `json:"memory"`
	Command string    `json:"command,omitempty"`
	Why     string    `json:"why"`
}

// Watched is a detached tree of a running terminal, by its first process:
// the processes it held when the sweep last saw it work, each with the
// processor time it had used, and when that was.
type Watched struct {
	PID     int             `json:"pid"`
	Started time.Time       `json:"started"`
	Members []WatchedMember `json:"members"`
	Since   time.Time       `json:"since"`
}

// WatchedMember is one process of a watched tree.
type WatchedMember struct {
	PID     int           `json:"pid"`
	Started time.Time     `json:"started"`
	CPU     time.Duration `json:"cpu"`
}

// worked reports whether a tree did anything since the sweep that recorded
// prior: a process joined it or left it, or the processes it still holds
// used idleProcessorTime between them. A tree is never judged by its
// processes' time added up, which falls when one that used the processor
// exits: on 2026-10-10 a dry run showed a long test run, between two of its
// test programs, reading as having done less than an hour before. A process
// that exited did its work unseen, so its going counts as work. A record
// that names no member says nothing, and its tree counts as having worked.
func worked(prior Watched, tree []Process) bool {
	if len(prior.Members) != len(tree) {
		return true
	}
	var used time.Duration
	for _, member := range tree {
		index := slices.IndexFunc(prior.Members, func(seen WatchedMember) bool {
			return seen.PID == member.PID && seen.Started.Equal(member.Started)
		})
		if index < 0 {
			return true
		}
		if member.CPU > prior.Members[index].CPU {
			used += member.CPU - prior.Members[index].CPU
		}
	}
	return used >= idleProcessorTime
}

// ProcessSweep is what one sweep did about processes.
type ProcessSweep struct {
	Ended   []ProcessItem `json:"ended,omitempty"`
	Left    []ProcessItem `json:"left,omitempty"`
	Watched []Watched     `json:"watched,omitempty"`
}

// ProcessPlan is what a sweep would do about the home's processes, from one
// reading of the machine and of the home's terminals.
type ProcessPlan struct {
	// Read is how many processes the plan read, and Owners the terminals it
	// judged them for.
	Read   int
	Owners []Owner
	// Ending is what the sweep would end and Left what it would name for the
	// CFO, each with why.
	Ending []ProcessItem
	Left   []ProcessItem
	// Kept is what the sweep judged and leaves: each process that is a
	// terminal's own, or carries its mark, and is not ended, with the rule
	// that keeps it. A process no terminal's evidence reaches is not listed.
	Kept     []ProcessItem
	Watching []Watched
	Notes    []string
}

// planProcesses decides what a sweep ends, what it leaves for the CFO and
// what it goes on watching. A process is a terminal's own by the rule its
// teardown ends it by (lifecycle.OwnedProcesses), so the sweep ends nothing
// a pause or a stop of that terminal would not have ended.
//
// What a terminal with no host left running is ended: its owner is gone.
//
// A detached tree of a running terminal, one whose parents no longer reach
// the terminal's host, is ended only when two things have both lasted
// DetachedIdleFor: the tree did nothing (worked), and its goblin was at rest
// (Owner.AtRestSince). Every background command a harness starts through Git
// Bash is detached, and nothing the sweep can read says which of them a
// goblin still waits on: a gate waiter that blocks for hours, a watch on a
// quiet file, a test it reads from. So nothing of a goblin that works is
// idle, however still it sits. It ends with the goblin, at its pause, stop,
// cleanup or relaunch, and shows on the board under it meanwhile.
//
// The rest is left and named for the CFO: a gate agent's process whose gate
// is gone, and a browser bridge nothing ties to an owner. A desktop program
// is the Overlord's and is neither ended nor named.
func planProcesses(processes []Process, owners []Owner, watched []Watched, now time.Time) (ending, left []ProcessItem, watching []Watched, kept []ProcessItem) {
	evidence := make([]lifecycle.Process, len(processes))
	byPID := make(map[int]Process, len(processes))
	children := make(map[int][]Process, len(processes))
	running := make([]proc.ServiceProcess, 0, len(processes))
	for index, process := range processes {
		evidence[index] = process.Process
		if process.PID == 0 {
			continue
		}
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
		running = append(running, proc.ServiceProcess{PID: process.PID, ParentPID: process.ParentPID, ExeBase: process.Name, Arguments: process.Arguments, Start: process.Started})
	}
	desktop := lifecycle.DesktopPrograms(evidence)
	services := proc.ServicesOf(running)
	// under lists pid and everything below it by parents that started first.
	under := func(pid int, within func(Process) bool) []Process {
		root, isRunning := byPID[pid]
		if !isRunning {
			return nil
		}
		tree, seen := []Process{root}, map[int]bool{pid: true}
		for index := 0; index < len(tree); index++ {
			for _, child := range children[tree[index].PID] {
				if !seen[child.PID] && !child.Started.Before(tree[index].Started) && within(child) {
					seen[child.PID] = true
					tree = append(tree, child)
				}
			}
		}
		return tree
	}
	hasLivingParent := func(process Process) bool {
		parent, isRunning := byPID[process.ParentPID]
		return isRunning && parent.PID != process.PID && !process.Started.Before(parent.Started)
	}
	item := func(tree []Process, owner, why string) ProcessItem {
		root := tree[0]
		entry := ProcessItem{PID: root.PID, Started: root.Started, Name: root.Name, Owner: owner, Command: commandOf(root.Arguments), Why: why}
		for _, process := range tree {
			entry.Memory += process.Memory
		}
		return entry
	}

	keep := func(process Process, owner, why string) {
		kept = append(kept, item([]Process{process}, owner, why))
	}
	isOwned := map[int]bool{}
	for _, owner := range owners {
		owned := map[int]bool{}
		for _, process := range owner.owned(evidence) {
			owned[process.PID], isOwned[process.PID] = true, true
		}
		reach := map[int]bool{}
		for _, process := range under(owner.HostPID, func(Process) bool { return true }) {
			reach[process.PID] = true
		}
		isDetached := func(process Process) bool {
			return owned[process.PID] && !reach[process.PID] && !desktop[process.PID]
		}
		for _, process := range processes {
			switch {
			case process.PID == 0:
			case !owned[process.PID]:
				// What carries the terminal's mark and is no part of what
				// its teardown ends.
				if process.Mark == (lifecycle.Mark{}) || !slices.Contains(owner.Marks, process.Mark) {
					break
				}
				switch {
				case services[process.PID] != proc.NoService:
					keep(process, owner.ID, "a machine service, or what runs under one, which serves every goblin")
				case process.IsGateAgent:
					keep(process, owner.ID, "a gate's agent, which works for a gate whichever goblin started its daemon")
				case desktop[process.PID]:
					keep(process, owner.ID, "a desktop program, or what one started, which is the Overlord's to close")
				default:
					keep(process, owner.ID, "it carries the terminal's mark and no rule of its teardown makes it the terminal's")
				}
			case owner.IsChanging:
				keep(process, owner.ID, fmt.Sprintf("a command is changing terminal %s and decides what of it ends", owner.ID))
			case reach[process.PID]:
				keep(process, owner.ID, fmt.Sprintf("terminal %s runs and its host reaches it", owner.ID))
			case desktop[process.PID]:
				keep(process, owner.ID, "a desktop program at work in the task's folders, which is the Overlord's to close")
			}
		}
		if owner.IsChanging {
			continue
		}
		for _, process := range processes {
			if !isDetached(process) {
				continue
			}
			// A tree is named by its first process: the one whose parent is
			// no part of it.
			if parent, isRunning := byPID[process.ParentPID]; isRunning && isDetached(parent) && hasLivingParent(process) {
				continue
			}
			// A process that is the terminal's by its place alone, under a
			// living parent that is not the terminal's, is being run there
			// by somebody else, as when the CFO reads a paused goblin's
			// worktree.
			tree := under(process.PID, isDetached)
			if !slices.Contains(owner.Marks, process.Mark) && hasLivingParent(process) {
				for _, member := range tree {
					keep(member, owner.ID, "somebody else runs it in the task's folders: it carries no mark of the terminal and its parent still runs")
				}
				continue
			}
			if owner.HostPID == 0 {
				for _, member := range tree {
					ending = append(ending, item([]Process{member}, owner.ID, fmt.Sprintf("its terminal %s is gone", owner.ID)))
				}
				continue
			}
			// A browser bridge keeps its page drawing whether or not anything
			// drives it: three left on 2026-10-09 used most of a processor
			// between them. So a bridge is idle by when its session was last
			// used, never by the processor time it uses.
			if !process.LastUsed.IsZero() {
				switch unused := later(process.LastUsed, owner.AtRestSince); {
				case owner.AtRestSince.IsZero():
					for _, member := range tree {
						keep(member, owner.ID, fmt.Sprintf("a browser bridge of terminal %s, whose goblin works: nothing of a goblin that works is idle", owner.ID))
					}
				case now.Sub(unused) >= DetachedIdleFor:
					for _, member := range tree {
						ending = append(ending, item([]Process{member}, owner.ID, fmt.Sprintf("a browser bridge of terminal %s, whose goblin rests, unused since %s", owner.ID, process.LastUsed.UTC().Format("15:04Z"))))
					}
				default:
					for _, member := range tree {
						keep(member, owner.ID, fmt.Sprintf("a browser bridge of terminal %s, whose goblin rests since %s, last used at %s: ended once both have lasted an hour", owner.ID, owner.AtRestSince.UTC().Format("15:04Z"), process.LastUsed.UTC().Format("15:04Z")))
					}
				}
				continue
			}
			seen := Watched{PID: process.PID, Started: process.Started, Since: now}
			for _, member := range tree {
				seen.Members = append(seen.Members, WatchedMember{PID: member.PID, Started: member.Started, CPU: member.CPU})
			}
			if index := slices.IndexFunc(watched, func(prior Watched) bool { return prior.PID == process.PID && prior.Started.Equal(process.Started) }); index >= 0 && !worked(watched[index], tree) {
				seen = watched[index]
			}
			if owner.AtRestSince.IsZero() {
				watching = append(watching, seen)
				for _, member := range tree {
					keep(member, owner.ID, fmt.Sprintf("detached from terminal %s, whose goblin works: nothing of a goblin that works is idle", owner.ID))
				}
				continue
			}
			if idle := later(seen.Since, owner.AtRestSince); now.Sub(idle) < DetachedIdleFor {
				watching = append(watching, seen)
				for _, member := range tree {
					keep(member, owner.ID, fmt.Sprintf("detached from terminal %s, whose goblin rests since %s: last seen at work at %s, ended once both have lasted an hour", owner.ID, owner.AtRestSince.UTC().Format("15:04Z"), seen.Since.UTC().Format("15:04Z")))
				}
				continue
			}
			for _, member := range tree {
				ending = append(ending, item([]Process{member}, owner.ID, fmt.Sprintf("detached from terminal %s, whose goblin rests since %s, and idle since %s", owner.ID, owner.AtRestSince.UTC().Format("15:04Z"), seen.Since.UTC().Format("15:04Z"))))
			}
		}
	}

	for _, process := range processes {
		if process.PID == 0 || isOwned[process.PID] || desktop[process.PID] || services[process.PID] != proc.NoService || hasLivingParent(process) {
			continue
		}
		tree := under(process.PID, func(child Process) bool { return !isOwned[child.PID] })
		switch {
		case process.IsGateAgent:
			left = append(left, item(tree, "", "a gate agent's process whose gate is gone"))
		case isBrowserBridge(process):
			left = append(left, item(tree, "", "a browser bridge nothing ties to an owner"))
		}
	}
	for _, items := range [][]ProcessItem{ending, left} {
		sort.Slice(items, func(i, j int) bool { return items[i].PID < items[j].PID })
	}
	// What is ended or named is said once, there.
	kept = slices.DeleteFunc(kept, func(item ProcessItem) bool {
		isListed := func(listed ProcessItem) bool { return listed.PID == item.PID }
		return slices.ContainsFunc(ending, isListed) || slices.ContainsFunc(left, isListed)
	})
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Owner != kept[j].Owner {
			return kept[i].Owner < kept[j].Owner
		}
		return kept[i].PID < kept[j].PID
	})
	sort.Slice(watching, func(i, j int) bool { return watching[i].PID < watching[j].PID })
	return ending, left, watching, kept
}

// PlanProcesses reads the machine and the home's terminals once and says
// what a sweep would end, what it would name for the CFO and what it would
// leave, with the rule that decides each. It ends nothing and writes nothing.
// The sweep acts on this same plan (sweepProcesses), so what it lists to end
// is what a sweep with the same readers ends.
func (cfg Config) PlanProcesses(ctx context.Context) (ProcessPlan, error) {
	processes, err := cfg.Processes(ctx)
	if err != nil {
		return ProcessPlan{}, err
	}
	owners, notes := cfg.Owners()
	lastUsed := cfg.browserBridgeUse()
	for index, process := range processes {
		if isBrowserBridge(process) {
			processes[index].LastUsed = lastUsed[process.PID]
		}
	}
	plan := ProcessPlan{Read: len(processes), Owners: owners, Notes: notes}
	plan.Ending, plan.Left, plan.Watching, plan.Kept = planProcesses(processes, owners, cfg.Watched, cfg.Now)
	return plan, nil
}

// later is the later of two times.
func later(one, other time.Time) time.Time {
	if other.After(one) {
		return other
	}
	return one
}

// sweepProcesses ends the processes the home's terminals left running and
// names the ones nothing proves the fleet's own. A machine that cannot be
// read ends nothing: the pass is a note, and the trees the last sweep
// watched are carried over so their idle time is not lost.
func (cfg Config) sweepProcesses(ctx context.Context, record *Record) {
	if cfg.Processes == nil || cfg.Owners == nil {
		return
	}
	record.Processes.Watched = cfg.Watched
	plan, err := cfg.PlanProcesses(ctx)
	if err != nil {
		record.Notes = append(record.Notes, "the machine's processes could not be read, so none was ended: "+err.Error())
		return
	}
	record.Notes = append(record.Notes, plan.Notes...)
	record.Processes.Left, record.Processes.Watched = plan.Left, plan.Watching
	for _, item := range plan.Ending {
		if cfg.EndProcess == nil {
			break
		}
		if err := cfg.EndProcess(ctx, item); err != nil {
			record.Notes = append(record.Notes, fmt.Sprintf("%s pid %d could not be ended, so the next sweep tries again: %v", item.Name, item.PID, err))
			continue
		}
		record.Processes.Ended = append(record.Processes.Ended, item)
	}
}

// isBrowserBridge reports whether a process is chrome-devtools-axi's bridge,
// which stays behind the command that started it and holds a browser.
func isBrowserBridge(process Process) bool {
	name := strings.TrimSuffix(strings.ToLower(process.Name), ".exe")
	return name == "node" && slices.ContainsFunc(process.Arguments, func(argument string) bool {
		return strings.Contains(strings.ToLower(argument), "chrome-devtools-axi-bridge")
	})
}

// secretArgument matches an argument that names what follows it as a secret.
var secretArgument = regexp.MustCompile(`(?i)(token|secret|passw|apikey|api[-_]key|credential|authorization|cookie|bearer)`)

// commandOf is a process's command as the CFO may read it: at most 160
// characters, with the value of an argument named as a secret, and a URL's
// query, left out. A command line can hold a credential, and the sweep's
// record and wake are kept and shown.
func commandOf(arguments []string) string {
	shown := make([]string, 0, len(arguments))
	hideNext := false
	for _, argument := range arguments {
		switch name, _, hasValue := strings.Cut(argument, "="); {
		case hideNext:
			argument, hideNext = "<hidden>", false
		case hasValue && secretArgument.MatchString(name):
			argument = name + "=<hidden>"
		case strings.HasPrefix(argument, "-") && secretArgument.MatchString(argument):
			hideNext = true
		case strings.Contains(argument, "://"):
			argument, _, _ = strings.Cut(argument, "?")
		}
		shown = append(shown, argument)
	}
	command := strings.Join(shown, " ")
	if len(command) > 160 {
		command = command[:157] + "..."
	}
	return command
}
