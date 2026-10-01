// Package reap finds and retires the fleet resources nothing else notices: a
// harness process whose native terminal host is gone, a dev server
// left running in a worktree no live goblin is working in, the worktree,
// metadata and status records left behind when a task ends without a clean
// cleanup, and the directory under a project's .worktrees/ that the project
// does not register as a worktree at all.
//
// No single source sees all of it. cfo knows the tasks it started and the
// native terminals that still run, only the operating system knows what is
// still running, and only a project's own git worktree list says which
// directories under it are worktrees, so classification cross-references all
// five (state, terminal hosts, processes, worktree directories, registration)
// and trusts none of them alone.
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
	// OrphanProcess is a harness process whose terminal no longer runs. It is
	// the dangerous one: unsupervised, invisible to every CFO surface, and
	// still able to spend tokens.
	OrphanProcess Class = "orphan_process"
	// StaleServer is a long-lived child (a next dev, a vite server) rooted in
	// a worktree with no live evidence of its goblin: no terminal of its
	// running, whatever the status log says. One whose task never reported a
	// terminal verb is still reported, and held: an abandoned server is a leak
	// whatever the log says, and whether the work behind it is over is the
	// operator's call.
	StaleServer Class = "stale_server"
	// OrphanWorktree is a worktree directory with no live terminal of a goblin
	// behind it. One whose task never reported a terminal status is still
	// reported, and held: a goblin that lost its terminal mid-work leaks its
	// worktree exactly as a finished one does, and the operator needs to see
	// it either way.
	OrphanWorktree Class = "orphan_worktree"
	// OrphanMeta is a state/<id>.meta with no live terminal, no process, and no
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
	// Registered says the project answered git worktree list and listed this
	// path. It is what makes the administrative entry the project's to prune
	// once the directory goes: where the project could not be asked, no
	// registration was established, so there is none to clear and no
	// repository established as the one to ask.
	Registered bool `json:"registered,omitempty"`
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
		}
	}
	text := strings.Join(reasons, "; also ")
	if absolute {
		return text + ". No --force clears this"
	}
	if len(keys) == 0 {
		return text
	}
	// An unestablished refusal answering a key the operator is about to be
	// told to name is cleared by that same --force, so only one answering a
	// key nobody was told to name leaves anything standing behind it. Saying
	// otherwise would tell the operator that a hold survives a force that in
	// fact ends the process, which is the reading the whole branch exists to
	// stop. The reasons still carry their own remedies in their own words.
	unestablished := false
	for _, refusal := range f.Holds {
		if !refusal.Propose && !slices.Contains(keys, refusal.Key) {
			unestablished = true
		}
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
	// Cwd is the directory the process runs in. It is read only for the
	// harness-shaped processes and their ancestors, the one place it decides
	// anything, and is empty wherever it was not read or could not be.
	Cwd string `json:"cwd,omitempty"`
}

// Task is one state record, reduced to what classification needs.
type Task struct {
	ID       string
	Meta     state.TaskMeta
	Verb     string
	Terminal bool
	IsPaused bool
	// Hosted says a native task's terminal still runs: Classify sets it from
	// the inventory's live host records, and it is what a goblin is alive by.
	Hosted bool
	// HostUnreadable says a native task's host record could not be read, so
	// whether its goblin still runs is unknown.
	HostUnreadable bool
}

// NativeHost is the record of one native terminal's host: the terminal it
// runs, the host's pid, and when the host recorded itself.
type NativeHost struct {
	ID      string
	HostPID int
	Started time.Time
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
	// Created is when the directory was made, zero when it could not be
	// read. It is what tells a live goblin's extra worktree from an older
	// directory that merely shares the start of its name.
	Created time.Time
}

// Inventory is the cross-referenced evidence one classification runs over. It
// is a plain value with no I/O so the classification is a pure function over
// synthetic fixtures; Collector builds the production one.
type Inventory struct {
	// StateDir is this home's state directory. A native terminal host run
	// with any other --state belongs to a scratch CFO home some goblin's test
	// or proof set up, not to the fleet.
	StateDir        string
	Tasks           []Task
	OrphanStatusIDs []string
	Processes       []Process
	Worktrees       []WorktreeDir
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
	// NativeHosts are the host records of native terminals. A goblin's
	// harness, and everything the harness starts, runs under its terminal's
	// host.
	NativeHosts []NativeHost
	// UnreadableHosts are the terminal ids whose state/hosts/<id>.json could
	// not be read, and every native task's id when state/hosts itself could
	// not be. Such a goblin may be alive or gone, so whatever rests on it
	// being gone is held.
	UnreadableHosts []string
}

// harnessSignatures are the distinctive command-line fragments a CFO-launched
// harness carries. They mirror internal/harness's adapters, and
// TestHarnessSignaturesMatchAdapters asserts they still do, so a flag that
// changes there cannot leave this sweep blind.
//
// kimi and pi build no distinctive flag of their own, so they are matched on
// the executable name alone. That is weaker, which is exactly why a match
// outside every terminal of this fleet is held rather than killed.
var harnessSignatures = []string{
	"--dangerously-skip-permissions",
	"--dangerously-bypass-approvals-and-sandbox",
}

// harnessExecutables are the harness process names, matched when a signature
// flag is absent.
var harnessExecutables = []string{"claude", "codex", "kimi", "pi"}

// desktopAppMarkers are the install paths of the Overlord's desktop
// applications. Each is a packaged app whose own processes run from under
// WindowsApps: Claude Desktop from Claude_<version>_<publisher>\app\claude.exe,
// and the Codex app from OpenAI.Codex_<version>_<publisher>\app\ChatGPT.exe,
// whose codex.exe app server runs from outside the package as its child.
var desktopAppMarkers = []string{`\windowsapps\claude_`, `\windowsapps\openai.codex_`}

// gateExecutable supervises a no-mistakes review round and launches the
// reviewer harnesses under it. Those harnesses are as supervised as a goblin
// in its terminal: killing one ends a review round for a goblin that is
// working.
const gateExecutable = "no-mistakes"

// serverModules are the long-lived development servers a goblin leaves behind.
// The match is on the module path in the command line, which is how a server
// started from a worktree names the worktree it belongs to.
var serverModules = []string{"next", "vite", "webpack", "nodemon", "npm", "pnpm", "yarn", "vitest", "jest"}

// Classify is the whole audit as a pure function. Findings come back grouped
// by class in a stable order, so two runs over the same inventory produce the
// same report.
func Classify(inv Inventory) []Finding {
	hosts := liveHosts(inv)
	inv.Tasks = slices.Clone(inv.Tasks)
	for i, task := range inv.Tasks {
		_, running := hosts[task.ID]
		native := task.Meta.Backend == "native"
		inv.Tasks[i].Hosted = running && native
		inv.Tasks[i].HostUnreadable = native && slices.Contains(inv.UnreadableHosts, task.ID)
	}
	supervised := supervisedPIDs(inv, hosts)
	tasks := make(map[string]Task, len(inv.Tasks))
	for _, task := range inv.Tasks {
		tasks[task.ID] = task
	}

	unreadable := make(map[string]bool, len(inv.UnreadableTasks))
	for _, id := range inv.UnreadableTasks {
		unreadable[id] = true
	}

	findings := classifyProcesses(inv, supervised, tasks, unreadable)
	findings = append(findings, classifyWorktrees(inv, supervised, tasks, unreadable)...)
	findings = append(findings, classifyMetas(inv, supervised)...)
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

func classifyProcesses(inv Inventory, supervised map[int]bool, tasks map[string]Task, unreadable map[string]bool) []Finding {
	desktop := descendants(inv.Processes, rootsMatching(inv.Processes, isDesktopApp))
	gates := descendants(inv.Processes, rootsMatching(inv.Processes, isGateSupervisor))
	byPID := make(map[int]Process, len(inv.Processes))
	for _, process := range inv.Processes {
		byPID[process.PID] = process
	}
	owns := func(dir, task string) bool {
		return runsForTask(dir, task, inv, tasks, unreadable)
	}
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
			fixture, underFixture := fixtureOf(process, byPID, inv.StateDir, owns)
			owner := ""
			if underFixture {
				dirs := fixture.Dirs
				if task, ok := tasks[fixture.GoTestTask]; fixture.GoTestTask != "" && ok {
					dirs = append(dirs, task.Meta.Worktree)
				}
				inUse := false
				for _, dir := range dirs {
					dir = scratchpadOwner(dir, inv.Worktrees)
					inUse = inUse || fixtureInUse(dir, inv, tasks, gates, unreadable)
					if _, ok := worktreeHolding(dir, inv.Worktrees); ok && owner == "" {
						owner = dir
					}
				}
				if inUse {
					// A stand-in harness a live goblin's or gate's test is
					// running: its scratch home's host or test binary outlived
					// the script that started it, so its ancestry reaches no
					// terminal of this fleet.
					continue
				}
				if fixture.ScratchHome && owner == "" {
					// A scratch CFO home no goblin's worktree or scratchpad
					// can be tied to is somebody's test or proof, never this
					// fleet's orphan.
					continue
				}
				if owner == "" {
					owner = fixture.Cwd
				}
			}
			finding := Finding{
				Class:  OrphanProcess,
				PID:    process.PID,
				Detail: fmt.Sprintf("%s started %s runs under no terminal of this fleet; unsupervised harness", process.Name, process.Start.UTC().Format(time.RFC3339)),
				Action: "kill the process tree",
			}
			if worktree, ok := worktreeOf(process, inv.Worktrees); ok {
				finding.TaskID = worktree.TaskID
				finding.Path = worktree.Path
			} else if worktree, ok := worktreeHolding(owner, inv.Worktrees); underFixture && ok {
				finding.TaskID = worktree.TaskID
				finding.Path = worktree.Path
			}
			if underFixture {
				where := owner
				if where == "" {
					where = "a directory that could not be read"
				}
				runsUnder := fmt.Sprintf("native terminal host pid %d, of a CFO home other than this one", fixture.PID)
				if fixture.GoTestTask != "" {
					runsUnder = fmt.Sprintf("%s pid %d, run from task %s's Go temporary directory", fixture.Name, fixture.PID, fixture.GoTestTask)
				}
				finding.Detail += fmt.Sprintf(" (a test fixture's: it runs under %s, started for %s, where nothing live works any more)", runsUnder, where)
				if worktree, ok := worktreeHolding(owner, inv.Worktrees); ok {
					task, known := ownerOf(worktree, tasks, inv.Worktrees, unreadable)
					finding.refuseUnlessForced(unreadableHostHold(task, known), ownerKey(task, known, worktree))
				}
			}
			finding.refuseUntilEstablished(unidentifiedHold, strconv.Itoa(process.PID))
			// Ending a process is the one action here that cannot be undone
			// and that costs somebody else their work, so it answers to the
			// operator naming that process and to nothing else. A sweep run
			// to tidy a status log acts on every finding it is not holding,
			// and on 19 September 2026 that killed a goblin's dev server
			// mid-suite. It is recorded here rather than at the gate so that
			// a finding already held for another reason still states the pid
			// its only action needs, and one --force naming every key on the
			// line clears it in a single pass.
			finding.refuseUnlessForced(killNeedsItsOwnPID, strconv.Itoa(process.PID))
			findings = append(findings, finding)
		case isServer(process):
			worktree, ok := worktreeOf(process, inv.Worktrees)
			if !ok {
				continue
			}
			task, known := ownerOf(worktree, tasks, inv.Worktrees, unreadable)
			if goblinIsAlive(task, known) {
				// Its goblin is still working; the server is doing its job.
				continue
			}
			unreadableRecord := unreadable[worktree.TaskID]
			finding := Finding{
				Class:  StaleServer,
				TaskID: worktree.TaskID,
				PID:    process.PID,
				Path:   worktree.Path,
				Detail: fmt.Sprintf("%s rooted in %s, with %s%s", process.Name, worktree.Path, placementOutcome(task, known, unreadableRecord), extraWorktreeNote(task, known, worktree)),
				Action: "kill the process tree",
			}
			// A task that never said it was done, or whose record could not be
			// read, is reported and held rather than skipped: the server is
			// still a leak once its goblin is gone, and the operator is the
			// one who decides that the work behind it is over.
			finding.refuseUnlessForced(unfinishedHold(task, known, unreadableRecord), ownerKey(task, known, worktree))
			finding.refuseUnlessForced(unreadableHostHold(task, known), ownerKey(task, known, worktree))
			finding.refuseUnlessForced(killNeedsItsOwnPID, strconv.Itoa(process.PID))
			findings = append(findings, finding)
		}
	}
	return findings
}

func classifyWorktrees(inv Inventory, supervised map[int]bool, tasks map[string]Task, unreadable map[string]bool) []Finding {
	var findings []Finding
	for _, worktree := range inv.Worktrees {
		task, known := ownerOf(worktree, tasks, inv.Worktrees, unreadable)
		if known && task.IsPaused {
			continue
		}
		if goblinIsAlive(task, known) {
			// A live goblin owns this directory, as its worktree or as an
			// extra one it made for another branch. This outranks
			// registration: "could not confirm a worktree" is not evidence
			// against a terminal that is running right now.
			continue
		}
		if worktree.Registration == RegistrationUnlisted {
			findings = append(findings, classifyDirectory(inv, supervised, worktree, task, known, unreadable[worktree.TaskID]))
			continue
		}
		finding := Finding{
			Class:      OrphanWorktree,
			TaskID:     worktree.TaskID,
			Path:       worktree.Path,
			Registered: worktree.Registration == RegistrationListed,
			Detail:     placementOutcome(task, known, unreadable[worktree.TaskID]) + extraWorktreeNote(task, known, worktree),
			Action:     "return the worktree through cfo cleanup",
		}
		// A goblin whose terminal died mid-work leaks its worktree just as surely
		// as a finished one, so it is reported; it is held because the task
		// never said it was done, or because nothing can say whether it did.
		finding.refuseUnlessForced(unfinishedHold(task, known, unreadable[worktree.TaskID]), ownerKey(task, known, worktree))
		finding.refuseUnlessForced(unreadableHostHold(task, known), ownerKey(task, known, worktree))
		findings = append(findings, finding)
	}
	return findings
}

// ownerOf returns the task a directory under .worktrees/ belongs to. That is
// the task its gb-<id> name records. A goblin also makes extra worktrees for
// other branches, named gb-<id>-<suffix>, and one no record names belongs to
// the task it extends: the longest such id, in the same project, whose own
// worktree was made before it. The age check keeps an older directory that
// merely shares the start of a live task's name from being read as that
// task's, and a directory whose own record could not be read is never handed
// to another task.
func ownerOf(dir WorktreeDir, tasks map[string]Task, worktrees []WorktreeDir, unreadable map[string]bool) (Task, bool) {
	if task, ok := tasks[dir.TaskID]; ok {
		return task, true
	}
	if unreadable[dir.TaskID] || dir.Created.IsZero() {
		return Task{}, false
	}
	var owner Task
	found := false
	for id, task := range tasks {
		if !strings.HasPrefix(strings.ToLower(dir.TaskID), strings.ToLower(id)+"-") || (found && len(id) <= len(owner.ID)) {
			continue
		}
		if normalizePath(filepath.Clean(task.Meta.Project)) != normalizePath(filepath.Clean(dir.Project)) {
			continue
		}
		own, ok := worktreeAt(task.Meta.Worktree, worktrees)
		if !ok || own.Created.IsZero() || dir.Created.Before(own.Created) {
			continue
		}
		owner, found = task, true
	}
	return owner, found
}

func worktreeAt(path string, worktrees []WorktreeDir) (WorktreeDir, bool) {
	for _, worktree := range worktrees {
		if normalizePath(filepath.Clean(worktree.Path)) == normalizePath(filepath.Clean(path)) {
			return worktree, true
		}
	}
	return WorktreeDir{}, false
}

// ownerKey is the id a refusal about whether a directory's work is over
// answers to: the task that owns it, which for an extra worktree is the
// goblin's own task. The finding itself keeps the directory's own id, so
// acting on it returns that directory and never the owner's worktree.
func ownerKey(task Task, known bool, dir WorktreeDir) string {
	if known {
		return task.ID
	}
	return dir.TaskID
}

// extraWorktreeNote names the task an extra worktree belongs to.
func extraWorktreeNote(task Task, known bool, dir WorktreeDir) string {
	if !known || task.ID == dir.TaskID {
		return ""
	}
	return "; it is an extra worktree of task " + task.ID
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

// unreadableHostHold is why nothing may rest on a native goblin being gone
// when its terminal's host record could not be read: that record is the only
// evidence of whether it still runs.
func unreadableHostHold(task Task, known bool) string {
	if known && task.HostUnreadable {
		return unreadableHostText
	}
	return ""
}

const unreadableHostText = "its native terminal's host record could not be read, so whether its goblin still runs is unknown"

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
		Detail: dir.Project + " answered git worktree list and does not list this path, so it is not one of that project's worktrees",
		Action: "remove the empty directory",
	}
	if pid, ok := processNaming(dir.Path, inv, supervised); ok {
		finding.PID = pid
		finding.Detail += fmt.Sprintf("; pid %d names it on its command line", pid)
		finding.refuseUnlessForced(fmt.Sprintf("pid %d is still using this directory, and that process is the leak here, not the directory it holds", pid), strconv.Itoa(pid))
	} else {
		finding.Detail += "; no command line names it, and a working directory is not readable from a process listing, so if the removal fails a leaked process is holding it open"
	}
	finding.refuseUnlessForced(unfinishedHold(task, known, unreadable), ownerKey(task, known, dir))
	finding.refuseUnlessForced(unreadableHostHold(task, known), ownerKey(task, known, dir))
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

func classifyMetas(inv Inventory, supervised map[int]bool) []Finding {
	worktrees := make(map[string]bool, len(inv.Worktrees))
	for _, worktree := range inv.Worktrees {
		worktrees[normalizePath(worktree.Path)] = true
	}
	var findings []Finding
	for _, task := range inv.Tasks {
		if task.IsPaused {
			continue
		}
		if task.Hosted {
			continue
		}
		if worktrees[normalizePath(task.Meta.Worktree)] {
			// The directory is still there, so the finding at that path owns
			// this one: the record reports on a later sweep, once the
			// directory is gone.
			continue
		}
		if taskHasProcess(task, inv.Processes, supervised) {
			continue
		}
		finding := Finding{
			Class:  OrphanMeta,
			TaskID: task.ID,
			Path:   task.Meta.Worktree,
			Detail: "no live terminal, no process, and no directory left on disk; its task " + taskOutcome(task, true, false),
			Action: "force-archive the task record",
		}
		if task.HostUnreadable {
			finding.Detail = unreadableHostText + "; no live terminal, no process, and no directory left on disk; its task " + taskOutcome(task, true, false)
		}
		if !task.Terminal {
			finding.refuseUnlessForced("task has not reached a terminal status (latest verb "+verbText(task.Verb)+")", finding.TaskID)
		}
		finding.refuseUnlessForced(unreadableHostHold(task, true), finding.TaskID)
		findings = append(findings, finding)
	}
	return findings
}

// goblinIsAlive reports whether anything that could say the goblin behind a
// worktree is still working says so. It deliberately does not consult the
// status log, because a goblin notifies done per pull request and a task can
// ship several under one id: a terminal verb establishes that a pull request
// finished, and nothing at all about whether the goblin is still at work.
//
// Every class that would take something a goblin is using asks this one
// question, so a signal added here reaches all of them: a killed dev server
// costs a goblin its round, and a returned worktree costs it more. The task's
// native terminal still running is the answer.
//
// The process table cannot answer this, which is the first place the next
// reader will look: no harness adapter puts the worktree on a command line,
// spawn carries it as the launch directory instead, and a working directory is
// not readable from a process listing. That is the same limit this package
// already names on an orphan_directory finding.
func goblinIsAlive(task Task, known bool) bool {
	return known && task.Hosted
}

// fixtureAncestry bounds the walk from a stand-in harness up to the fixture
// it runs under, with room for a shell and a shim between them.
const fixtureAncestry = 8

// fixtureOf finds the test fixture a process runs under: the native terminal
// host of another CFO home, or a program run from a goblin's Go temporary
// directory. A goblin's or a gate's test starts such a fixture and runs
// stand-in harnesses under it, and the fixture outlives the script that
// started it, so the stand-ins' ancestry reaches no terminal of this fleet. A
// scratch home's host carries its harness's command line after --, and a
// program run from a goblin's Go temporary directory may be the stand-in
// itself, so the process is checked as its own fixture origin too.
func fixtureOf(process Process, byPID map[int]Process, stateDir string, owns func(dir, task string) bool) (fixtureOrigin, bool) {
	if origin, ok := nativeFixture(process, stateDir, owns); ok {
		return origin, true
	}
	current := process
	for range fixtureAncestry {
		parent, ok := byPID[current.ParentPID]
		if !ok || parent.PID == current.PID || (!parent.Start.IsZero() && !current.Start.IsZero() && current.Start.Before(parent.Start)) {
			return fixtureOrigin{}, false
		}
		if origin, ok := nativeFixture(parent, stateDir, owns); ok {
			return origin, true
		}
		current = parent
	}
	return fixtureOrigin{}, false
}

// nativeFixture reports whether process is where a fixture runs its stand-ins
// from: the native terminal host of another CFO home, or a program run from a
// goblin's Go temporary directory and from that goblin's own worktree or
// scratch directory.
func nativeFixture(process Process, stateDir string, owns func(dir, task string) bool) (fixtureOrigin, bool) {
	if dirs, scratch := scratchHost(process, stateDir); scratch {
		return fixtureOrigin{Process: process, Dirs: append([]string{process.Cwd}, dirs...), ScratchHome: true}, true
	}
	if task, ok := goTestTask(process); ok && owns(process.Cwd, task) {
		return fixtureOrigin{Process: process, GoTestTask: task}, true
	}
	return fixtureOrigin{}, false
}

// runsForTask reports whether dir, a process's working directory, lies in
// task's own worktree, an extra worktree ownerOf gives it, or its scratch
// directory: its task temporary directory or its Claude Code scratchpad. A Go
// temporary directory alone names no owner: the shared no-mistakes daemon
// builds every goblin's gate tests under the directory of whichever goblin
// started it, and runs them from the gate's own worktree.
func runsForTask(dir, task string, inv Inventory, tasks map[string]Task, unreadable map[string]bool) bool {
	if inv.StateDir != "" && pathWithin(dir, filepath.Join(inv.StateDir, "tasktmp", task)) {
		return true
	}
	worktree, ok := worktreeHolding(scratchpadOwner(dir, inv.Worktrees), inv.Worktrees)
	if !ok {
		return false
	}
	if strings.EqualFold(worktree.TaskID, task) {
		return true
	}
	owner, known := ownerOf(worktree, tasks, inv.Worktrees, unreadable)
	return known && strings.EqualFold(owner.ID, task)
}

// fixtureOrigin is the process a test fixture's stand-ins run under: the
// native terminal host of another CFO home, or a program a goblin's Go test
// runs. Dirs are where it was started and, for a
// host, the state and terminal directories it names; any of them can tie it
// to the goblin whose test or proof it is. GoTestTask is the task whose Go
// temporary directory the program runs from and in whose worktree or scratch
// directory it runs, which ties it directly.
type fixtureOrigin struct {
	Process
	Dirs        []string
	ScratchHome bool
	GoTestTask  string
}

// goTestTask is the task whose Go temporary directory,
// %LOCALAPPDATA%\cfo\gotmp\<fleet>\<task id> (state.GoTmpDir), process's program
// runs from. A goblin's Go test builds its test binary there, and the
// stand-ins it starts run from the test's own temporary directory under it,
// so the path names the goblin the test may be; runsForTask decides whether
// it is.
func goTestTask(process Process) (string, bool) {
	args := commandArgs(process.CommandLine)
	if len(args) == 0 {
		return "", false
	}
	parts := strings.FieldsFunc(args[0], func(r rune) bool { return r == '\\' || r == '/' })
	for index := 1; index+3 < len(parts); index++ {
		if strings.EqualFold(parts[index-1], "cfo") && strings.EqualFold(parts[index], "gotmp") {
			return parts[index+2], true
		}
	}
	return "", false
}

// scratchHost reports whether process is a native terminal host run for a CFO
// home other than this one, and the --state and --dir it names. cfo host is
// only ever run with the home's state directory, so a host with any other is
// a scratch home some goblin's test or proof started.
func scratchHost(process Process, stateDir string) ([]string, bool) {
	args := commandArgs(process.CommandLine)
	if len(args) < 2 || !strings.EqualFold(args[1], "host") {
		return nil, false
	}
	var state, dir string
	for index := 2; index+1 < len(args) && args[index] != "--"; index++ {
		switch args[index] {
		case "--state":
			state = args[index+1]
		case "--dir":
			dir = args[index+1]
		}
	}
	if state == "" || normalizePath(filepath.Clean(state)) == normalizePath(filepath.Clean(stateDir)) {
		return nil, false
	}
	return []string{state, dir}, true
}

// commandArgs splits a Windows command line into its arguments, a quoted
// argument whole and without its quotes.
func commandArgs(commandLine string) []string {
	var args []string
	var current strings.Builder
	quoted, started := false, false
	for _, r := range commandLine {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case (r == ' ' || r == '\t') && !quoted:
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		args = append(args, current.String())
	}
	return args
}

// scratchpadOwner is the worktree a directory under a Claude Code session's
// temporary folder belongs to, since Claude Code names that folder
// %TEMP%\claude\<slug> after the working directory, each character in it that
// is not an ASCII letter or digit replaced by -; any other directory is its
// own answer.
func scratchpadOwner(dir string, worktrees []WorktreeDir) string {
	parts := strings.Split(normalizePath(dir), `\`)
	for index := 0; index+2 < len(parts); index++ {
		if parts[index] != "temp" || parts[index+1] != "claude" {
			continue
		}
		for _, worktree := range worktrees {
			slug := strings.Map(func(r rune) rune {
				if ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') {
					return r
				}
				return '-'
			}, normalizePath(worktree.Path))
			if parts[index+2] == slug {
				return worktree.Path
			}
		}
	}
	return dir
}

// fixtureInUse reports whether anything live still works where a test
// fixture was started. A fixture started in a task worktree (or extra
// worktree) is in use while that worktree's goblin lives, or while a gate
// agent works inside that same worktree. One started anywhere else is in use
// while a gate agent works at or above that directory, never because
// something live works below it: a fixture started from a project's main
// checkout is not kept by every goblin working in the project's worktrees.
// Once nothing qualifies, a stand-in under the fixture is an orphan: its
// goblin retired, or its gate run finished, and left it running. A directory
// that could not be read is in use by nothing anyone can show.
func fixtureInUse(dir string, inv Inventory, tasks map[string]Task, gates map[int]bool, unreadable map[string]bool) bool {
	if dir == "" {
		return false
	}
	if worktree, ok := worktreeHolding(dir, inv.Worktrees); ok {
		task, known := ownerOf(worktree, tasks, inv.Worktrees, unreadable)
		if goblinIsAlive(task, known) {
			return true
		}
		for _, process := range inv.Processes {
			if gates[process.PID] && isHarness(process) && pathWithin(process.Cwd, worktree.Path) {
				return true
			}
		}
		return false
	}
	for _, process := range inv.Processes {
		if gates[process.PID] && isHarness(process) && pathWithin(dir, process.Cwd) {
			return true
		}
	}
	return false
}

// worktreeHolding finds the worktree a directory lies in, the deepest one
// when worktrees nest.
func worktreeHolding(dir string, worktrees []WorktreeDir) (WorktreeDir, bool) {
	best := WorktreeDir{}
	found := false
	for _, worktree := range worktrees {
		if pathWithin(dir, worktree.Path) && (!found || len(worktree.Path) > len(best.Path)) {
			best, found = worktree, true
		}
	}
	return best, found
}

// pathWithin reports whether path is root or a directory under it, folded the
// same way every other path comparison here is.
func pathWithin(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	base := strings.TrimSuffix(normalizePath(root), `\`)
	folded := strings.TrimSuffix(normalizePath(path), `\`)
	return folded == base || strings.HasPrefix(folded, base+`\`)
}

// taskHasProcess reports whether anything still running belongs to the task. A
// meta is only an orphan when nothing at all is left, so a process this sweep
// has already flagged as an orphan still counts as a process: the process
// finding is the one to act on, not the record behind it.
func taskHasProcess(task Task, processes []Process, supervised map[int]bool) bool {
	if task.Meta.Worktree == "" {
		return false
	}
	for _, process := range processes {
		if !supervised[process.PID] {
			continue
		}
		if namesPath(process.CommandLine, task.Meta.Worktree) {
			return true
		}
	}
	return false
}

// supervisedPIDs is everything this sweep must never touch: each live native
// terminal's host, everything descended from them, and this process's own
// ancestry. The host set is the real answer to whether something is
// supervised; the self set is what keeps the sweep from reporting the session
// it runs inside.
func supervisedPIDs(inv Inventory, hosts map[string]int) map[int]bool {
	roots := make([]int, 0, len(hosts)+len(inv.SelfPIDs))
	for _, pid := range hosts {
		roots = append(roots, pid)
	}
	roots = append(roots, inv.SelfPIDs...)
	return descendants(inv.Processes, roots)
}

// liveHosts maps each native terminal whose host still runs to the host's
// pid. A record names its host by pid, and Windows reuses pids: a host that
// was stopped by pid leaves its record behind, so the pid counts only while a
// process holding it started no later than the host recorded itself.
func liveHosts(inv Inventory) map[string]int {
	started := make(map[int]time.Time, len(inv.Processes))
	for _, process := range inv.Processes {
		started[process.PID] = process.Start
	}
	hosts := make(map[string]int, len(inv.NativeHosts))
	for _, host := range inv.NativeHosts {
		start, running := started[host.HostPID]
		if running && !start.After(host.Started) {
			hosts[host.ID] = host.HostPID
		}
	}
	return hosts
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

// killNeedsItsOwnPID is why --apply alone never ends a process. Every other
// action this sweep takes is recoverable: an archived log is moved rather than
// deleted, a removed directory was proven empty, a returned worktree was proven
// to hold no unpushed work. Ending a process is none of those, and it costs a
// goblin the round it was in the middle of, so it is authorised by naming that
// process and never as a side effect of tidying something else.
const killNeedsItsOwnPID = "ending a process cannot be undone and can cost a goblin the round it is in, so a kill is authorised only by naming this process, never by a sweep acting on everything at once"

// unidentifiedHold is what a harness-shaped process gets when the sweep has
// run out of evidence. It says what could not be determined and stops there:
// the populations it cannot place are the Overlord's own sessions and tools,
// and every one of the fourteen findings that named --force here was a process
// that must never be killed.
const unidentifiedHold = "could not determine what this process belongs to: it runs under no terminal of this fleet, and it is neither the desktop application nor a gate agent; identify it before anything acts on it"

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

// isDesktopApp matches a packaged desktop application's own process by its
// install path; whatever it starts outside the package is found as its
// descendant.
func isDesktopApp(process Process) bool {
	command := normalizePath(process.CommandLine)
	return slices.ContainsFunc(desktopAppMarkers, func(marker string) bool { return strings.Contains(command, marker) })
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

// placementOutcome says what the sweep actually established about the goblin
// behind a worktree, which is not the same sentence in every case and must
// never be one the hold on the same line contradicts. Where no record names
// the worktree, there was no record to ask about, and claiming anything about
// its terminal restates the outcome a second time.
func placementOutcome(task Task, known, unreadable bool) string {
	switch {
	case known && task.HostUnreadable:
		return "a native terminal whose host record could not be read, so nothing establishes whether its goblin is working there, and its task " + taskOutcome(task, known, unreadable)
	case !known && !unreadable:
		return "no task record to ask about"
	}
	return "no live terminal of its goblin, and its task " + taskOutcome(task, known, unreadable)
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
	return verb == "done" || verb == "failed" || verb == "stopped"
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
