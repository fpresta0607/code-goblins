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

type fakeGate struct {
	progress pipeline.Progress
	steps    []pipeline.StepDetail
	branch   string
	reads    int
}

func (g *fakeGate) Progress(_ context.Context, _, branch string) (pipeline.Progress, error) {
	g.reads++
	if branch != g.branch {
		return pipeline.Progress{}, pipeline.ErrNoProgress
	}
	return g.progress, nil
}

func (g *fakeGate) StepDetails(context.Context, string) ([]pipeline.StepDetail, error) {
	return g.steps, nil
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
			node, ok := gateNode(gateReading{progress: pipeline.Progress{RunID: "r", Status: test.status}, steps: test.steps}, born, nil)
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

func (missingGate) StepDetails(context.Context, string) ([]pipeline.StepDetail, error) {
	return nil, nil
}
