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
	"slices"
	"sort"
	"strconv"
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
	// directory left on disk. A meta whose directory still exists is left to
	// the finding at that path, because one resource must not be reported
	// twice; the record reports on a later sweep, once the directory is gone.
	OrphanMeta Class = "orphan_meta"
	// OrphanStatus is a state/<id>.status log with no matching meta.
	OrphanStatus Class = "orphan_status"
	// OrphanDirectory is a directory under a project's .worktrees/ that the
	// project does not register as a worktree: the empty shell a task that
	// died leaves behind. It is a separate class because it is not a worktree,
	// and every git question asked from inside one is answered by the
	// enclosing repository instead.
	OrphanDirectory Class = "orphan_directory"
)

// Finding is one classified resource and the verdict on acting upon it.
// Holds, when any are recorded, are why --apply will leave it alone: an
// unattributable process, a task that has not finished, work that would be
// destroyed. Every gate that refuses records its refusal here rather than
// dropping the finding, so the report says what was found AND why it was not
// touched.
type Finding struct {
	Class  Class  `json:"class"`
	TaskID string `json:"task_id,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
	Action string `json:"action"`
	// Holds are the refusals, each carrying the key that clears it. They
	// accumulate, so a finding refused for two reasons answers to both, and
	// neither key answers for the other. The rendered hold is Hold(), a
	// function of these and never a second copy kept beside them.
	Holds []Refusal `json:"holds,omitempty"`
}

// Refusal is one reason a finding is held, together with what clears it.
//
// Each refusal answers to its own authority and to nothing else. Naming a pid
// accepts responsibility for that process and can never say whether a task has
// finished; naming a task id says the task is over and can never speak for a
// process still holding its directory. Some refusals answer to no --force at
// all, because they exist to stop work being destroyed, and that is not the
// operator's judgement to waive through this command.
type Refusal struct {
	Reason string `json:"reason"`
	// Key is what --force must name to clear this one, and nothing else ever
	// clears it. Every refusal a --force can answer carries one, because a
	// refusal with no key of its own is one that any identifier the finding
	// happens to carry could clear, which is the opposite of the rule.
	Key string `json:"key,omitempty"`
	// Propose says the rendered hold offers --force as the remedy, which is
	// true only where forcing is the operator's judgement to make.
	Propose bool `json:"propose,omitempty"`
	// Absolute is a refusal no --force clears.
	Absolute bool `json:"absolute,omitempty"`
}

// refuseUntilEstablished records a refusal where the sweep could not establish
// something and the answer is to establish it. The key is still the authority
// that refusal answers to and nothing else clears it, but the rendered hold
// does not offer --force, because proposing it is how fourteen findings came
// to invite an operator to kill the desktop application and three live review
// agents. The reason carries its own remedy in its own words.
func (f *Finding) refuseUntilEstablished(reason, key string) {
	f.add(Refusal{Reason: reason, Key: key})
}

// refuseUnlessForced records a refusal where a --force IS the legitimate
// answer, because it is a judgement only the operator can make: the pid for a
// judgement about a process, the task id for whether a task has finished. Only
// that key clears it, and the rendered hold says so.
func (f *Finding) refuseUnlessForced(reason, key string) {
	f.add(Refusal{Reason: reason, Key: key, Propose: true})
}

// refuseAbsolutely records a refusal no --force clears.
func (f *Finding) refuseAbsolutely(reason string) {
	f.add(Refusal{Reason: reason, Absolute: true})
}

// add appends a refusal. Appending is the whole point: every site adds, none
// assigns, so a refusal already recorded cannot be dropped by a later one that
// happens to run after it.
func (f *Finding) add(refusal Refusal) {
	if refusal.Reason == "" {
		return
	}
	f.Holds = append(f.Holds, refusal)
}

// Held reports whether anything is refusing this finding.
func (f Finding) Held() bool {
	return len(f.Holds) > 0
}

// Hold renders every refusal and then says what answers them, derived from
// the refusals themselves rather than stored beside them. It describes which
// reasons a --force answers and never that the sweep will act: a cleared hold
// only means the action is attempted, and a HELD line promising an outcome it
// cannot deliver is the defect this sweep exists to stop reporting.
func (f Finding) Hold() string {
	reasons := make([]string, 0, len(f.Holds))
	absolute := false
	unestablished := false
	var keys []string
	for _, refusal := range f.Holds {
		reasons = append(reasons, refusal.Reason)
		switch {
		case refusal.Absolute:
			absolute = true
		case refusal.Propose:
			if !slices.Contains(keys, refusal.Key) {
				keys = append(keys, refusal.Key)
			}
		default:
			unestablished = true
		}
	}
	text := strings.Join(reasons, "; also ")
	if absolute {
		return text + ". No --force clears this"
	}
	if len(keys) == 0 {
		return text
	}
	named := "Name " + keys[0] + " with --force"
	if len(keys) > 1 {
		named = "Name every one of " + strings.Join(keys, " and ") + " with --force"
	}
	switch {
	case unestablished:
		// It says what is true and stops there. A --force naming that key does
		// clear an unestablished refusal, and saying otherwise would be this
		// command describing what it wishes were so, which is the habit the
		// whole branch exists to break. What it will not do is propose the
		// override as the remedy, because the remedy is the evidence.
		return text + ". " + named + " to take responsibility for that much. The rest is evidence the sweep could not gather rather than a judgement to make, so resolve it rather than overriding it"
	case len(keys) > 1:
		return text + ". " + named + " to take responsibility for every reason above, because each refusal answers only to its own"
	}
	return text + ". " + named + " to take responsibility for it"
}

// clearForced drops the refusals the operator has taken responsibility for and
// keeps the rest, so a force that answers one refusal cannot carry a finding
// past another it says nothing about.
func (f *Finding) clearForced(force map[string]bool) {
	if len(force) == 0 || len(f.Holds) == 0 {
		return
	}
	kept := make([]Refusal, 0, len(f.Holds))
	for _, refusal := range f.Holds {
		if refusal.Absolute || !force[refusal.Key] {
			kept = append(kept, refusal)
		}
	}
	f.Holds = kept
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
	if hold := f.Hold(); hold != "" {
		b.WriteString(" | HELD: " + hold)
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

// Registration is what a project repository answered about a directory under
// its .worktrees/. It is three-valued because "the repository does not list
// this directory" and "the repository could not be asked" are different facts,
// and the sweep must not act on the second as though it were the first.
type Registration string

const (
	// RegistrationUnknown is the zero value: the repository could not be
	// asked, so nothing was established. It classifies as a worktree, which
	// is the gated class, because a sweep that proved nothing must not tell
	// the operator a live worktree is a directory a dead task left behind.
	RegistrationUnknown Registration = ""
	// RegistrationListed is a directory the repository lists as a worktree.
	RegistrationListed Registration = "listed"
	// RegistrationUnlisted is a directory the repository answered about and
	// does not list: the shell a task that died leaves behind. It is the
	// premise the whole worktree classification rests on having stopped
	// holding, because git run inside such a shell answers for the enclosing
	// repository instead.
	RegistrationUnlisted Registration = "unlisted"
)

// WorktreeDir is one directory found under a project's .worktrees/.
type WorktreeDir struct {
	Path         string
	Project      string
	TaskID       string
	Registration Registration
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
	// UnreadableTasks are the ids whose state/<id>.meta could not be read.
	// Without them such a task is simply absent, and its directory then
	// classifies as having no record at all, which carries no hold: a record
	// the sweep could not read would produce a more actionable finding than
	// one it read and found unfinished. Whether the task finished is exactly
	// what is unknown here, so it is gated.
	UnreadableTasks []string
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

// desktopAppMarker is the install path of the Overlord's Claude Desktop
// application. It is a packaged app, so every one of its processes runs from
// under WindowsApps\Claude_<version>_<publisher>\app\claude.exe.
const desktopAppMarker = `\windowsapps\claude_`

// gateExecutable supervises a no-mistakes review round and launches the
// reviewer harnesses under it. Those harnesses are as supervised as a goblin
// in a pane: killing one ends a review round for a goblin that is working.
const gateExecutable = "no-mistakes"

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

	unreadable := make(map[string]bool, len(inv.UnreadableTasks))
	for _, id := range inv.UnreadableTasks {
		unreadable[id] = true
	}

	findings := classifyProcesses(inv, supervised, fleet, tasks, unreadable)
	findings = append(findings, classifyWorktrees(inv, supervised, tasks, panes, unreadable)...)
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

func classifyProcesses(inv Inventory, supervised, fleet map[int]bool, tasks map[string]Task, unreadable map[string]bool) []Finding {
	desktop := descendants(inv.Processes, rootsMatching(inv.Processes, isDesktopApp))
	gates := descendants(inv.Processes, rootsMatching(inv.Processes, isGateSupervisor))
	var findings []Finding
	for _, process := range inv.Processes {
		if supervised[process.PID] {
			continue
		}
		switch {
		case isHarness(process):
			if isNonFleetHarness(process, desktop, gates) {
				// Not a fleet process at all, so not this sweep's business:
				// reporting it would wake the CFO for something no action of
				// its own could ever be right about.
				continue
			}
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
				finding.refuseUntilEstablished(unidentifiedHold, strconv.Itoa(process.PID))
			}
			finding.refuseUntilEstablished(unresolvedPaneHold(inv), strconv.Itoa(process.PID))
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
			unreadableRecord := unreadable[worktree.TaskID]
			finding := Finding{
				Class:  StaleServer,
				TaskID: worktree.TaskID,
				PID:    process.PID,
				Path:   worktree.Path,
				Detail: fmt.Sprintf("%s rooted in %s, whose task %s", process.Name, worktree.Path, taskOutcome(task, known, unreadableRecord)),
				Action: "kill the process tree",
			}
			finding.refuseUntilEstablished(unresolvedPaneHold(inv), strconv.Itoa(process.PID))
			if unreadableRecord {
				// Killing this server says the task behind it is over, and an
				// unreadable record is the one thing that cannot say so. It is
				// added, not assigned, so an unrelated pane that could not
				// report its identity cannot drop it.
				finding.refuseUnlessForced(unfinishedHold(task, known, true), finding.TaskID)
			}
			findings = append(findings, finding)
		}
	}
	return findings
}

func classifyWorktrees(inv Inventory, supervised map[int]bool, tasks map[string]Task, panes map[string]Pane, unreadable map[string]bool) []Finding {
	var findings []Finding
	for _, worktree := range inv.Worktrees {
		task, known := tasks[worktree.TaskID]
		if known {
			if pane, ok := panes[task.Meta.HerdrPaneID]; ok && pane.HasAgent {
				// A live goblin owns this directory. This outranks
				// registration: "could not confirm a worktree" is not
				// evidence against a pane that is holding an agent right now.
				continue
			}
		}
		if worktree.Registration == RegistrationUnlisted {
			findings = append(findings, classifyDirectory(inv, supervised, worktree, task, known, unreadable[worktree.TaskID]))
			continue
		}
		finding := Finding{
			Class:  OrphanWorktree,
			TaskID: worktree.TaskID,
			Path:   worktree.Path,
			Detail: "no live pane, and its task " + taskOutcome(task, known, unreadable[worktree.TaskID]),
			Action: "return the worktree through cfo cleanup",
		}
		// A goblin whose pane died mid-work leaks its worktree just as surely
		// as a finished one, so it is reported; it is held because the task
		// never said it was done, or because nothing can say whether it did.
		finding.refuseUnlessForced(unfinishedHold(task, known, unreadable[worktree.TaskID]), finding.TaskID)
		findings = append(findings, finding)
	}
	return findings
}

// unfinishedHold is why a directory may not be retired yet: the task behind it
// never said it was done, or its record could not be read at all. Both are the
// same refusal, because both mean the sweep cannot show the work is over.
func unfinishedHold(task Task, known, unreadable bool) string {
	switch {
	case unreadable:
		return "its task record could not be read, so whether the task finished is unknown"
	case known && !task.Terminal:
		return "task has not reached a terminal status (latest verb " + verbText(task.Verb) + ")"
	}
	return ""
}

// classifyDirectory reports a directory under .worktrees/ that the project
// does not register as a worktree. It is deliberately not an OrphanWorktree:
// the premise every worktree question rests on has stopped holding here, and a
// git command run inside such a shell is answered by the enclosing repository,
// which is how an empty directory came to be reported as having the parent
// repository's uncommitted changes.
//
// A shell that cannot be removed is a process using it as its working
// directory, and that process is the real leak. The command lines are searched
// for one; a working directory is not readable from a process listing, so when
// nothing names the path the finding says that rather than guessing.
func classifyDirectory(inv Inventory, supervised map[int]bool, dir WorktreeDir, task Task, known, unreadable bool) Finding {
	finding := Finding{
		Class:  OrphanDirectory,
		TaskID: dir.TaskID,
		Path:   dir.Path,
		Detail: dir.Project + " does not list it in git worktree list, so it is a directory a dead task left behind, not a worktree",
		Action: "remove the empty directory",
	}
	if pid, ok := processNaming(dir.Path, inv, supervised); ok {
		finding.PID = pid
		finding.Detail += fmt.Sprintf("; pid %d names it on its command line", pid)
		finding.refuseUnlessForced(fmt.Sprintf("pid %d is still using this directory, and that process is the leak here, not the directory it holds", pid), strconv.Itoa(pid))
	} else {
		finding.Detail += "; no command line names it, and a working directory is not readable from a process listing, so if the removal fails a leaked process is holding it open"
	}
	finding.refuseUnlessForced(unfinishedHold(task, known, unreadable), finding.TaskID)
	return finding
}

// processNaming finds an unsupervised process whose command line names the
// directory. It is the cheap half of the question: it catches a server or a
// tool launched with the path as an argument, and misses one that merely has
// it as its working directory.
func processNaming(path string, inv Inventory, supervised map[int]bool) (int, bool) {
	for _, process := range inv.Processes {
		if supervised[process.PID] {
			continue
		}
		if namesPath(process.CommandLine, path) {
			return process.PID, true
		}
	}
	return 0, false
}

// namesPath reports whether a command line names a directory. The match ends
// at a boundary because task ids are operator-chosen names, so one is
// routinely a prefix of another: without it, a process working in gb-old-2 is
// read as the process holding gb-old open, and the sweep names the wrong pid
// as the leak.
func namesPath(commandLine, path string) bool {
	target := normalizePath(path)
	if target == "" {
		return false
	}
	command := normalizePath(commandLine)
	for offset := 0; offset+len(target) <= len(command); {
		index := strings.Index(command[offset:], target)
		if index < 0 {
			return false
		}
		end := offset + index + len(target)
		if end == len(command) || isPathBoundary(command[end]) {
			return true
		}
		offset += index + 1
	}
	return false
}

func isPathBoundary(char byte) bool {
	switch char {
	case '\\', '"', '\'', ' ', '\t':
		return true
	}
	return false
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
			// The directory is still there, so the finding at that path owns
			// this one: the record reports on a later sweep, once the
			// directory is gone.
			continue
		}
		if taskHasProcess(task, inv.Processes, supervised, fleet) {
			continue
		}
		finding := Finding{
			Class:  OrphanMeta,
			TaskID: task.ID,
			Path:   task.Meta.Worktree,
			Detail: "no pane, no process, and no directory left on disk; its task " + taskOutcome(task, true, false),
			Action: "force-archive the task record",
		}
		if !task.Terminal {
			finding.refuseUnlessForced("task has not reached a terminal status (latest verb "+verbText(task.Verb)+")", finding.TaskID)
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
	for _, process := range processes {
		if !supervised[process.PID] && !fleet[process.PID] {
			continue
		}
		if namesPath(process.CommandLine, task.Meta.Worktree) {
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
	return fmt.Sprintf("%d pane(s) could not report their process identity (%s), so a live goblin is indistinguishable from an orphan here; fix Herdr so those panes report what is running in them, then sweep again", len(inv.UnresolvedPanes), strings.Join(inv.UnresolvedPanes, ", "))
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

// unidentifiedHold is what a harness-shaped process gets when the sweep has
// run out of evidence. It says what could not be determined and stops there:
// the populations it cannot place are the Overlord's own sessions and tools,
// and every one of the fourteen findings that named --force here was a process
// that must never be killed.
const unidentifiedHold = "could not determine what this process belongs to: it has no Herdr ancestry, and it is neither the desktop application nor a gate agent; identify it before anything acts on it"

// isNonFleetHarness reports whether a harness-shaped process is not a fleet
// process at all. The image name alone says nothing on this machine: claude.exe
// is the Overlord's desktop application a dozen times over (one parent plus the
// Chromium children it spawns, which run the same executable), and one more per
// no-mistakes review round in progress. Both are read from the evidence the
// scan already has, the ancestry and the command line, and both must be left
// alone entirely: forcing one closes the application the Overlord is using or
// ends a review round for a goblin that is working.
func isNonFleetHarness(process Process, desktopApp, gateAgents map[int]bool) bool {
	switch {
	case desktopApp[process.PID]:
		// The packaged application, by its own install path or an ancestor's.
		return true
	case gateAgents[process.PID]:
		// A reviewer launched by a round in progress.
		return true
	}
	return false
}

// isDesktopApp matches the packaged desktop application by its install path,
// which every one of its processes carries.
func isDesktopApp(process Process) bool {
	return strings.Contains(normalizePath(process.CommandLine), desktopAppMarker)
}

// isGateSupervisor matches no-mistakes, whose children are the reviewer
// harnesses of a round in progress.
func isGateSupervisor(process Process) bool {
	return executableName(process.Name) == gateExecutable
}

// rootsMatching collects the pids of every process the predicate accepts, for
// descendants to walk down from.
func rootsMatching(processes []Process, match func(Process) bool) []int {
	var roots []int
	for _, process := range processes {
		if match(process) {
			roots = append(roots, process.PID)
		}
	}
	return roots
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
	best := WorktreeDir{}
	found := false
	for _, worktree := range worktrees {
		path := normalizePath(worktree.Path)
		if !namesPath(process.CommandLine, worktree.Path) {
			continue
		}
		// Longest match wins so a nested worktree beats its parent.
		if !found || len(path) > len(normalizePath(best.Path)) {
			best, found = worktree, true
		}
	}
	return best, found
}

func taskOutcome(task Task, known, unreadable bool) string {
	if unreadable {
		// "no record" and "a record nothing could read" must not read the
		// same: the first is a fact about the task, the second is a fact
		// about the sweep.
		return "has a metadata record that could not be read"
	}
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
