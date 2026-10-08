package spawn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnStartsAHelperOnABranchCutFromItsParentsLastCommit(t *testing.T) {
	// Arrange: goblin "task" works on feat/x; its helper is task-7, the id
	// the native fixture watches, in a real repository.
	f := newQuickFixture(t)
	project, parentWorktree := parentRepository(t)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "task", Project: project, Worktree: parentWorktree})
	f.service.Worktrees.Git = nil
	f.service.Worktrees.Commands = execx.OSRunner{}
	f.service.Commands = execx.OSRunner{}
	f.request.Project, f.request.Parent, f.request.Mode = project, "task", "local-only"
	writeFile(t, f.brief, "Do the helper's part.\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := f.service.Spawn(ctx, f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Parent != "task" || meta.Mode != "local-only" || meta.Worktree != result.Meta.Worktree {
		t.Errorf("record = %+v, want a local-only helper of task", meta)
	}
	if branch := gitIn(t, meta.Worktree, "symbolic-ref", "--short", "HEAD"); branch != "feat/x-7" {
		t.Errorf("the helper's worktree is on %q, want feat/x-7", branch)
	}
	if head, want := gitIn(t, meta.Worktree, "rev-parse", "HEAD"), gitIn(t, parentWorktree, "rev-parse", "HEAD"); head != want {
		t.Errorf("the helper starts at %s, want its parent's last commit %s", head, want)
	}
	if fsx.SamePath(meta.Worktree, parentWorktree) {
		t.Errorf("the helper shares its parent's worktree %s", parentWorktree)
	}
	submitted := named(f.events(t), "submitted")
	if len(submitted) != 1 || !strings.Contains(delivered(t, submitted[0].Text), "helper goblin of task") {
		t.Errorf("submitted = %+v, want the helper's instruction once", submitted)
	}
}
