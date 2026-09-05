// Package reap finds and retires the fleet resources nothing else notices: a
// harness process whose pane is gone, a dev server left running in a finished
// goblin's worktree, and the worktree, metadata and status records left behind
// when a task ends without a clean cleanup.
//
// No single source sees all of it. cfo knows the tasks it started, Herdr knows
// the panes that still exist, and only the operating system knows what is
// still running, so classification cross-references all four (state, panes,
// processes, worktree directories) and trusts none of them alone.
//
// Detection is the default and acting is opt-in: see Service.Apply for the
// gates, which is where the correctness of this package actually lives.
package reap

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// Class names one kind of leaked fleet resource. Each needs a different
// action, which is why they are separate classes rather than one "orphan".
type Class string

const (
	// OrphanProcess is a harness process whose pane no longer exists. It is
	// the dangerous one: unsupervised, invisible to herdr agent list and to
	// every CFO surface, and still able to spend tokens.
	OrphanProcess Class = "orphan_process"
	// StaleServer is a long-lived child (a next dev, a vite server) rooted in
	// a worktree whose task has finished.
	StaleServer Class = "stale_server"
	// OrphanWorktree is a worktree directory with no pane holding a live agent
	// behind it. One whose task never reported a terminal status is still
	// reported, and held: a goblin that lost its pane mid-work leaks its
	// worktree exactly as a finished one does, and the operator needs to see
	// it either way.
	OrphanWorktree Class = "orphan_worktree"
	// OrphanMeta is a state/<id>.meta with no pane, no process, and no
	// worktree left on disk. A meta whose worktree still exists classifies as
	// OrphanWorktree instead, because returning the worktree retires the meta
	// with it and one resource must not be reported twice.
	OrphanMeta Class = "orphan_meta"
	// OrphanStatus is a state/<id>.status log with no matching meta.
	OrphanStatus Class = "orphan_status"
)

// Finding is one classified resource and the verdict on acting upon it.
// Hold, when set, is why --apply will leave it alone: an unattributable
// process, a task that has not finished, work that would be destroyed. Every
// gate that refuses records its refusal here rather than dropping the finding,
// so the report says what was found AND why it was not touched.
type Finding struct {
	Class  Class  `json:"class"`
	TaskID string `json:"task_id,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
	Action string `json:"action"`
	Hold   string `json:"hold,omitempty"`
}

// Line renders one finding as the single line both the report and the status
// log use, so a reaped resource reads identically in both places.
func (f Finding) Line() string {
	var b strings.Builder
	b.WriteString(string(f.Class))
	if f.TaskID != "" {
		b.WriteString(" " + f.TaskID)
	}
	if f.PID != 0 {
		fmt.Fprintf(&b, " pid=%d", f.PID)
	}
	if f.Path != "" {
		b.WriteString(" " + f.Path)
	}
	b.WriteString(": " + f.Detail)
	if f.Hold != "" {
		b.WriteString(" | HELD: " + f.Hold)
		return b.String()
	}
	b.WriteString(" | would " + f.Action)
	return b.String()
}

// Process is one row of the operating system process table.
type Process struct {
	PID         int       `json:"pid"`
	ParentPID   int       `json:"ppid"`
	Name        string    `json:"name"`
	CommandLine string    `json:"cmd"`
	Start       time.Time `json:"start"`
}

// Pane is one pane Herdr still reports, with the operating-system identity
// behind it. ShellPID is the shell Herdr started the pane with and
// ForegroundPID is the process group currently in its foreground; a harness
// running in the pane is that foreground group or a descendant of the shell.
type Pane struct {
	ID            string
	ShellPID      int
	ForegroundPID int
	HasAgent      bool
}

// Task is one state record, reduced to what classification needs.
type Task struct {
	ID       string
	Meta     state.TaskMeta
	Verb     string
	Terminal bool
}

// WorktreeDir is one directory found under a project's .worktrees/.
type WorktreeDir struct {
	Path    string
	Project string
	TaskID  string
}

// Inventory is the cross-referenced evidence one classification runs over. It
// is a plain value with no I/O so the classification is a pure function over
// synthetic fixtures; Collector builds the production one.
type Inventory struct {
	Tasks           []Task
	OrphanStatusIDs []string
	Panes           []Pane
	Processes       []Process
	Worktrees       []WorktreeDir
	// FleetRootPIDs are the Herdr server processes. Every pane shell, and so
	// every harness CFO ever started, descends from one of these. A harness
	// process with no such ancestry is somebody else's (the Overlord's own
	// editor session, most importantly) and is never killed without --force.
	FleetRootPIDs []int
	// SelfPIDs is this process and its ancestors. The sweep runs inside the
	// CFO's own session, so without this it would report the session it is
	// running in as an orphan.
	SelfPIDs []int
	// UnresolvedPanes are panes that exist but could not report their
	// operating-system identity. Their harness processes are therefore
	// missing from the supervised set, and a live goblin under one of them
	// looks exactly like an orphan. Every process finding is held while any
	// pane is unresolved: the sweep still reports what it saw, but it will
	// not kill on evidence it knows is incomplete.
	UnresolvedPanes []string
}

// harnessSignatures are the distinctive command-line fragments a CFO-launched
// harness carries. They mirror internal/harness's adapters, and
// TestHarnessSignaturesMatchAdapters asserts they still do, so a flag that
// changes there cannot leave this sweep blind.
//
// kimi and pi build no distinctive flag of their own, so they are matched on
// the executable name alone. That is weaker, which is exactly why a match
// with no Herdr ancestry behind it is held rather than killed.
var harnessSignatures = []string{
	"--dangerously-skip-permissions",
	"--dangerously-bypass-approvals-and-sandbox",
}

// harnessExecutables are the harness process names, matched when a signature
// flag is absent.
var harnessExecutables = []string{"claude", "codex", "kimi", "pi"}

// serverModules are the long-lived development servers a goblin leaves behind.
// The match is on the module path in the command line, which is how a server
// started from a worktree names the worktree it belongs to.
var serverModules = []string{"next", "vite", "webpack", "nodemon", "npm", "pnpm", "yarn", "vitest", "jest"}

// Classify is the whole audit as a pure function. Findings come back grouped
// by class in a stable order, so two runs over the same inventory produce the
// same report.
func Classify(inv Inventory) []Finding {
	supervised := supervisedPIDs(inv)
	fleet := descendants(inv.Processes, inv.FleetRootPIDs)
	tasks := make(map[string]Task, len(inv.Tasks))
	for _, task := range inv.Tasks {
		tasks[task.ID] = task
	}
	panes := make(map[string]Pane, len(inv.Panes))
	for _, pane := range inv.Panes {
		panes[pane.ID] = pane
	}

	findings := classifyProcesses(inv, supervised, fleet, tasks)
	findings = append(findings, classifyWorktrees(inv, tasks, panes)...)
	findings = append(findings, classifyMetas(inv, panes, supervised, fleet)...)
	for _, id := range inv.OrphanStatusIDs {
		findings = append(findings, Finding{
			Class:  OrphanStatus,
			TaskID: id,
			Detail: "status log with no matching meta",
			Action: "archive the log",
		})
	}
	SortFindings(findings)
	return findings
}

func classifyProcesses(inv Inventory, supervised, fleet map[int]bool, tasks map[string]Task) []Finding {
	var findings []Finding
	for _, process := range inv.Processes {
		if supervised[process.PID] {
			continue
		}
		switch {
		case isHarness(process):
			finding := Finding{
				Class:  OrphanProcess,
				PID:    process.PID,
				Detail: fmt.Sprintf("%s started %s has no pane; unsupervised harness", process.Name, process.Start.UTC().Format(time.RFC3339)),
				Action: "kill the process tree",
			}
			if worktree, ok := worktreeOf(process, inv.Worktrees); ok {
				finding.TaskID = worktree.TaskID
				finding.Path = worktree.Path
			}
			if !fleet[process.PID] {
				finding.Hold = "no Herdr ancestry, so this may not be a fleet process at all; name its pid with --force to kill it"
			}
			if hold := unresolvedPaneHold(inv); hold != "" {
				finding.Hold = hold
			}
			findings = append(findings, finding)
		case isServer(process):
			worktree, ok := worktreeOf(process, inv.Worktrees)
			if !ok {
				continue
			}
			task, known := tasks[worktree.TaskID]
			if known && !task.Terminal {
				// Its goblin is still working; the server is doing its job.
				continue
			}
			findings = append(findings, Finding{
				Class:  StaleServer,
				TaskID: worktree.TaskID,
				PID:    process.PID,
				Path:   worktree.Path,
				Detail: fmt.Sprintf("%s rooted in %s, whose task %s", process.Name, worktree.Path, taskOutcome(task, known)),
				Action: "kill the process tree",
				Hold:   unresolvedPaneHold(inv),
			})
		}
	}
	return findings
}

func classifyWorktrees(inv Inventory, tasks map[string]Task, panes map[string]Pane) []Finding {
	var findings []Finding
	for _, worktree := range inv.Worktrees {
		task, known := tasks[worktree.TaskID]
		if known {
			if pane, ok := panes[task.Meta.HerdrPaneID]; ok && pane.HasAgent {
				// A live goblin owns this directory.
				continue
			}
		}
		finding := Finding{
			Class:  OrphanWorktree,
			TaskID: worktree.TaskID,
			Path:   worktree.Path,
			Detail: "no live pane, and its task " + taskOutcome(task, known),
			Action: "return the worktree through cfo cleanup",
		}
		if known && !task.Terminal {
			// A goblin whose pane died mid-work leaks its worktree just as
			// surely as a finished one, so it is reported; it is held because
			// the task never said it was done.
			finding.Hold = "task has not reached a terminal status (latest verb " + verbText(task.Verb) + "); name its id with --force to reap it anyway"
		}
		findings = append(findings, finding)
	}
	return findings
}

func classifyMetas(inv Inventory, panes map[string]Pane, supervised, fleet map[int]bool) []Finding {
	worktrees := make(map[string]bool, len(inv.Worktrees))
	for _, worktree := range inv.Worktrees {
		worktrees[normalizePath(worktree.Path)] = true
	}
	var findings []Finding
	for _, task := range inv.Tasks {
		if _, ok := panes[task.Meta.HerdrPaneID]; ok {
			continue
		}
		if worktrees[normalizePath(task.Meta.Worktree)] {
			// The directory is still there, so OrphanWorktree owns this one:
			// returning the worktree retires the meta with it.
			continue
		}
		if taskHasProcess(task, inv.Processes, supervised, fleet) {
			continue
		}
		finding := Finding{
			Class:  OrphanMeta,
			TaskID: task.ID,
			Path:   task.Meta.Worktree,
			Detail: "no pane, no process, and no worktree left on disk; its task " + taskOutcome(task, true),
			Action: "force-archive the task record",
		}
		if !task.Terminal {
			finding.Hold = "task has not reached a terminal status (latest verb " + verbText(task.Verb) + "); name its id with --force to archive it anyway"
		}
		findings = append(findings, finding)
	}
	return findings
}

// taskHasProcess reports whether anything still running belongs to the task. A
// meta is only an orphan when nothing at all is left, so a process this sweep
// has already flagged as an orphan still counts as a process: the process
// finding is the one to act on, not the record behind it.
func taskHasProcess(task Task, processes []Process, supervised, fleet map[int]bool) bool {
	if task.Meta.Worktree == "" {
		return false
	}
	target := normalizePath(task.Meta.Worktree)
	for _, process := range processes {
		if !supervised[process.PID] && !fleet[process.PID] {
			continue
		}
		if strings.Contains(normalizePath(process.CommandLine), target) {
			return true
		}
	}
	return false
}

// supervisedPIDs is everything this sweep must never touch: each live pane's
// shell and foreground group, everything descended from them, and this
// process's own ancestry. The pane set is the real answer to whether something
// is supervised; the self set is what keeps the sweep from reporting the
// session it runs inside.
func supervisedPIDs(inv Inventory) map[int]bool {
	roots := make([]int, 0, len(inv.Panes)*2+len(inv.SelfPIDs))
	for _, pane := range inv.Panes {
		if pane.ShellPID != 0 {
			roots = append(roots, pane.ShellPID)
		}
		if pane.ForegroundPID != 0 {
			roots = append(roots, pane.ForegroundPID)
		}
	}
	roots = append(roots, inv.SelfPIDs...)
	return descendants(inv.Processes, roots)
}

// descendants returns roots plus every process reachable from one by parent
// links. A parent link is only followed downward, and a child that predates
// its recorded parent is not followed at all, so a reused pid cannot pull an
// unrelated process into the set.
func descendants(processes []Process, roots []int) map[int]bool {
	byParent := make(map[int][]Process, len(processes))
	start := make(map[int]time.Time, len(processes))
	for _, process := range processes {
		byParent[process.ParentPID] = append(byParent[process.ParentPID], process)
		start[process.PID] = process.Start
	}
	found := make(map[int]bool, len(roots))
	queue := make([]int, 0, len(roots))
	for _, root := range roots {
		if root != 0 && !found[root] {
			found[root] = true
			queue = append(queue, root)
		}
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range byParent[pid] {
			if found[child.PID] {
				continue
			}
			if parentStart, ok := start[pid]; ok && !child.Start.IsZero() && !parentStart.IsZero() && child.Start.Before(parentStart) {
				continue
			}
			found[child.PID] = true
			queue = append(queue, child.PID)
		}
	}
	return found
}

// unresolvedPaneHold refuses every kill while the supervised set is known to
// be incomplete. A pane that exists but cannot say which processes are running
// in it leaves its live harness looking exactly like an orphan, and killing a
// working goblin is the one mistake this whole package exists to avoid.
func unresolvedPaneHold(inv Inventory) string {
	if len(inv.UnresolvedPanes) == 0 {
		return ""
	}
	return fmt.Sprintf("%d pane(s) could not report their process identity (%s), so a live goblin is indistinguishable from an orphan here; fix Herdr or name the pid with --force", len(inv.UnresolvedPanes), strings.Join(inv.UnresolvedPanes, ", "))
}

func isHarness(process Process) bool {
	command := strings.ToLower(process.CommandLine)
	for _, signature := range harnessSignatures {
		if strings.Contains(command, signature) {
			return true
		}
	}
	name := executableName(process.Name)
	for _, executable := range harnessExecutables {
		if name == executable {
			return true
		}
	}
	return false
}

func isServer(process Process) bool {
	command := normalizePath(process.CommandLine)
	for _, module := range serverModules {
		// The separators keep next from matching a directory called
		// nextcloud, and npm from matching npmrc.
		if strings.Contains(command, "\\"+module+"\\") || strings.Contains(command, " "+module+" ") || strings.HasSuffix(command, " "+module) {
			return true
		}
	}
	return false
}

// worktreeOf finds the worktree a process is rooted in by looking for its path
// in the command line. A dev server started in a worktree runs that worktree's
// own node_modules copy, so the worktree path is in the module path it was
// launched with.
//
// ponytail: command line only. A server launched purely relative to its
// working directory carries no path, and reading another process's working
// directory needs a PEB walk; add that only if a real leak is ever missed
// this way.
func worktreeOf(process Process, worktrees []WorktreeDir) (WorktreeDir, bool) {
	command := normalizePath(process.CommandLine)
	best := WorktreeDir{}
	found := false
	for _, worktree := range worktrees {
		path := normalizePath(worktree.Path)
		if path == "" || !strings.Contains(command, path) {
			continue
		}
		// Longest match wins so a nested worktree beats its parent.
		if !found || len(path) > len(normalizePath(best.Path)) {
			best, found = worktree, true
		}
	}
	return best, found
}

func taskOutcome(task Task, known bool) string {
	if !known {
		return "has no metadata record"
	}
	if task.Terminal {
		return "finished (" + verbText(task.Verb) + ")"
	}
	return "is recorded as " + verbText(task.Verb)
}

func verbText(verb string) string {
	if verb == "" {
		return "no status"
	}
	return verb
}

// IsTerminal reports whether a status verb ends a task. done and failed are
// the two outcomes a goblin reports through cfo notify; every other verb
// leaves the task somebody's problem.
func IsTerminal(verb string) bool {
	return verb == "done" || verb == "failed"
}

// normalizePath folds a Windows path for comparison: lowercase, with forward
// slashes rewritten as backslashes, so a path written either way matches.
func normalizePath(path string) string {
	return strings.ToLower(strings.ReplaceAll(path, "/", "\\"))
}

func executableName(name string) string {
	base := strings.ToLower(filepath.Base(name))
	return strings.TrimSuffix(base, ".exe")
}

// SortFindings orders findings for a stable report: by class, then task, then
// pid, then path.
func SortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Class != right.Class {
			return left.Class < right.Class
		}
		if left.TaskID != right.TaskID {
			return left.TaskID < right.TaskID
		}
		if left.PID != right.PID {
			return left.PID < right.PID
		}
		return left.Path < right.Path
	})
}
