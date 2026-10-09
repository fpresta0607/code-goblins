package fleettree

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeGate is the branch's run with its steps, a run a goblin can name with
// its own steps, and how long each step's rounds usually take.
type fakeGate struct {
	progress   pipeline.Progress
	steps      []pipeline.StepDetail
	branch     string
	reads      int
	named      pipeline.Progress
	namedSteps []pipeline.StepDetail
	usual      map[string]time.Duration
	usualReads int
}

func (g *fakeGate) Progress(_ context.Context, _, branch string) (pipeline.Progress, error) {
	g.reads++
	if branch != g.branch {
		return pipeline.Progress{}, pipeline.ErrNoProgress
	}
	return g.progress, nil
}

func (g *fakeGate) Run(_ context.Context, runID string) (pipeline.Progress, error) {
	if runID != g.named.RunID {
		return pipeline.Progress{}, pipeline.ErrNoProgress
	}
	return g.named, nil
}

func (g *fakeGate) StepDetails(_ context.Context, runID string) ([]pipeline.StepDetail, error) {
	if runID == g.named.RunID {
		return g.namedSteps, nil
	}
	return g.steps, nil
}

func (g *fakeGate) UsualStepTimes(context.Context) (map[string]time.Duration, error) {
	g.usualReads++
	return g.usual, nil
}

// gateWorktree is a worktree whose linked git folder has feat/tree checked
// out, as a goblin's worktree has.
func gateWorktree(t *testing.T) string {
	t.Helper()
	worktree := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "worktrees", "gb-tree")
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n", at)
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat/tree\n", at)
	return worktree
}

func TestReadShowsTheGoblinsGateRunAndItsActiveStep(t *testing.T) {
	// Arrange
	born := at.Add(-2 * time.Hour)
	stepStart := at.Add(-6 * time.Minute)
	gate := &fakeGate{branch: "feat/tree", progress: pipeline.Progress{RunID: "01M3", Status: "running"}, steps: []pipeline.StepDetail{
		{Name: "review", Status: "completed", StartedAt: at.Add(-20 * time.Minute).Unix(), LastActivityAt: at.Add(-7 * time.Minute).Unix()},
		{Name: "test", Status: "running", StartedAt: stepStart.Unix(), LastActivityAt: at.Add(-time.Minute).Unix(), LastActivity: "go test ./internal/fleettree\nmore", AgentPID: 300},
		{Name: "lint", Status: "pending"},
	}}
	processes := []Process{
		{PID: 300, ParentPID: 7, Exe: "claude.exe", Started: stepStart.Add(time.Second), Memory: 1000 * megabyte},
		{PID: 301, ParentPID: 300, Exe: "go.exe", Started: stepStart.Add(time.Minute), Memory: 100 * megabyte},
		{PID: 400, ParentPID: 1, Exe: "pwsh.exe", Started: born, Memory: 10 * megabyte},
	}
	reader := Reader{Home: t.TempDir(), Gate: gate, Processes: func() ([]Process, error) { return processes, nil }, Now: func() time.Time { return at }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t), Project: `C:\dev\code-goblins`, SpawnGen: "s" + itoa(born.UnixNano())}

	// Act
	tree, _ := reader.Read(context.Background(), Goblin{Meta: meta, HarnessPID: 400})
	again, _ := reader.Read(context.Background(), Goblin{Meta: meta, HarnessPID: 400})

	// Assert
	node := child(t, tree, "gate:01M3")
	if node.Kind != KindGate || node.Label != "Gate: test" || node.State != Working || !node.Started.Equal(stepStart) || node.LastLine != "go test ./internal/fleettree" || node.Memory != 1100*megabyte {
		t.Errorf("gate = %+v, want its running test step, started %v, with its agent's memory", node, stepStart)
	}
	if gate.reads != 1 || len(again.Children) != 1 {
		t.Errorf("gate read %d times for two tree reads within %v, want once", gate.reads, GateEvery)
	}
}

// A gate run from before the goblin's generation is an earlier goblin's.
func TestReadLeavesOutAnEarlierGenerationsGateRun(t *testing.T) {
	// Arrange
	born := at.Add(-time.Hour)
	gate := &fakeGate{branch: "feat/tree", progress: pipeline.Progress{RunID: "old", Status: "completed"}, steps: []pipeline.StepDetail{
		{Name: "review", Status: "completed", StartedAt: born.Add(-time.Hour).Unix(), LastActivityAt: born.Add(-50 * time.Minute).Unix()},
	}}
	reader := Reader{Home: t.TempDir(), Gate: gate, Now: func() time.Time { return at }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t), SpawnGen: "s" + itoa(born.UnixNano())}

	// Act
	tree, _ := reader.Read(context.Background(), Goblin{Meta: meta})

	// Assert
	if len(tree.Children) != 0 {
		t.Errorf("children = %+v, want no gate from before this generation", tree.Children)
	}
}

// A switch starts a new generation of the goblin and leaves its gate run
// going: on 2026-10-09 Duke was switched while his run's test step ran. A run
// of the branch still going is the goblin's own, whenever it started.
func TestReadKeepsARunStillGoingFromBeforeASwitch(t *testing.T) {
	// Arrange
	born := at.Add(-time.Hour)
	gate := &fakeGate{branch: "feat/tree", progress: pipeline.Progress{RunID: "01M4", Status: "running"}, steps: []pipeline.StepDetail{
		{Name: "review", Status: "completed", StartedAt: born.Add(-2 * time.Hour).Unix(), LastActivityAt: born.Add(-90 * time.Minute).Unix()},
		{Name: "test", Status: "fixing", StartedAt: born.Add(-90 * time.Minute).Unix(), RoundStartedAt: at.Add(-5 * time.Minute).Unix()},
	}}
	reader := Reader{Home: t.TempDir(), Gate: gate, Now: func() time.Time { return at }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t), SpawnGen: "s" + itoa(born.UnixNano())}

	// Act
	tree, _ := reader.Read(context.Background(), Goblin{Meta: meta})

	// Assert
	if step, ok := tree.Gate(); !ok || step.Run != "01M4" || step.Step != "test" {
		t.Errorf("gate = %+v, %v; want the switched goblin's run at its test step, children %+v", step, ok, tree.Children)
	}
}

func TestGateNodeSaysWhereTheRunStands(t *testing.T) {
	born := at.Add(-time.Hour)
	started := at.Add(-30 * time.Minute).Unix()
	for name, test := range map[string]struct {
		status string
		steps  []pipeline.StepDetail
		state  State
		label  string
	}{
		"fixing":        {"running", []pipeline.StepDetail{{Name: "review", Status: "fixing", StartedAt: started}}, Working, "Gate: review"},
		"a decision":    {"running", []pipeline.StepDetail{{Name: "review", Status: "awaiting_approval", StartedAt: started}}, Waiting, "Gate: review"},
		"a failed step": {"failed", []pipeline.StepDetail{{Name: "test", Status: "failed", StartedAt: started}}, Failed, "Gate: test"},
		"passed":        {"completed", []pipeline.StepDetail{{Name: "ci", Status: "completed", StartedAt: started}}, Done, "Gate passed"},
		"cancelled":     {"cancelled", []pipeline.StepDetail{{Name: "review", Status: "completed", StartedAt: started}}, Failed, "Gate cancelled"},
		"between steps": {"running", []pipeline.StepDetail{{Name: "review", Status: "completed", StartedAt: started}, {Name: "test", Status: "pending"}}, Waiting, "Gate"},
		// An ended run's step left fixing or running is not work going on.
		"cancelled mid-fix":       {"cancelled", []pipeline.StepDetail{{Name: "review", Status: "fixing", StartedAt: started}}, Failed, "Gate cancelled"},
		"an interrupted CI watch": {"ci_monitor_interrupted", []pipeline.StepDetail{{Name: "review", Status: "completed", StartedAt: started}, {Name: "ci", Status: "running", StartedAt: started}}, Failed, "Gate interrupted"},
	} {
		t.Run(name, func(t *testing.T) {
			node, ok := gateNode(gateReading{progress: pipeline.Progress{RunID: "r", Status: test.status}, steps: test.steps}, born, nil, nil)
			if !ok || node.State != test.state || node.Label != test.label {
				t.Errorf("gateNode = %+v, %v; want %s %q", node, ok, test.state, test.label)
			}
		})
	}
}

// A home whose no-mistakes has never run keeps no state database: that is no
// gate run, not evidence that could not be read.
func TestReadTakesAMissingGateDatabaseAsNoRun(t *testing.T) {
	// Arrange
	missing := &missingGate{}
	reader := Reader{Home: t.TempDir(), Gate: missing, Now: func() time.Time { return at }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t)}

	// Act
	tree, _ := reader.Read(context.Background(), Goblin{Meta: meta})

	// Assert
	if len(tree.Children) != 0 || len(tree.Unread) != 0 {
		t.Errorf("tree = %+v, want no gate and nothing unread", tree)
	}
}

type missingGate struct{}

func (missingGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return pipeline.Progress{}, fmt.Errorf("pipeline: cannot inspect state database: %w", fs.ErrNotExist)
}

func (missingGate) Run(context.Context, string) (pipeline.Progress, error) {
	return pipeline.Progress{}, fmt.Errorf("pipeline: cannot inspect state database: %w", fs.ErrNotExist)
}

func (missingGate) StepDetails(context.Context, string) ([]pipeline.StepDetail, error) {
	return nil, nil
}

func (missingGate) UsualStepTimes(context.Context) (map[string]time.Duration, error) {
	return nil, nil
}

// A goblin can name the gate run it waits on, such as one it started outside
// its worktree: that run is its gate, and a wait on anything else, such as
// another task, leaves it its branch's run. A new wait is read at once.
func TestReadTakesTheRunAGoblinWaitsOnAsItsGate(t *testing.T) {
	// Arrange
	born := at.Add(-time.Hour)
	gate := &fakeGate{
		branch: "feat/tree", progress: pipeline.Progress{RunID: "01BRANCH", Status: "running"},
		steps: []pipeline.StepDetail{{Name: "review", Status: "running", StartedAt: at.Add(-10 * time.Minute).Unix()}},
		named: pipeline.Progress{RunID: "01NAMED", Status: "running"}, namedSteps: []pipeline.StepDetail{{Name: "test", Status: "running", StartedAt: born.Add(-time.Hour).Unix()}},
	}
	awaited := "other-task"
	reader := Reader{Home: t.TempDir(), Gate: gate, Awaited: func(state.TaskMeta) string { return awaited }, Now: func() time.Time { return at }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t), SpawnGen: "s" + itoa(born.UnixNano())}

	// Act
	onTask, _ := reader.Read(context.Background(), Goblin{Meta: meta})
	awaited = "01NAMED"
	onRun, _ := reader.Read(context.Background(), Goblin{Meta: meta})

	// Assert
	if len(onTask.Children) != 1 || onTask.Children[0].ID != "gate:01BRANCH" {
		t.Errorf("waiting on a task, children = %+v, want the branch's run", onTask.Children)
	}
	if len(onRun.Children) != 1 || onRun.Children[0].ID != "gate:01NAMED" || onRun.Children[0].Label != "Gate: test" {
		t.Errorf("waiting on a run, children = %+v, want the named run's test step", onRun.Children)
	}
}

// The supervisor judges a goblin's wait on its gate by the step's latest
// round and how long the step's rounds usually take on this machine, which
// is read once an hour. A step with none on record is given UsualUnknown,
// and a run at no step, such as one that passed, has no step to wait on.
func TestGateIsTheStepItsRoundAndItsUsualTime(t *testing.T) {
	// Arrange
	born := at.Add(-2 * time.Hour)
	round := at.Add(-5 * time.Minute)
	parkedAt := at.Add(-time.Minute)
	gate := &fakeGate{branch: "feat/tree", progress: pipeline.Progress{RunID: "01M3", Status: "running"}, usual: map[string]time.Duration{"test": 38 * time.Minute}, steps: []pipeline.StepDetail{
		{Name: "review", Status: "completed", StartedAt: at.Add(-time.Hour).Unix()},
		{Name: "test", Status: "fixing", StartedAt: at.Add(-40 * time.Minute).Unix(), RoundStartedAt: round.Unix()},
	}}
	clock := at
	reader := Reader{Home: t.TempDir(), Gate: gate, Now: func() time.Time { return clock }}
	meta := state.TaskMeta{ID: "tree", Harness: "pi", Worktree: gateWorktree(t), SpawnGen: "s" + itoa(born.UnixNano())}
	read := func(when time.Time, status string, steps ...pipeline.StepDetail) (GateStep, bool) {
		clock, gate.progress.Status = when, status
		if len(steps) > 0 {
			gate.steps = steps
		}
		tree, _ := reader.Read(context.Background(), Goblin{Meta: meta})
		return tree.Gate()
	}

	// Act
	fixing, isFixing := read(at, "running")
	parked, isParked := read(at.Add(GateEvery), "running", pipeline.StepDetail{Name: "document", Status: "awaiting_approval", StartedAt: parkedAt.Unix()})
	_, isPassed := read(at.Add(2*GateEvery), "completed", pipeline.StepDetail{Name: "ci", Status: "completed", StartedAt: parkedAt.Unix()})

	// Assert
	if want := (GateStep{Run: "01M3", Step: "test", Round: round, Usual: 38 * time.Minute}); !isFixing || fixing != want {
		t.Errorf("fixing = %+v, %v; want %+v", fixing, isFixing, want)
	}
	if want := (GateStep{Run: "01M3", Step: "document", Parked: true, Round: parkedAt, Usual: UsualUnknown}); !isParked || parked != want {
		t.Errorf("parked = %+v, %v; want %+v", parked, isParked, want)
	}
	if isPassed {
		t.Error("a passed run has a step to wait on")
	}
	if gate.usualReads != 1 {
		t.Errorf("usual times read %d times within %v, want once", gate.usualReads, UsualEvery)
	}
}
