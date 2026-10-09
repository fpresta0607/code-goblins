package fleettree

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

// GateProgress reads a goblin's gate run, as pipeline.Reader does: its
// branch's newest run or the run with the ID it names, the run's steps, and
// how long a round of each step usually runs on this machine.
type GateProgress interface {
	Progress(ctx context.Context, project, branch string) (pipeline.Progress, error)
	Run(ctx context.Context, runID string) (pipeline.Progress, error)
	StepDetails(ctx context.Context, runID string) ([]pipeline.StepDetail, error)
	UsualStepTimes(ctx context.Context) (map[string]time.Duration, error)
}

// GateEvery is how long one reading of a goblin's gate stands: each starts
// two sqlite3 processes.
const GateEvery = time.Minute

// UsualEvery is how long one reading of how long gate steps usually run
// stands: it reads every step the machine's gate has run.
const UsualEvery = time.Hour

// UsualUnknown is how long a round of a step with none on record is given.
const UsualUnknown = time.Hour

// gateReading is one reading of a goblin's gate. awaited is what the goblin
// waited on when it was read.
type gateReading struct {
	at       time.Time
	awaited  string
	progress pipeline.Progress
	steps    []pipeline.StepDetail
	err      error
}

// GateStep is a goblin's gate run at a step: running it, or parked on an
// answer there. Round is when the step's latest round started, since a fix
// round runs the step again, and Usual how long nine in ten of its rounds
// took on this machine, or UsualUnknown for a step with none on record.
type GateStep struct {
	Run    string
	Step   string
	Parked bool
	Round  time.Time
	Usual  time.Duration
}

// gateWords says what a step's status means for the run.
var gateWords = map[string]struct {
	state State
	words string
}{
	"running":           {Working, "running"},
	"fixing":            {Working, "fixing what it found"},
	"awaiting_approval": {Waiting, "waiting on a decision"},
	"fix_review":        {Waiting, "waiting on a decision"},
	"awaiting_agent":    {Waiting, "waiting on the goblin"},
	"failed":            {Failed, "failed"},
}

// gateEnded says how a run reads that the pipeline counts as ended.
var gateEnded = map[string]struct {
	state         State
	label, detail string
}{
	"completed":              {Done, "Gate passed", "every step passed"},
	"failed":                 {Failed, "Gate failed", "run failed"},
	"cancelled":              {Failed, "Gate cancelled", "run cancelled"},
	"ci_monitor_interrupted": {Failed, "Gate interrupted", "its watch of CI was interrupted"},
}

// gateNode is the goblin's gate run as a child: its active step, or how the
// run ended. A run that ended, and whose steps started before the goblin's
// generation, is an earlier goblin's, and ok is false for it, while a run
// still going is the goblin's own, as after a switch started a new
// generation.
func gateNode(reading gateReading, born time.Time, processes []Process, usual map[string]time.Duration) (Node, bool) {
	if reading.progress.RunID == "" || len(reading.steps) == 0 {
		return Node{}, false
	}
	var first, last time.Time
	for _, step := range reading.steps {
		if step.StartedAt > 0 && (first.IsZero() || unix(step.StartedAt).Before(first)) {
			first = unix(step.StartedAt)
		}
		last = later(last, unix(step.LastActivityAt))
	}
	ended, isEnded := gateEnded[reading.progress.Status]
	if first.IsZero() || isEnded && !born.IsZero() && first.Before(born) {
		return Node{}, false
	}
	node := Node{ID: "gate:" + reading.progress.RunID, Kind: KindGate, Started: first, LastActivity: last, SourceUpdatedAt: last}
	for _, step := range reading.steps {
		word, active := gateWords[step.Status]
		// An ended run's step records name no work going on, only the step
		// that failed it.
		if !active || isEnded && word.state != Failed {
			continue
		}
		node.Label = "Gate: " + step.Name
		node.Detail = step.Name + " step " + word.words
		node.State = word.state
		node.Started = later(first, unix(step.StartedAt))
		node.LastActivity = later(node.Started, unix(step.LastActivityAt))
		node.LastLine = bounded(firstLine(step.LastActivity), 200)
		node.Memory = agentMemory(step.AgentPID, unix(step.StartedAt), processes)
		if node.State == Failed {
			node.Finished = node.LastActivity
			return node, true
		}
		node.gate = GateStep{Run: reading.progress.RunID, Step: step.Name, Parked: node.State == Waiting, Round: unix(step.RoundStartedAt), Usual: UsualUnknown}
		if node.gate.Round.IsZero() {
			node.gate.Round = unix(step.StartedAt)
		}
		if took, isKnown := usual[step.Name]; isKnown {
			node.gate.Usual = took
		}
		return node, true
	}
	if !isEnded {
		node.Label, node.State, node.Detail = "Gate", Waiting, "between steps"
		return node, true
	}
	node.Label, node.State, node.Detail, node.Finished = ended.label, ended.state, ended.detail, last
	return node, true
}

// agentMemory is the private memory of a gate step's agent and everything
// under it, when that process still runs and started with the step.
func agentMemory(pid int, started time.Time, processes []Process) uint64 {
	if pid == 0 {
		return 0
	}
	var used uint64
	for _, process := range processes {
		if process.PID == pid && !process.Started.Before(started.Add(-time.Minute)) {
			agent, _ := readHarness(pid, processes, 0)
			for _, member := range agent.all {
				used += member.Memory
			}
			return used
		}
	}
	return 0
}

func unix(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// worktreeBranch is the branch a worktree has checked out, read from its git
// files, or empty when its HEAD is detached or unreadable.
func worktreeBranch(worktree string) string {
	gitDir := filepath.Join(worktree, ".git")
	if data, err := fsx.ReadFile(gitDir); err == nil {
		if linked, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
			gitDir = linked
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(worktree, gitDir)
			}
		}
	}
	head, err := fsx.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if branch == strings.TrimSpace(string(head)) {
		return ""
	}
	return branch
}
