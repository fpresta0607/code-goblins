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
	// ProbeIdle samples processor time to decide whether a process is really
	// idle. It costs CPUSample of wall clock per process finding, so the
	// watcher's background audit leaves it off and simply reports what it
	// found; --apply always turns it on, because nothing is ever killed
	// without a fresh idleness measurement.
	ProbeIdle bool
	// CPUSample and CPUIdle override the idleness gate's window and threshold.
	CPUSample time.Duration
	CPUIdle   time.Duration
}

func (o Options) forced(finding Finding) bool {
	if o.Force == nil {
		return false
	}
	if finding.PID != 0 && o.Force[strconv.Itoa(finding.PID)] {
		return true
	}
	return finding.TaskID != "" && o.Force[finding.TaskID]
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
	// return means the process could not be measured, which is treated as
	// "cannot prove it is idle" and holds the kill.
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
	for i := range result.Findings {
		finding := &result.Findings[i]
		if finding.Hold != "" {
			continue
		}
		if err := s.act(ctx, *finding); err != nil {
			finding.Hold = "action failed: " + err.Error()
			continue
		}
		line := "reaped: " + finding.Line()
		result.Applied = append(result.Applied, line)
		s.record(*finding, line)
	}
	return result, nil
}

// record writes one line per reaped resource: into the task's own status log
// when the finding names a task, and always into the fleet-level reap log, so
// there is one place that answers "what did the reaper do".
func (s Service) record(finding Finding, line string) {
	// An archived status log must not be recreated by the very line that
	// reports its archiving; the fleet log below carries that one.
	if finding.Class != OrphanStatus && finding.TaskID != "" && state.ValidTaskID(finding.TaskID) == nil {
		// Best effort: a failed status write must not undo a completed kill.
		_ = state.AppendStatus(s.Home.State, finding.TaskID, state.NormalizeStatusDetail(line))
	}
	_ = state.AppendStatus(s.Home.State, StatusID, state.NormalizeStatusDetail(line))
}

// gate is where the correctness lives. Every class gets the checks that make
// its action safe, and a refusal is recorded on the finding rather than
// dropping it: the operator must be able to see what was found AND why it was
// left alone.
func (s Service) gate(ctx context.Context, finding *Finding, options Options) {
	if finding.Hold != "" && options.forced(*finding) {
		// The operator named this exact pid or task, which is the only thing
		// that clears an attribution or non-terminal hold.
		finding.Hold = ""
	}
	if finding.Hold != "" {
		return
	}
	switch finding.Class {
	case OrphanProcess, StaleServer:
		if options.forced(*finding) || !options.ProbeIdle {
			return
		}
		if hold := s.holdIfBusy(finding.PID, options); hold != "" {
			finding.Hold = hold
		}
	case OrphanWorktree:
		// Runs even for a forced task: --force covers the operator's
		// judgement about a task's status, never a decision to destroy work.
		if hold := s.holdIfWorkWouldBeLost(ctx, finding.Path); hold != "" {
			finding.Hold = hold
		}
	}
}

// holdIfBusy samples processor time twice and refuses anything that moved.
// An unmeasurable process is held too: "I could not tell" is not "it is idle".
func (s Service) holdIfBusy(pid int, options Options) string {
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
	return ""
}

// holdIfWorkWouldBeLost refuses any worktree with uncommitted changes or with
// commits that exist nowhere else. A goblin's unpushed branch is the entire
// product of its run, and no --force clears this: the point of the sweep is to
// free resources, never to decide that somebody's work did not matter.
func (s Service) holdIfWorkWouldBeLost(ctx context.Context, worktree string) string {
	if worktree == "" {
		return ""
	}
	if s.Commands == nil {
		return "no command runner configured, so the worktree cannot be proven clean"
	}
	status, err := s.git(ctx, worktree, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "cannot read git status: " + err.Error()
	}
	if status != "" {
		return "worktree has uncommitted or untracked changes"
	}
	unpushed, err := s.git(ctx, worktree, "log", "--oneline", "HEAD", "--not", "--remotes")
	if err != nil {
		return "cannot read unpushed commits: " + err.Error()
	}
	if unpushed != "" {
		count := len(strings.Split(unpushed, "\n"))
		return fmt.Sprintf("worktree has %d commit(s) on no remote; pushing them is the only way this is safe to remove", count)
	}
	return ""
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
