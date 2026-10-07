package reap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ProcessLister reads the operating system process table. It is an interface
// because it is the one source with no test double anywhere else in the tree:
// a classification test cannot conjure real processes.
type ProcessLister interface {
	List(ctx context.Context) ([]Process, error)
}

// PaneReader is the read-only slice of the Herdr client the sweep needs: the
// structural snapshot (which panes and agents exist) plus each pane's
// operating-system identity.
type PaneReader interface {
	Snapshot(ctx context.Context) (herdr.SessionSnapshot, error)
	PaneProcessInfo(ctx context.Context, target herdr.Target) (herdr.PaneProcessInfo, error)
}

// Collector assembles one Inventory from every source.
//
// The two sources that decide what is alive - the pane table and the process
// table - are hard requirements: with either missing, every live goblin reads
// as an orphan, so the sweep refuses rather than reporting a fleet it cannot
// see. Everything else degrades and says so in the returned notes.
type Collector struct {
	Home      home.Home
	Session   string
	Panes     PaneReader
	Processes ProcessLister
	// Commands runs git, which is the only source that can say whether a
	// directory among the worktrees is a worktree at all.
	Commands execx.Runner
	// StatusTail bounds how much of each status log is read to find the
	// latest verb, matching crewstate.Resolve's own window.
	StatusTail int
	// ProjectsRoot reads the folder that holds the operator's checkouts. It is
	// a function because reading it can cost a registry query, and the
	// watcher builds a Collector far more often than it runs a sweep.
	ProjectsRoot func() (string, error)
	// WorkingDirectory reads the directory a process runs in. It is what
	// traces a test fixture's stand-in harness back to the goblin or gate
	// that started it; nil leaves every stand-in unplaced.
	WorkingDirectory func(pid int) (string, error)
	// Environment reads the environment a process runs with. It is what
	// proves a process runs in a native terminal whose host is alive, by the
	// proof value that terminal's host gave it; nil leaves every process
	// unproven.
	Environment func(pid int) ([]string, error)
}

// Collect reads state, panes, processes and worktree directories once each.
func (c Collector) Collect(ctx context.Context) (Inventory, []string, error) {
	if c.Home.State == "" {
		return Inventory{}, nil, errors.New("reap: home state directory is required")
	}
	var notes []string
	inv := Inventory{SelfPIDs: selfAncestry(), Session: c.Session, StateDir: c.Home.State, ScratchRoot: c.Home.Scratch()}

	scan, err := state.ScanIDs(c.Home.State)
	if err != nil {
		return Inventory{}, nil, fmt.Errorf("reap: read state directory: %w", err)
	}
	inv.OrphanStatusIDs = scan.OrphanStatusIDs
	for _, id := range scan.MetaIDs {
		if state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(c.Home.State, id)
		if err != nil {
			notes = append(notes, fmt.Sprintf("state/%s.meta: UNREADABLE (%s)", id, err))
			// Named rather than dropped: a task that vanishes here reads as a
			// task that never existed, and a directory with no record behind
			// it carries no hold at all.
			inv.UnreadableTasks = append(inv.UnreadableTasks, id)
			continue
		}
		verb := c.latestVerb(id)
		task := Task{ID: id, Meta: meta, Verb: verb, Terminal: IsTerminal(verb)}
		lifecycle, lifecycleErr := state.ReadLifecycle(c.Home.State, id)
		if lifecycleErr != nil && !errors.Is(lifecycleErr, os.ErrNotExist) {
			notes = append(notes, fmt.Sprintf("lifecycle/%s.json: UNREADABLE (%s)", id, lifecycleErr))
			task.IsPaused = true
		} else if lifecycle.Generation == meta.SpawnGen && lifecycle.Phase != "running" && lifecycle.Phase != "stopped" {
			task.IsPaused = true
		}
		inv.Tasks = append(inv.Tasks, task)
	}

	inv.Worktrees = c.worktrees(ctx, inv.Tasks, &notes)

	var processes []Process
	listed := false
	if c.Panes != nil {
		panes, unresolved, unplaced, paneErr := c.readPanes(ctx)
		if paneErr != nil {
			// Every pane reads as gone when Herdr is unreadable, which would
			// classify the whole live fleet as orphaned. Refuse instead: a
			// sweep that cannot see panes has no business reporting orphans,
			// unless no Herdr server runs for this session, when no pane can
			// exist and a fleet of native terminals is swept on its own
			// evidence. Another session's server is a test fixture, and a
			// Herdr CLI call is no server.
			if c.Processes == nil {
				return Inventory{}, notes, fmt.Errorf("reap: read Herdr panes: %w", paneErr)
			}
			var err error
			if processes, err = c.Processes.List(ctx); err != nil {
				return Inventory{}, notes, fmt.Errorf("reap: read process table: %w", err)
			}
			listed = true
			if slices.ContainsFunc(processes, c.runsSessionServer) {
				return Inventory{}, notes, fmt.Errorf("reap: read Herdr panes: %w", paneErr)
			}
			notes = append(notes, "no Herdr server runs for session "+c.Session+", so there are no panes; native terminals and processes decide")
		}
		inv.Panes = panes
		inv.UnresolvedPanes = unresolved
		inv.UnplacedAgents = unplaced
		for _, pane := range unresolved {
			notes = append(notes, "pane "+pane+" could not report its process identity; process findings are held")
		}
		for _, pane := range unplaced {
			notes = append(notes, "the agent on pane "+pane+" reported no working directory, so it cannot be placed in a worktree; findings that rest on placing it are held")
		}
	} else {
		notes = append(notes, "no pane reader configured; pane evidence is missing")
	}

	if c.Processes != nil {
		if !listed {
			var err error
			if processes, err = c.Processes.List(ctx); err != nil {
				return Inventory{}, notes, fmt.Errorf("reap: read process table: %w", err)
			}
		}
		inv.Processes = processes
		inv.FleetRootPIDs = herdrRoots(processes)
		c.placeHarnesses(inv.Processes)
	} else {
		notes = append(notes, "no process lister configured; process evidence is missing")
	}
	c.nativeHosts(&inv, &notes)
	c.placeTerminals(&inv)

	return inv, notes, nil
}

// runsSessionServer reports whether process is the Herdr server of the
// collector's session.
func (c Collector) runsSessionServer(process Process) bool {
	return isHerdrServer(process) && strings.EqualFold(herdrSession(process.CommandLine), c.Session)
}

// nativeHosts reads every native terminal's host record. A record that cannot
// be read is named, and its task held: whether that goblin still runs is
// exactly what is unknown. A record gone since the listing is a host that has
// just ended, which leaves nothing to read.
func (c Collector) nativeHosts(inv *Inventory, notes *[]string) {
	ids, err := host.RecordIDs(c.Home.State)
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("state/hosts: UNREADABLE (%s); native goblins cannot be told alive, so their findings are held", err))
		for _, task := range inv.Tasks {
			if task.Meta.Backend == "native" {
				inv.UnreadableHosts = append(inv.UnreadableHosts, task.ID)
			}
		}
		return
	}
	for _, id := range ids {
		record, err := host.ReadRecord(c.Home.State, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("state/hosts/%s.json: UNREADABLE (%s)", id, err))
			inv.UnreadableHosts = append(inv.UnreadableHosts, id)
			continue
		}
		inv.NativeHosts = append(inv.NativeHosts, NativeHost{ID: record.ID, HostPID: record.HostPID, Started: record.Started, ProofSum: record.ProofSum})
	}
}

// placeTerminals proves, for every harness-shaped process and its ancestors,
// the native terminal of this home it runs in: the one its environment names,
// when the proof value beside that name is the one the terminal's host
// recorded. Every process in a terminal inherits both, through an exec that
// cuts its chain of parents short of the host too, as an MSYS one does.
func (c Collector) placeTerminals(inv *Inventory) {
	if c.Environment == nil {
		return
	}
	sums := make(map[string]string, len(inv.NativeHosts))
	for _, record := range inv.NativeHosts {
		sums[strings.ToLower(record.ID)] = record.ProofSum
	}
	index := make(map[int]int, len(inv.Processes))
	for i, process := range inv.Processes {
		index[process.PID] = i
	}
	read := make(map[int]bool)
	for _, process := range inv.Processes {
		if !isHarness(process) {
			continue
		}
		pid := process.PID
		for range fixtureAncestry + 1 {
			i, ok := index[pid]
			if !ok || read[pid] {
				break
			}
			read[pid] = true
			if env, err := c.Environment(pid); err == nil {
				inv.Processes[i].Terminal = provenTerminal(env, sums)
			}
			pid = inv.Processes[i].ParentPID
		}
	}
}

// provenTerminal is the terminal env names whose host's recorded proof sum,
// among sums, is that of the proof value env carries, or "" when there is
// none.
func provenTerminal(env []string, sums map[string]string) string {
	var id, proof string
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(name, host.IDVariable):
			id = value
		case strings.EqualFold(name, host.ProofVariable):
			proof = value
		}
	}
	sum, ok := sums[strings.ToLower(id)]
	if !ok || !(host.Record{ProofSum: sum}).Proves(proof) {
		return ""
	}
	return id
}

func (c Collector) latestVerb(id string) string {
	tail := c.StatusTail
	if tail <= 0 {
		tail = 200
	}
	lines, err := state.TailStatus(c.Home.State, id, tail)
	if err != nil {
		return ""
	}
	verb, ok := crewstate.LatestVerb(taskReported(lines))
	if !ok {
		return ""
	}
	return verb
}

// taskReported drops the reaper's own audit lines from a task's status log.
// The latest verb answers what the task last reported, and the reaper is not
// the task: its line parses as the verb "reaped", which ends nothing, so a
// task the sweep has acted on once would read as unfinished for the rest of
// its life and every later finding for it would be held behind --force. That
// is the two-sweep sequence this package relies on, where a directory is
// removed in one sweep and the record it leaves behind is archived in the next.
func taskReported(lines []string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		_, event := state.SplitStatus(line)
		if strings.HasPrefix(strings.TrimSpace(event), ReapedPrefix) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// readPanes pairs each pane in the structural snapshot with its
// operating-system identity and whether an agent is registered on it, and
// returns the panes whose identity could not be read. Such a pane still counts
// as a live pane, because it exists, which is what keeps its worktree off the
// list; but its shell pid is missing from the supervised set, so it is named
// separately and every process finding is held while any pane is unresolved.
//
// The third return is the panes whose agent reported no working directory.
// Herdr declares that field nullable, so one agent answering with nothing is a
// legitimate state rather than a fault, and it is neither an agent working
// nowhere nor a fleet-wide failure: it is one agent the sweep cannot place, so
// it is carried out and the classes that rest on placing it hold.
//
// That per-agent answer is why no protocol check stands here. A snapshot whose
// working directories all arrive empty leaves every agent unplaced and every
// destructive finding held, while the recoverable ones the operator asked for
// still get done. Refusing the whole sweep on a protocol bump would instead
// fail Collect, so the watcher records an error, fires no wake, and nothing at
// all is swept.
func (c Collector) readPanes(ctx context.Context) (panes []Pane, unresolved, unplaced []string, err error) {
	snapshot, err := c.Panes.Snapshot(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	agents := make(map[string]herdr.SnapshotAgent, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		agents[agent.PaneID] = agent
		if agent.Cwd == "" {
			unplaced = append(unplaced, agent.PaneID)
		}
	}
	panes = make([]Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		agent, hasAgent := agents[pane.ID]
		entry := Pane{ID: pane.ID, HasAgent: hasAgent, AgentCwd: agent.Cwd}
		info, err := c.Panes.PaneProcessInfo(ctx, herdr.Target{Session: c.Session, Pane: pane.ID})
		if err == nil {
			entry.ShellPID = info.ShellPID
			entry.ForegroundPID = info.ForegroundProcessGroupID
		} else {
			unresolved = append(unresolved, pane.ID)
		}
		panes = append(panes, entry)
	}
	return panes, unresolved, unplaced, nil
}

// worktrees enumerates every worktree the fleet could own: every folder under
// the home's worktrees folder, where spawn puts each one, and every
// .worktrees/ directory an older build used, the CFO home's own, each clone
// under projects/, and the project of every task that has a record. The last
// is what reaches a project cloned outside the home, which is most of them.
//
// Every checkout under the projects root is scanned as well, because a goblin
// worktree whose records were all archived sits in a project no task names any
// more.
//
// Outside the home's own root and clones, a folder under .worktrees/ counts
// only when this home's records name it (see claims): a project's checkout and
// the projects root are every home's on the machine, so another home's goblin
// worktrees and the operator's own sit there too, and this sweep must not so
// much as report one as an orphan.
func (c Collector) worktrees(ctx context.Context, tasks []Task, notes *[]string) []WorktreeDir {
	roots := map[string]bool{c.Home.Root: true}
	for _, entry := range readDirNames(filepath.Join(c.Home.Root, "projects")) {
		roots[filepath.Join(c.Home.Root, "projects", entry)] = true
	}
	for _, task := range tasks {
		if project := filepath.Clean(task.Meta.Project); task.Meta.Project != "" && !roots[project] {
			roots[project] = false
		}
	}
	held := heldIDs(c.Home.State, tasks)
	// A root's value is whether everything under its .worktrees/ is this
	// home's. A root already known keeps its value.
	if c.ProjectsRoot != nil {
		projectsRoot, err := c.ProjectsRoot()
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("projects root: UNREADABLE (%s); checkouts no task names were not scanned", err))
		}
		if projectsRoot != "" {
			projectsRoot = filepath.Clean(projectsRoot)
			entries, _ := os.ReadDir(projectsRoot)
			for _, entry := range entries {
				checkout := filepath.Join(projectsRoot, entry.Name())
				// Stat rather than the entry's own type, so a junction to a
				// checkout kept on another drive counts as the directory it
				// is, exactly as it does when a name resolves to it.
				if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
					continue
				}
				if !roots[checkout] {
					roots[checkout] = false
				}
			}
		}
	}

	seen := make(map[string]bool)
	found := c.homeWorktrees(ctx, tasks, seen, notes)
	for _, root := range sortedKeys(roots) {
		dir := filepath.Join(root, home.LegacyWorktreesDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				*notes = append(*notes, fmt.Sprintf("%s: UNREADABLE (%s)", dir, err))
			}
			continue
		}
		// Read once per root, and only for a root that has something under
		// .worktrees/ to classify: the answer costs a subprocess, and most
		// roots have nothing there at all.
		var registered []os.FileInfo
		var answered, asked bool
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if !entry.IsDir() || (!roots[root] && !claims(root, path, tasks, held)) {
				continue
			}
			if seen[normalizePath(path)] {
				continue
			}
			seen[normalizePath(path)] = true
			if !asked {
				registered, answered = c.registeredWorktrees(ctx, root, notes)
				asked = true
			}
			found = append(found, WorktreeDir{
				Path:         path,
				Project:      root,
				TaskID:       strings.TrimPrefix(entry.Name(), home.LegacyWorktreePrefix),
				Registration: registrationOf(path, registered, answered),
				Created:      createdAt(path),
			})
		}
	}
	return found
}

// claims reports whether this home's records name path, a folder under the
// .worktrees/ of checkout root: a live task's worktree or extra one, a goblin
// worktree named for a task this home holds any record of, or one named as an
// extra of a live task in that checkout, <id>-<suffix>, as ownerOf reads it.
func claims(root, path string, tasks []Task, held map[string]bool) bool {
	key := normalizePath(filepath.Clean(path))
	for _, task := range tasks {
		for _, recorded := range append([]string{task.Meta.Worktree}, task.Meta.Extras...) {
			if recorded != "" && normalizePath(filepath.Clean(recorded)) == key {
				return true
			}
		}
	}
	name, ok := strings.CutPrefix(strings.ToLower(filepath.Base(path)), home.LegacyWorktreePrefix)
	if !ok || name == "" {
		return false
	}
	if held[name] {
		return true
	}
	for _, task := range tasks {
		if strings.HasPrefix(name, strings.ToLower(task.ID)+"-") && normalizePath(filepath.Clean(task.Meta.Project)) == normalizePath(filepath.Clean(root)) {
			return true
		}
	}
	return false
}

// heldIDs are the ids, lowercased as Windows compares them, of every task this
// home holds a record of: a task record, readable or not, a status log, an
// outcome, or a record cleanup or the sweep archived, <id>.<stamp> or
// <id>.status.<stamp>.
func heldIDs(stateDir string, tasks []Task) map[string]bool {
	held := map[string]bool{}
	for _, task := range tasks {
		held[strings.ToLower(task.ID)] = true
	}
	entries, _ := os.ReadDir(stateDir)
	for _, entry := range entries {
		name := entry.Name()
		if id, ok := strings.CutSuffix(name, ".meta"); ok && !entry.IsDir() {
			held[strings.ToLower(id)] = true
		} else if id, ok := strings.CutSuffix(name, ".status"); ok && !entry.IsDir() {
			held[strings.ToLower(id)] = true
		}
	}
	outcomes, _ := os.ReadDir(filepath.Join(stateDir, "outcomes"))
	for _, entry := range outcomes {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok {
			held[strings.ToLower(id)] = true
		}
	}
	archived, _ := os.ReadDir(filepath.Join(stateDir, state.ArchiveDirName))
	for _, entry := range archived {
		id := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), ".status")
		held[strings.ToLower(id)] = true
	}
	return held
}

// homeWorktrees lists every folder under the home's worktrees folder,
// <project folder>\<name>, each placed with the checkout it belongs to: the
// project its task's record names, or else the repository its .git file
// leads to. A folder whose checkout cannot be found is listed with its
// registration unknown, the gated class.
func (c Collector) homeWorktrees(ctx context.Context, tasks []Task, seen map[string]bool, notes *[]string) []WorktreeDir {
	recorded := map[string]string{}
	for _, task := range tasks {
		for _, path := range append([]string{task.Meta.Worktree}, task.Meta.Extras...) {
			if path != "" && task.Meta.Project != "" {
				recorded[normalizePath(filepath.Clean(path))] = filepath.Clean(task.Meta.Project)
			}
		}
	}
	registered := map[string][]os.FileInfo{}
	answered := map[string]bool{}
	var found []WorktreeDir
	root := c.Home.Worktrees()
	for _, folder := range readDirNames(root) {
		for _, name := range readDirNames(filepath.Join(root, folder)) {
			path := filepath.Join(root, folder, name)
			if info, err := os.Stat(path); err != nil || !info.IsDir() || seen[normalizePath(path)] {
				continue
			}
			seen[normalizePath(path)] = true
			project, ok := recorded[normalizePath(path)]
			if !ok {
				project, ok = checkoutOf(path)
			}
			dir := WorktreeDir{Path: path, Project: project, TaskID: name, Created: createdAt(path)}
			if ok {
				if _, asked := answered[project]; !asked {
					registered[project], answered[project] = c.registeredWorktrees(ctx, project, notes)
				}
				dir.Registration = registrationOf(path, registered[project], answered[project])
			} else {
				*notes = append(*notes, fmt.Sprintf("%s: no task record names it and its .git file leads to no checkout; it cannot be confirmed to be a worktree", path))
			}
			found = append(found, dir)
		}
	}
	return found
}

// checkoutOf is the checkout a worktree belongs to, read from the gitdir its
// .git file names: <checkout>\.git\worktrees\<name>, whose commondir leads
// back to <checkout>\.git.
func checkoutOf(worktree string) (string, bool) {
	content, err := fsx.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return "", false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = filepath.Clean(filepath.FromSlash(strings.TrimSpace(gitDir)))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	common, err := fsx.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return "", false
	}
	commonDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(common))))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	if !strings.EqualFold(filepath.Base(commonDir), ".git") {
		return "", false
	}
	return filepath.Dir(commonDir), true
}

// registeredWorktrees reads the directories a project registers as worktrees,
// as their on-disk identities rather than as the paths git spelled them. It is
// the premise the whole worktree classification rests on, and it is checked
// because it stops holding: a task that dies leaves its directory behind, the
// repository drops it from the list, and the directory still looks exactly
// like a worktree. Every git question asked from inside such a shell is
// answered by the enclosing repository, which is how an empty directory came
// to be reported as carrying that repository's uncommitted changes.
//
// A root that is not a repository at all is refused rather than read. Git
// there answers with the enclosing repository's list, which says nothing about
// a path under this root, so every directory beneath it would read as
// unregistered, which is the removable class, on the word of a repository
// nobody asked about it.
//
// The second return says whether the repository answered at all. Every failure
// path leaves it false, which is what keeps "could not be asked" from reading
// as "answered, and does not list this".
func (c Collector) registeredWorktrees(ctx context.Context, root string, notes *[]string) ([]os.FileInfo, bool) {
	if c.Commands == nil {
		*notes = append(*notes, "no command runner configured; no directory among the worktrees can be confirmed to be a worktree")
		return nil, false
	}
	// The query asks a directory what it registers, which is only a question
	// this root can answer when it is its own repository. Run inside a plain
	// directory nested in a repository, git answers for that repository
	// instead, and an absent path would then read as unregistered, which is
	// the removable class, on the word of a repository that was never asked
	// about it. This scan removes directories, so it establishes whose answer
	// it is getting before it trusts one.
	itsOwn, err := answersForItself(ctx, c.Commands, root)
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: whose repository git there answers for could not be established (%s); its directories cannot be confirmed to be worktrees", root, err))
		return nil, false
	}
	if !itsOwn {
		*notes = append(*notes, fmt.Sprintf("%s: not its own repository, so git there answers for an enclosing one; its directories cannot be confirmed to be worktrees", root))
		return nil, false
	}
	result, err := c.Commands.Run(ctx, execx.Request{Dir: root, Name: "git", Args: []string{"worktree", "list", "--porcelain"}})
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: git worktree list failed (%s); its directories cannot be confirmed to be worktrees", root, err))
		return nil, false
	}
	if result.ExitCode != 0 {
		*notes = append(*notes, fmt.Sprintf("%s: git worktree list exited with code %d (%s); its directories cannot be confirmed to be worktrees", root, result.ExitCode, strings.TrimSpace(string(result.Stderr))))
		return nil, false
	}
	var registered []os.FileInfo
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		info, err := os.Stat(filepath.Clean(path))
		if errors.Is(err, os.ErrNotExist) {
			// A path git still lists but that is gone from disk is a worktree
			// awaiting a prune. It cannot be any directory this scan found, so
			// dropping it leaves the evidence about the others complete.
			continue
		}
		if err != nil {
			// Any other failure means this entry could not be compared at all,
			// so the evidence set is incomplete, and an incomplete set cannot
			// prove a directory is absent from it. Reporting that as "not
			// listed" would be the same failure this package exists to remove:
			// a premise that stopped holding, resolved to the permissive answer.
			*notes = append(*notes, fmt.Sprintf("%s: %s is listed as a worktree but could not be read (%s); directories under this root cannot be confirmed", root, path, err))
			return nil, false
		}
		registered = append(registered, info)
	}
	return registered, true
}

// answersForItself reports whether git run in dir answers for dir itself,
// which is the premise under every git question this package asks: a project
// root that registers its own worktrees, and a worktree whose status and
// commits are its own. The comparison is by directory identity rather than by
// spelling, for the same reason the registration comparison is: an operator
// keeps a checkout behind a junction and git prints the target while the scan
// holds the junction's own name.
//
// A question git could not answer comes back as an error rather than as an
// answer, because "git here speaks for somewhere else" and "git could not be
// asked" are different facts and only the first one establishes anything.
func answersForItself(ctx context.Context, commands execx.Runner, dir string) (bool, error) {
	result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("git rev-parse --show-toplevel exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	top := strings.TrimSpace(string(result.Stdout))
	if top == "" {
		return false, errors.New("git rev-parse --show-toplevel answered with nothing")
	}
	topInfo, err := os.Stat(filepath.Clean(top))
	if err != nil {
		return false, err
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	return os.SameFile(topInfo, dirInfo), nil
}

// registrationOf places a directory against what the repository answered. The
// comparison is by directory identity rather than by spelling: an operator
// keeps a checkout on another drive behind a junction, git prints the
// junction's target while the scan joins the junction's own name, and
// filepath.EvalSymlinks does not see through a Windows junction. os.Stat does,
// and os.SameFile then answers the only question that matters, which is
// whether this is the same directory on disk.
func registrationOf(path string, registered []os.FileInfo, answered bool) Registration {
	if !answered {
		return RegistrationUnknown
	}
	info, err := os.Stat(path)
	if err != nil {
		// Nothing was established about this directory, so it takes the gated
		// class. Answering "unlisted" here would put a directory the sweep
		// could not even read into the removable one.
		return RegistrationUnknown
	}
	for _, entry := range registered {
		if os.SameFile(info, entry) {
			return RegistrationListed
		}
	}
	return RegistrationUnlisted
}

// herdrRoots finds the Herdr server processes. Every pane shell CFO ever
// created descends from one, which is what makes an unsupervised harness
// attributable to the fleet rather than to the operator's own editor.
func herdrRoots(processes []Process) []int {
	var roots []int
	for _, process := range processes {
		if executableName(process.Name) == "herdr" {
			roots = append(roots, process.PID)
		}
	}
	return roots
}

// placeHarnesses reads the working directory of every harness-shaped process
// and of each of its ancestors, which is where a test fixture's stand-ins are
// traced back to the goblin or gate that started them. Reading one opens the
// process, and nothing else here needs it, so nothing else is read.
func (c Collector) placeHarnesses(processes []Process) {
	if c.WorkingDirectory == nil {
		return
	}
	index := make(map[int]int, len(processes))
	for i, process := range processes {
		index[process.PID] = i
	}
	read := make(map[int]bool)
	for _, process := range processes {
		if !isHarness(process) {
			continue
		}
		pid := process.PID
		for range fixtureAncestry + 1 {
			i, ok := index[pid]
			if !ok || read[pid] {
				break
			}
			read[pid] = true
			if dir, err := c.WorkingDirectory(pid); err == nil {
				processes[i].Cwd = dir
			}
			pid = processes[i].ParentPID
		}
	}
}

// WorktreeOwner reads the task that owns the worktree at path, by the rule
// ownerOf states, from the records in stateDir. A path that is in no fleet
// worktree, in the home's worktreesRoot or where an older build put one, is
// owned only by a record that names it.
func WorktreeOwner(stateDir, worktreesRoot, project, path string) (Task, bool, error) {
	scan, err := state.ScanIDs(stateDir)
	if err != nil {
		return Task{}, false, err
	}
	tasks := make(map[string]Task, len(scan.MetaIDs))
	unreadable := make(map[string]bool)
	worktrees := make([]WorktreeDir, 0, len(scan.MetaIDs))
	for _, id := range scan.MetaIDs {
		if state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(stateDir, id)
		if err != nil {
			unreadable[id] = true
			continue
		}
		tasks[id] = Task{ID: id, Meta: meta}
		worktrees = append(worktrees, WorktreeDir{Path: meta.Worktree, Created: createdAt(meta.Worktree)})
	}
	dir := WorktreeDir{Path: path, Project: project, Created: createdAt(path)}
	if place, ok := home.LocateWorktree(worktreesRoot, path); ok {
		dir.TaskID = place.Name
	}
	task, known := ownerOf(dir, tasks, worktrees, unreadable)
	return task, known, nil
}

// createdAt is when a directory was made, zero when that cannot be read.
func createdAt(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, data.CreationTime.Nanoseconds()).UTC()
}

// selfAncestry is this process and its parents. The sweep runs inside the
// CFO's own Claude session (directly, or hosted by the watcher), so its own
// harness would otherwise look exactly like an orphan.
func selfAncestry() []int {
	entries, err := proc.Ancestry(proc.Self(), 12)
	if err != nil {
		return []int{proc.Self()}
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pids = append(pids, entry.PID)
	}
	return pids
}

func readDirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CIMProcesses reads the Windows process table through one CIM query. The
// Toolhelp32 snapshot internal/proc already takes carries no command line, and
// the command line is what tells a goblin's dev server apart from the
// operator's, so this pays one subprocess per sweep to get it.
type CIMProcesses struct {
	Commands execx.Runner
}

// utf8OutputPrelude forces the child's console output encoding to UTF-8.
// Go spawns powershell with a pipe rather than a console, and PowerShell then
// encodes stdout with the OEM code page - IBM437 on this machine. A command
// line holding any character outside that page comes back mangled, and some
// mangle into raw control bytes: U+00A7 SECTION SIGN encodes to byte 0x15,
// which encoding/json rejects with "invalid character '\x15' in string
// literal", failing the entire process listing and with it the whole orphan
// sweep. Escaping the byte instead would decode, but would report a command
// line that is not the process's real one, and reap classifies processes by
// their command line to decide what may be killed.
const utf8OutputPrelude = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; `

const cimProcessScript = utf8OutputPrelude + `Get-CimInstance Win32_Process | ForEach-Object { [pscustomobject]@{ pid = [int]$_.ProcessId; ppid = [int]$_.ParentProcessId; name = $_.Name; cmd = $_.CommandLine; start = $(if ($_.CreationDate) { $_.CreationDate.ToUniversalTime().ToString('o') } else { '' }) } } | ConvertTo-Json -Compress -Depth 3`

// List runs the CIM query and decodes it. ConvertTo-Json emits a bare object
// rather than an array when the pipeline yields exactly one item, so a single
// row is wrapped before decoding; a real process table never has one row, but
// a wrapper that only works on the common case is not worth the bug report.
func (c CIMProcesses) List(ctx context.Context) ([]Process, error) {
	if c.Commands == nil {
		return nil, errors.New("reap: command runner is required")
	}
	result, err := c.Commands.Run(ctx, execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", cimProcessScript},
	})
	if err != nil {
		return nil, fmt.Errorf("reap: list processes: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("reap: list processes exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return decodeProcesses(result.Stdout)
}

func decodeProcesses(stdout []byte) ([]Process, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, errors.New("reap: process listing returned no output")
	}
	if strings.HasPrefix(trimmed, "{") {
		trimmed = "[" + trimmed + "]"
	}
	var rows []struct {
		PID   int    `json:"pid"`
		PPID  int    `json:"ppid"`
		Name  string `json:"name"`
		Cmd   string `json:"cmd"`
		Start string `json:"start"`
	}
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, fmt.Errorf("reap: decode process listing: %w", err)
	}
	processes := make([]Process, 0, len(rows))
	for _, row := range rows {
		process := Process{PID: row.PID, ParentPID: row.PPID, Name: row.Name, CommandLine: row.Cmd}
		if row.Start != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, row.Start); err == nil {
				process.Start = parsed.UTC()
			}
		}
		processes = append(processes, process)
	}
	return processes, nil
}
