package reap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// InventorySource supplies one sweep's cross-referenced evidence.
type InventorySource interface {
	Collect(ctx context.Context) (Inventory, []string, error)
}

// StatusID is the fleet-level status log every reap writes to: state/.reap.status.
// The leading dot keeps it out of state.ScanIDs, so the reaper's own log can
// never be reported as an orphan status log by the next sweep.
const StatusID = ".reap"

// DefaultCPUSample is how long a process is watched before it is called idle,
// and DefaultCPUIdle is the processor time it may consume in that window and
// still count as idle.
//
// Log age is not evidence. A goblin waiting on an API response writes nothing
// for minutes and is very much alive; a goblin mid-turn burns processor time
// continuously. Three seconds is long enough to separate the two and short
// enough that an operator waits through it for a whole fleet, and 100ms of
// processor time in those three seconds is roughly 3% of one core: above the
// idle noise a UI thread makes, far below anything doing work.
const (
	DefaultCPUSample = 3 * time.Second
	DefaultCPUIdle   = 100 * time.Millisecond
)

// Options are the operator's choices for one sweep.
type Options struct {
	// Apply acts on the findings. Without it the sweep only reports, which is
	// the default because killing a process is irreversible.
	Apply bool
	// Force names the specific things the operator accepts responsibility for:
	// a decimal pid, or a task id. It is deliberately never a blanket
	// override, so "force" always names what it forces.
	Force map[string]bool
	// ProbeIdle samples processor time and reports it beside each process
	// finding, so the operator reads whether it is doing work before deciding
	// which pid to name. It costs CPUSample of wall clock per process
	// finding, so the watcher's background audit leaves it off and simply
	// reports what it found; every cfo reap invocation turns it on.
	ProbeIdle bool
	// CPUSample and CPUIdle override the idleness measurement's window and
	// threshold.
	CPUSample time.Duration
	CPUIdle   time.Duration
}

// forcedPID reports whether the operator named this finding's process. It is
// the only thing that authorises ending it: a task id says a task is over and
// can never speak for a process still running under it.
func (o Options) forcedPID(finding Finding) bool {
	return finding.PID != 0 && o.Force[strconv.Itoa(finding.PID)]
}

// Result is one sweep: what was found, what was done about it, and anything
// the sweep could not read.
type Result struct {
	Findings []Finding `json:"findings"`
	Applied  []string  `json:"applied,omitempty"`
	Notes    []string  `json:"notes,omitempty"`
}

// Service runs the sweep. The action seams are plain functions rather than
// interfaces because each has exactly one production implementation, wired in
// cmd/cfo, and exactly one fake per test.
type Service struct {
	Home home.Home
	// Inventory is the evidence source. It is an interface so a gate test can
	// hand the service a synthetic fleet; Collector is the one production
	// implementation.
	Inventory InventorySource
	Commands  execx.Runner
	// CPU reads a process's total consumed processor time. A false second
	// return means the process could not be measured, which is reported as
	// "cannot prove it is idle" rather than as idleness.
	CPU func(pid int) (time.Duration, bool)
	// Kill ends a process and its children.
	Kill func(ctx context.Context, pid int) error
	// Clean returns a task's worktree through the existing cfo cleanup path.
	Clean func(ctx context.Context, id string, forceArchive bool) error
	// Return releases a worktree directory that has no task record behind it,
	// through the same primitive cleanup uses.
	Return func(ctx context.Context, project, worktree string) error
	Sleep  func(time.Duration)
	Now    func() time.Time
}

// Audit collects the inventory, classifies it, and applies every safety gate,
// without acting on anything. It is what --dry-run prints and what the
// watcher persists for the session-start digest.
func (s Service) Audit(ctx context.Context, options Options) (Result, error) {
	inv, notes, err := s.Inventory.Collect(ctx)
	if err != nil {
		return Result{Notes: notes}, err
	}
	findings := Classify(inv)
	for i := range findings {
		s.gate(ctx, &findings[i], options)
	}
	return Result{Findings: findings, Notes: notes}, nil
}

// Apply runs the audit and then acts on every finding the gates cleared.
// A finding that is held is left exactly as found and reported with its
// reason; a finding that fails while being acted on becomes a held finding
// carrying the failure, so nothing is silently dropped.
func (s Service) Apply(ctx context.Context, options Options) (Result, error) {
	options.Apply = true
	options.ProbeIdle = true
	result, err := s.Audit(ctx, options)
	if err != nil {
		return result, err
	}
	// Which tasks had a record is read once, before anything is acted on,
	// because the question is about the fleet the sweep classified and not
	// about whatever is left by the time each finding's turn comes. Two
	// findings can name one task, the action for a worktree and for a meta is
	// a cleanup that removes the record, and findings are acted on in class
	// order, so asking per finding still loses the audit line for whichever
	// one sorts second.
	hadRecord := make(map[string]bool, len(result.Findings))
	for _, finding := range result.Findings {
		if finding.TaskID != "" {
			hadRecord[finding.TaskID] = s.hasRecord(finding.TaskID)
		}
	}
	for i := range result.Findings {
		finding := &result.Findings[i]
		if finding.Held() {
			continue
		}
		if err := s.act(ctx, *finding); err != nil {
			finding.refuseAbsolutely("action failed: " + err.Error())
			continue
		}
		line := ReapedPrefix + finding.Line()
		result.Applied = append(result.Applied, line)
		s.record(*finding, line, hadRecord[finding.TaskID])
	}
	return result, nil
}

// ReapedPrefix marks a line the reaper wrote about its own action, as opposed
// to a line the task reported about itself. It is a constant because it is
// written here and read back in Collector.latestVerb: a status line's verb is
// its first word before the colon, so an unmarked "reaped: ..." line makes
// "reaped" the task's latest verb, and "reaped" ends nothing. A record the
// sweep touched once would then read as unfinished forever, and every later
// finding for it would sit behind --force.
const ReapedPrefix = "reaped: "

// record writes one line per reaped resource: into the task's own status log
// when the finding names a task, and always into the fleet-level reap log, so
// there is one place that answers "what did the reaper do".
func (s Service) record(finding Finding, line string, hadRecord bool) {
	// The reaper may add to the history of a task that exists and must never
	// manufacture one for a task that does not. state.AppendStatus creates
	// the log it is handed, so a line written for a record retired long ago
	// would leave behind a status log holding nothing but the reaper's own
	// line, which state.ScanIDs reports as an orphan the sweep itself made.
	// The record is the fact to ask about, not the log: a goblin spawned a
	// moment ago has a record and has reported nothing yet. The fleet log
	// below carries every line either way.
	if hadRecord {
		// Best effort: a failed status write must not undo a completed kill.
		_ = state.AppendStatus(s.Home.State, finding.TaskID, state.NormalizeStatusDetail(line))
	}
	_ = state.AppendStatus(s.Home.State, StatusID, state.NormalizeStatusDetail(line))
}

// hasRecord reports whether the task behind a finding still has a state
// record. It is asked while the record can still answer.
func (s Service) hasRecord(id string) bool {
	if id == "" || state.ValidTaskID(id) != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(s.Home.State, id+".meta"))
	return err == nil
}

// gate is where the correctness lives. Every class gets the checks that make
// its action safe, and a refusal is recorded on the finding rather than
// dropping it: the operator must be able to see what was found AND why it was
// left alone.
func (s Service) gate(ctx context.Context, finding *Finding, options Options) {
	// The operator's --force drops the refusals it actually answers and leaves
	// every other one standing.
	finding.clearForced(options.Force)
	// The measurement is reported rather than used to refuse: naming a pid has
	// always meant killing that process even if it is busy. It is taken on
	// every sweep that asks for one, held or not and authorised or not,
	// because the operator reads a held process finding to decide whether to
	// name its pid, and whether it is burning processor time is the one fact
	// that separates a live process from an abandoned one. A number produced
	// only on the run that kills arrives after the decision it informs.
	if options.ProbeIdle && (finding.Class == OrphanProcess || finding.Class == StaleServer) {
		if busy := s.measureBusy(finding.PID, options); busy != "" {
			finding.Detail += "; " + busy
		}
	}
	// A finding still refused takes no further gate, because none of them can
	// change the outcome: three git subprocesses per worktree, spent to reach
	// a verdict already reached. This is not the mistake the refusal machinery
	// exists to prevent. Dropping a refusal during classification is a lie,
	// because classification is pure and cheap and its whole job is to state
	// every reason something is held; skipping a question here adds no refusal
	// and replaces none, so nothing is lost but the wait.
	if finding.Held() {
		return
	}
	switch finding.Class {
	case OrphanProcess, StaleServer:
		if !options.forcedPID(*finding) {
			// Ending a process is the one action here that cannot be undone
			// and that costs somebody else their work, so it answers to the
			// operator naming that process and to nothing else. A sweep run
			// to tidy a status log acts on every finding it is not holding,
			// and on 19 September 2026 that killed a goblin's dev server
			// mid-suite: one flag covering an archive and a kill invites
			// exactly that reading.
			finding.refuseUnlessForced(killNeedsItsOwnPID, strconv.Itoa(finding.PID))
			return
		}
	case OrphanWorktree:
		// Its work-preservation refusals are absolute and stand even for a
		// forced task: --force covers the operator's judgement about a task's
		// status, never a decision to destroy work.
		s.holdIfWorkWouldBeLost(ctx, finding)
	case OrphanDirectory:
		// Removing the shell is only ever a removal of an empty directory.
		// Anything with contents is somebody's files in a directory this
		// sweep has already failed to explain, so it is reported, not touched.
		finding.refuseAbsolutely(holdIfNotEmpty(finding.Path))
	}
	// A gate records its own refusals after the clear above, so the force is
	// applied once more: a refusal the operator has already answered may not
	// stand merely because the gate that found it ran second. The absolute
	// ones are untouched, which is what makes them absolute.
	finding.clearForced(options.Force)
}

// measureBusy samples processor time twice and says what it found when the
// process moved, or when it could not be read at all: "I could not tell" is
// not "it is idle". The answer is reported beside the finding, because log age
// is not evidence and a goblin waiting on an API response writes nothing for
// minutes while very much alive.
func (s Service) measureBusy(pid int, options Options) string {
	if s.CPU == nil {
		return "no processor-time sampler configured, so idleness cannot be proven"
	}
	before, ok := s.CPU(pid)
	if !ok {
		return fmt.Sprintf("processor time for pid %d could not be read, so idleness cannot be proven", pid)
	}
	window := sampleWindow(options)
	s.sleep(window)
	after, ok := s.CPU(pid)
	if !ok {
		return fmt.Sprintf("processor time for pid %d could not be read after the sample, so idleness cannot be proven", pid)
	}
	delta := after - before
	if delta > idleThreshold(options) {
		return fmt.Sprintf("busy: burned %s of processor time in %s, which is a process doing work, not an idle one", delta.Round(time.Millisecond), window)
	}
	// An idle reading is still a reading, and it is the one the operator acts
	// on: this is the line they decide from when naming a pid. Reporting it as
	// nothing would make a measured idle process indistinguishable from one
	// nobody measured, which is the difference this whole sweep turns on.
	return fmt.Sprintf("idle: burned %s of processor time in %s", delta.Round(time.Millisecond), window)
}

// sweepAgain closes every work-gate refusal where the sweep could not look.
// Such a refusal is still absolute, because unreadable is not clean and the
// worktree may hold the only copy of a goblin's work, but the operator is owed
// the remedy that does exist: fix the read and sweep again.
const sweepAgain = ", then sweep again, because a worktree whose state cannot be read may hold the only copy of a goblin's work"

// holdIfWorkWouldBeLost refuses any worktree with uncommitted changes or with
// commits that exist nowhere else. A goblin's unpushed branch is the entire
// product of its run, and no --force clears that: the point of the sweep is to
// free resources, never to decide that somebody's work did not matter. A
// question it could not ask refuses too, and absolutely, because unprovable
// stays unremovable; it says it could not look rather than claiming it found
// work. Only the premise refusal is the operator's to answer, because an empty
// directory holds no work to preserve.
func (s Service) holdIfWorkWouldBeLost(ctx context.Context, finding *Finding) {
	worktree := finding.Path
	if worktree == "" {
		return
	}
	if s.Commands == nil {
		finding.refuseAbsolutely("no command runner is configured, so the sweep could not ask git anything here and this worktree's state is unknown rather than proven clean; configure one" + sweepAgain)
		return
	}
	// Every git question below is answered by the nearest enclosing repository
	// when this path is not a worktree of its own, so the premise is asserted
	// once, here, rather than checked again inside each answer. Emptiness only
	// ruled out one shape of not being a worktree; a populated folder that is
	// not one got the enclosing repository's dirt reported as its own, which
	// is the defect this whole branch exists to remove.
	itsOwn, err := answersForItself(ctx, s.Commands, worktree)
	if err != nil {
		finding.refuseAbsolutely("the sweep could not establish whether git at this path answers for this path or for an enclosing repository, so nothing git reports here can be attributed to this worktree: " + err.Error() + "; resolve that" + sweepAgain)
		return
	}
	if !itsOwn {
		holdNotAWorktreeOfItsOwn(finding)
		return
	}

	status, err := s.git(ctx, worktree, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		finding.refuseAbsolutely("the sweep could not read git status here, so whether this worktree holds uncommitted work is unknown rather than answered: " + err.Error() + "; resolve that" + sweepAgain)
		return
	}
	if status != "" {
		finding.refuseAbsolutely("worktree has uncommitted or untracked changes")
		return
	}
	unpushed, err := s.git(ctx, worktree, "log", "--oneline", "HEAD", "--not", "--remotes")
	if err != nil {
		finding.refuseAbsolutely("the sweep could not list this worktree's unpushed commits, so whether it holds the only copy of any of them is unknown: " + err.Error() + "; resolve that" + sweepAgain)
		return
	}
	if unpushed != "" {
		count := len(strings.Split(unpushed, "\n"))
		finding.refuseAbsolutely(fmt.Sprintf("worktree has %d commit(s) on no remote; pushing them is the only way this is safe to remove", count))
	}
}

// holdNotAWorktreeOfItsOwn refuses a path git does not answer for, and what
// the path holds decides which refusal it gets. Nothing at all holds no work,
// so that one is the operator's to answer by naming the task, and what the
// force buys is the removal of the directory and the prune of the
// registration behind it where one was established, never the cleanup the
// class was named for. The reason there deliberately names no cause, because
// the same emptiness arrives from a registration that could not be read and
// from a listed worktree whose files are gone, and a remedy right for one is
// wrong for the other. Files are the absolute case: they belong to something
// the sweep cannot attribute to any repository, and unattributable stays
// unremovable.
func holdNotAWorktreeOfItsOwn(finding *Finding) {
	empty, err := isEmptyDir(finding.Path)
	if err != nil {
		finding.refuseAbsolutely("git at this path answers for a different repository, and the sweep could not read the directory to establish what removing it would destroy: " + err.Error() + "; resolve that" + sweepAgain)
		return
	}
	if !empty {
		finding.refuseAbsolutely("git at this path answers for a different repository, so nothing it reports about uncommitted work or unpushed commits can be attributed here, and the files this directory does hold belong to something the sweep cannot identify; identify them before anything removes this")
		return
	}
	finding.Action = "remove the empty directory"
	if finding.Registered {
		finding.Action += " and prune its registration from the project"
	}
	finding.refuseUntilEstablished("this directory holds no files, so no git answer about it belongs to it rather than to an enclosing repository, and nothing about what it is can be established from here; establish what it is before it is removed", finding.TaskID)
}

func (s Service) act(ctx context.Context, finding Finding) error {
	switch finding.Class {
	case OrphanProcess, StaleServer:
		if s.Kill == nil {
			return errors.New("no process killer configured")
		}
		return s.Kill(ctx, finding.PID)
	case OrphanWorktree:
		return s.returnWorktree(ctx, finding)
	case OrphanDirectory:
		// Non-recursive on purpose: it removes the empty shell and fails on
		// anything else, including a directory a process still holds open,
		// which is the failure that names the real leak.
		return os.Remove(finding.Path)
	case OrphanMeta:
		if s.Clean == nil {
			return errors.New("no cleanup path configured")
		}
		return s.Clean(ctx, finding.TaskID, true)
	case OrphanStatus:
		return s.archiveStatus(finding.TaskID)
	default:
		return fmt.Errorf("no action for class %q", finding.Class)
	}
}

// returnWorktree prefers the existing cfo cleanup path, which closes the tab,
// returns the worktree and archives the record in one guarded step. A
// directory with no task record behind it has nothing for cleanup to read, so
// it goes through the same worktree return primitive cleanup itself calls,
// rather than through a second removal implementation.
func (s Service) returnWorktree(ctx context.Context, finding Finding) error {
	// A directory holding no files has nothing to return, and both paths below
	// run git inside it, which is answered by the enclosing repository. That
	// is the lie this whole branch exists to stop telling, so it must not be
	// told by the action either. What is left of a return is administrative:
	// remove the directory, then prune the registration that outlives it,
	// asked of the project, which is the repository that answered for this
	// path in the first place. A project that could not be asked registers
	// nothing here, so there is nothing to prune and no repository the sweep
	// has any business asking. A failed prune surfaces rather than being
	// swallowed: the directory is gone by then, and the administrative entry
	// it leaves is what nothing else in the fleet ever clears.
	if empty, err := isEmptyDir(finding.Path); err == nil && empty {
		if err := os.Remove(finding.Path); err != nil {
			return err
		}
		if !finding.Registered {
			return nil
		}
		_, err := s.git(ctx, filepath.Dir(filepath.Dir(finding.Path)), "worktree", "prune")
		return err
	}
	if finding.TaskID != "" && state.ValidTaskID(finding.TaskID) == nil {
		if _, err := os.Stat(filepath.Join(s.Home.State, finding.TaskID+".meta")); err == nil {
			if s.Clean == nil {
				return errors.New("no cleanup path configured")
			}
			return s.Clean(ctx, finding.TaskID, false)
		}
	}
	if s.Return == nil {
		return errors.New("no worktree return path configured")
	}
	project := filepath.Dir(filepath.Dir(finding.Path))
	return s.Return(ctx, project, finding.Path)
}

// archiveStatus moves an orphaned status log under state/archive/ rather than
// deleting it. It is the only record of what that goblin reported, and cleanup
// deliberately leaves it behind; the sweep's job is to get it out of the live
// listing, not to destroy the history.
func (s Service) archiveStatus(id string) error {
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.Home.State, state.ArchiveDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, id+".status."+s.now().UTC().Format("20060102T150405Z"))
	return os.Rename(filepath.Join(s.Home.State, id+".status"), target)
}

// holdIfNotEmpty refuses anything with contents. isEmptyDir reads one entry,
// which is all "does this directory hold files" needs. A directory the sweep
// could not read is refused too, and says it could not look rather than that
// it found contents; that refusal is absolute for the same reason its
// siblings in the work gate are, because a directory whose contents cannot be
// read is one that cannot be shown to be empty.
func holdIfNotEmpty(dir string) string {
	empty, err := isEmptyDir(dir)
	if err != nil {
		return "the sweep could not read this directory, so whether it is an abandoned shell or somebody's files is unknown rather than answered: " + err.Error() + "; resolve that and sweep again, because a directory that cannot be read may hold the only copy of somebody's work"
	}
	if !empty {
		return "the directory is not empty, so it is not an abandoned shell; what is in it has to be explained before it is removed"
	}
	return ""
}

// isEmptyDir reports whether a directory holds nothing at all.
func isEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func (s Service) git(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := s.Commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git %s exited with code %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func sampleWindow(options Options) time.Duration {
	if options.CPUSample > 0 {
		return options.CPUSample
	}
	return DefaultCPUSample
}

func idleThreshold(options Options) time.Duration {
	if options.CPUIdle > 0 {
		return options.CPUIdle
	}
	return DefaultCPUIdle
}

func (s Service) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
