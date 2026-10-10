package spawn

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cleanup"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// heldCleanup stops a cleanup at its first git status, which it asks for once
// it holds the task and before it removes anything, until released.
type heldCleanup struct {
	execx.Runner
	holds   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *heldCleanup) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" && len(request.Args) > 0 && request.Args[0] == "status" {
		h.once.Do(func() { close(h.holds) })
		<-h.release
	}
	return h.Runner.Run(ctx, request)
}

// pausedForResume records the fixture's task as paused with a resume of it
// under way, as cfo resume leaves it when it hands the relaunch to Switch.
func pausedForResume(t *testing.T, f *switchFixture) SwitchRequest {
	t.Helper()
	if err := state.WriteLifecycle(f.stateDir, state.Lifecycle{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "auto-resume-1", Action: "resume", Phase: "resuming"}); err != nil {
		t.Fatal(err)
	}
	return SwitchRequest{ID: f.meta.ID, Generation: f.meta.SpawnGen, IsResume: true, Admit: func() error { return nil }}
}

// The 2026-10-09 20:15Z order, held still by a seam: the supervisor resumes
// a paused goblin whose pause cleared while the CFO's cleanup of it runs.
// The cleanup holds the task and has not removed its worktree yet, so the
// resume's own early look at the worktree passes. Then the resume wrote a new
// task record and started a host into a worktree that was going, and the
// cleanup ended on "task still has live metadata".
func TestAResumeChangesNothingOnceACleanupHoldsTheTask(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	request := pausedForResume(t, f)
	held := &heldCleanup{Runner: f.git, holds: make(chan struct{}), release: make(chan struct{})}
	cleaner := cleanup.Service{StateDir: f.stateDir, Commands: held, Terminal: &herdr.Client{Commands: held, Session: "fleet"}, Worktrees: f.service.Worktrees}
	type cleaned struct {
		result cleanup.Result
		err    error
	}
	done := make(chan cleaned, 1)
	go func() {
		result, err := cleaner.Cleanup(context.Background(), f.meta.ID)
		done <- cleaned{result, err}
	}()
	select {
	case <-held.holds:
	case finished := <-done:
		t.Fatalf("the cleanup ended before it looked at the worktree: %+v, %v", finished.result, finished.err)
	case <-time.After(30 * time.Second):
		t.Fatal("the cleanup never looked at the worktree")
	}

	// Act
	_, resumeErr := f.service.Switch(context.Background(), request)

	// Assert: the resume refused, saying why in one line, with no task record
	// written and no host started.
	if resumeErr == nil || !strings.Contains(resumeErr.Error(), state.CleanupPurpose(f.meta.ID)) || strings.Contains(resumeErr.Error(), "\n") {
		t.Errorf("the resume = %v, want one line refusing it for %s", resumeErr, state.CleanupPurpose(f.meta.ID))
	}
	if after, err := state.ReadTaskMeta(f.stateDir, f.meta.ID); err != nil || after.SpawnGen != f.meta.SpawnGen {
		t.Errorf("the task record after the refused resume = %+v, %v, want it as the cleanup found it", after, err)
	}
	if _, err := os.Stat(f.record); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a harness was started for a task its cleanup holds: %v", err)
	}

	// The cleanup then ends cleanly: the task is retired and stays retired.
	close(held.release)
	finished := <-done
	if finished.err != nil {
		t.Fatalf("the cleanup: %v", finished.err)
	}
	if strings.Contains(finished.result.Output, "live metadata") {
		t.Errorf("the cleanup's output = %q, want no task record written under it", finished.result.Output)
	}
	if _, err := state.ReadTaskMeta(f.stateDir, f.meta.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task record after the cleanup: %v, want it retired", err)
	}
	if f.nativeFixture.git.returned != 1 {
		t.Errorf("the worktree was returned %d times, want once", f.nativeFixture.git.returned)
	}
}

// A relaunch can wait a long time for its turn on the spawn lock after its
// early look at the task. What took the task away meanwhile, as the teardown
// of the failed spawn it waited on does, is found by a second look once the
// wait is over, before anything is stopped, written or started.
func TestARelaunchLooksAgainAtItsTaskOnceItsWaitIsOver(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	request := pausedForResume(t, f)
	request.Admit = func() error {
		return state.RemoveTaskMeta(f.stateDir, f.meta.ID)
	}

	// Act
	_, err := f.service.Switch(context.Background(), request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is gone") || strings.Contains(err.Error(), "\n") {
		t.Errorf("Switch = %v, want one line saying the task is gone", err)
	}
	if _, readErr := state.ReadTaskMeta(f.stateDir, f.meta.ID); !errors.Is(readErr, os.ErrNotExist) {
		t.Errorf("the relaunch wrote a task record for a task that is gone: %v", readErr)
	}
	if _, statErr := os.Stat(f.record); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a harness was started for a task that is gone: %v", statErr)
	}
}
