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
)

// detachedIdleFor is how long a detached tree of a running terminal may use
// no processor time before the sweep ends it. A sweep runs at most once an
// hour, so a tree is ended at the second sweep that finds it idle.
const detachedIdleFor = time.Hour

// idleProcessorTime is the processor time under which a tree has done
// nothing between two sweeps. A server waiting for a request and a pipeline
// waiting on its reader use none, while a loop that looks at something once
// a minute uses a few milliseconds each time, which is work its owner
// waits on.
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
// the processor time the tree had used when the sweep last saw it work, and
// when that was.
type Watched struct {
	PID     int           `json:"pid"`
	Started time.Time     `json:"started"`
	CPU     time.Duration `json:"cpu"`
	Since   time.Time     `json:"since"`
}

// ProcessSweep is what one sweep did about processes.
type ProcessSweep struct {
	Ended   []ProcessItem `json:"ended,omitempty"`
	Left    []ProcessItem `json:"left,omitempty"`
	Watched []Watched     `json:"watched,omitempty"`
}

// planProcesses decides what a sweep ends, what it leaves for the CFO and
// what it goes on watching. A process is a terminal's own by the rule its
// teardown ends it by (lifecycle.OwnedProcesses), so the sweep ends nothing
// a pause or a stop of that terminal would not have ended.
//
// What a terminal with no host left running is ended: its owner is gone. So
// is a detached tree of a running terminal, one whose parents no longer
// reach the terminal's host, once it has used no processor time for
// detachedIdleFor. A detached tree that works is left to its owner, and
// shows on the board under it.
//
// The rest is left and named for the CFO: a gate agent's process whose gate
// is gone, and a browser bridge nothing ties to an owner. A desktop program
// is the Overlord's and is neither ended nor named.
func planProcesses(processes []Process, owners []Owner, watched []Watched, now time.Time) (ending, left []ProcessItem, watching []Watched) {
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

	isOwned := map[int]bool{}
	for _, owner := range owners {
		owned := map[int]bool{}
		for _, process := range lifecycle.OwnedProcesses(evidence, owner.Directories, nil, owner.Marks) {
			owned[process.PID], isOwned[process.PID] = true, true
		}
		if owner.IsChanging {
			continue
		}
		reach := map[int]bool{}
		for _, process := range under(owner.HostPID, func(Process) bool { return true }) {
			reach[process.PID] = true
		}
		isDetached := func(process Process) bool {
			return owned[process.PID] && !reach[process.PID] && !desktop[process.PID]
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
			if !slices.Contains(owner.Marks, process.Mark) && hasLivingParent(process) {
				continue
			}
			tree := under(process.PID, isDetached)
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
				for _, member := range tree {
					if now.Sub(process.LastUsed) >= detachedIdleFor {
						ending = append(ending, item([]Process{member}, owner.ID, fmt.Sprintf("a browser bridge of terminal %s, unused since %s", owner.ID, process.LastUsed.UTC().Format("15:04Z"))))
					}
				}
				continue
			}
			var used time.Duration
			for _, member := range tree {
				used += member.CPU
			}
			seen := Watched{PID: process.PID, Started: process.Started, CPU: used, Since: now}
			if index := slices.IndexFunc(watched, func(prior Watched) bool { return prior.PID == process.PID && prior.Started.Equal(process.Started) }); index >= 0 && used-watched[index].CPU < idleProcessorTime {
				seen = watched[index]
			}
			if now.Sub(seen.Since) < detachedIdleFor {
				watching = append(watching, seen)
				continue
			}
			for _, member := range tree {
				ending = append(ending, item([]Process{member}, owner.ID, fmt.Sprintf("detached from terminal %s and idle since %s", owner.ID, seen.Since.UTC().Format("15:04Z"))))
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
	sort.Slice(watching, func(i, j int) bool { return watching[i].PID < watching[j].PID })
	return ending, left, watching
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
	processes, err := cfg.Processes(ctx)
	if err != nil {
		record.Notes = append(record.Notes, "the machine's processes could not be read, so none was ended: "+err.Error())
		return
	}
	owners, notes := cfg.Owners()
	record.Notes = append(record.Notes, notes...)
	lastUsed := cfg.browserBridgeUse()
	for index, process := range processes {
		if isBrowserBridge(process) {
			processes[index].LastUsed = lastUsed[process.PID]
		}
	}
	ending, left, watching := planProcesses(processes, owners, cfg.Watched, cfg.Now)
	record.Processes.Left, record.Processes.Watched = left, watching
	for _, item := range ending {
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
