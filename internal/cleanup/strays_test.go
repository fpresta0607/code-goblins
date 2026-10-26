package cleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A harness's own tooling leaves worktrees of its own inside the folders its
// goblin works in: one in the task's scratch folder, which is its TEMP, and one
// in the checkout it runs in. Both are the task's, so its cleanup returns them
// with its own worktree and names each one, rather than leaving them behind to
// be found as strays later.
func TestCleanupReturnsTheWorktreesItsHarnessLeftAndNamesEach(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	scratch := filepath.Join(t.TempDir(), "scratch")
	scratchpad := filepath.Join(scratch, "claude", "scratchpad", "probe")
	nested := filepath.Join(fixture.worktree, ".claude", "worktrees", "probe")
	for _, dir := range []string{scratchpad, nested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture.meta.Scratch = scratch
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	fixture.runner.registered = "worktree " + scratchpad + "\nworktree " + nested + "\nworktree " + fixture.worktree + "\n"

	// Act
	result, err := fixture.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	for _, path := range []string{scratchpad, nested} {
		if !strings.Contains(result.Output, "returned harness worktree "+path) {
			t.Errorf("Output = %q, want it naming the returned harness worktree %s", result.Output, path)
		}
	}
	returned := map[string]bool{}
	for _, pair := range fixture.git.returned {
		returned[pair[1]] = true
	}
	for _, path := range []string{scratchpad, nested, fixture.worktree} {
		if !returned[path] {
			t.Errorf("worktree returns = %v, want %s among them", fixture.git.returned, path)
		}
	}
}

// A harness worktree holding uncommitted work is the goblin's, not the
// sweep's: the cleanup keeps it and says so, rather than discarding work.
func TestCleanupKeepsAHarnessWorktreeWithUncommittedWork(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	scratch := filepath.Join(t.TempDir(), "scratch")
	untouched := filepath.Join(scratch, "claude", "scratchpad", "draft")
	if err := os.MkdirAll(untouched, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.meta.Scratch = scratch
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	fixture.runner.registered = "worktree " + untouched + "\n"
	fixture.git.returnErr = errors.New("worktree: \"" + untouched + "\" has uncommitted work; refusing to remove it")
	fixture.git.returnErrPath = untouched

	// Act
	result, err := fixture.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !strings.Contains(result.Output, "kept the harness worktree "+untouched) {
		t.Errorf("Output = %q, want it naming the harness worktree it kept", result.Output)
	}
	if _, err := os.Stat(untouched); err != nil {
		t.Errorf("the harness worktree was removed although it could not be returned: %v", err)
	}
}

// A goblin's harness may be using a worktree of its own right now, so nothing
// of the task's is touched until its terminal is proven to hold no working
// agent: the cleanup refuses and every folder stays exactly as it was.
func TestCleanupRefusesBeforeTouchingAHarnessWorktreeOfAnAgentAtWork(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	scratch := filepath.Join(t.TempDir(), "scratch")
	inUse := filepath.Join(scratch, "claude", "scratchpad", "probe")
	if err := os.MkdirAll(inUse, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.meta.Scratch = scratch
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	fixture.runner.registered = "worktree " + inUse + "\n"
	fixture.runner.snapshotBody = cleanupSnapshot(
		`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws"}`,
		`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws","agent":"claude","agent_status":"working"}`)

	// Act
	_, err := fixture.service.Cleanup(context.Background(), "g1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "still has agent") {
		t.Fatalf("Cleanup = %v, want it refused for the agent at work", err)
	}
	if _, err := os.Stat(inUse); err != nil {
		t.Errorf("the agent's harness worktree was removed behind it: %v", err)
	}
	if len(fixture.git.returned) != 0 {
		t.Errorf("worktree returns = %v, want none before the agent is proven gone", fixture.git.returned)
	}
}

func TestCleanupRefusesWhenHarnessWorktreesCannotBeRead(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.runner.isWorktreeListUnavailable = true

	_, err := fixture.service.Cleanup(context.Background(), "g1")

	if err == nil || !strings.Contains(err.Error(), "worktree list unavailable") {
		t.Fatalf("Cleanup = %v, want it refused when worktrees cannot be read", err)
	}
	if len(fixture.git.returned) != 0 {
		t.Errorf("returned = %v, want no removal after an unreadable scan", fixture.git.returned)
	}
	if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err != nil {
		t.Fatalf("task record was retired after an unreadable scan: %v", err)
	}
}

func TestCleanupKeepsTheParentOfAnIgnoredHarnessWorktreeItCannotReturn(t *testing.T) {
	fixture := newCleanupFixture(t)
	nested := filepath.Join(fixture.worktree, ".claude", "worktrees", "draft")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.runner.registered = "worktree " + nested + "\n"
	fixture.git.returnErr = errors.New("uncommitted work")
	fixture.git.returnErrPath = nested

	_, err := fixture.service.Cleanup(context.Background(), "g1")

	if err == nil || !strings.Contains(err.Error(), nested) {
		t.Fatalf("Cleanup = %v, want it to keep and name the nested worktree", err)
	}
	for _, returned := range fixture.git.returned {
		if returned[1] == fixture.worktree {
			t.Fatal("cleanup returned the parent of a harness worktree it could not return")
		}
	}
}

func TestCleanupForceArchiveKeepsScratchWorktrees(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.service.ForceArchive = true
	scratch := filepath.Join(t.TempDir(), "scratch")
	nested := filepath.Join(scratch, "scratchpad", "draft")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.meta.Scratch = scratch
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	fixture.runner.registered = "worktree " + nested + "\n"

	result, err := fixture.service.Cleanup(context.Background(), "g1")

	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("force-archive removed a scratch worktree: %v", err)
	}
	if !strings.Contains(result.Output, nested) {
		t.Errorf("output = %q, want the kept worktree named", result.Output)
	}
}
