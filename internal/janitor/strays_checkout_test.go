package janitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// checkoutWorktree makes a worktree a project registers away from the fleet's
// folders, at rel inside the checkout, as a harness's own tooling leaves one,
// and describes it as the inventory reports it: listed, and recorded by no task
// of this home.
func (f *sweepFixture) checkoutWorktree(t *testing.T, rel string) reap.WorktreeDir {
	t.Helper()
	path := filepath.Join(f.project, rel)
	gitIn(t, f.project, "worktree", "add", "-q", "--detach", path, "origin/main")
	return reap.WorktreeDir{Path: path, Project: f.project, Source: reap.SourceCheckout, Registration: reap.RegistrationListed, Created: f.now.Add(-2 * time.Hour)}
}

// A worktree git registers in one of the fleet's checkouts at a path no task of
// this home records is a harness's or an older session's, left inside a
// project rather than under the home. Nothing else in the fleet reports it, so
// the sweep returns it on the same rule as a goblin's own: clean and on the
// default branch, so it goes.
func TestSweepReturnsACheckoutWorktreeThatNoRecordNames(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	stray := f.checkoutWorktree(t, filepath.Join(".claude", "worktrees", "leftover"))

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{stray}}))

	// Assert
	if _, err := os.Stat(stray.Path); !os.IsNotExist(err) {
		t.Fatalf("the stray worktree survived: %v; kept %+v notes %v", err, record.Kept, record.Notes)
	}
	if item, ok := has(record.Removed, stray.Path); !ok || !strings.Contains(item.Detail, "on the default branch") {
		t.Errorf("removed = %+v, want the stray named with its work on the default branch", record.Removed)
	}
	if tags := gitIn(t, f.project, "tag", "--list", "archive/*"); tags != "" {
		t.Errorf("work already on main was archived as %s", tags)
	}
	if listed := gitIn(t, f.project, "worktree", "list"); strings.Contains(listed, "leftover") {
		t.Errorf("the project still registers it:\n%s", listed)
	}
}

// A checkout worktree holding uncommitted work is nobody's to discard: the
// sweep keeps it, reports it, and leaves it exactly as it is.
func TestSweepKeepsACheckoutWorktreeWithUncommittedWork(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	stray := f.checkoutWorktree(t, filepath.Join(".claude", "worktrees", "draft"))
	if err := os.WriteFile(filepath.Join(stray.Path, "draft.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{stray}}))

	// Assert
	if got, err := os.ReadFile(filepath.Join(stray.Path, "draft.txt")); err != nil || string(got) != "unsaved" {
		t.Fatalf("the uncommitted work = %q, %v; want it untouched", got, err)
	}
	if item, ok := has(record.Kept, stray.Path); !ok || !strings.Contains(item.Detail, "uncommitted") {
		t.Errorf("kept = %+v, want the stray kept for its uncommitted work", record.Kept)
	}
	if item, ok := has(record.Strays, stray.Path); !ok || !strings.Contains(item.Detail, "left by a harness or an older session") {
		t.Errorf("strays = %+v, want the stray reported as a harness's leftover", record.Strays)
	}
}

// A checkout worktree whose work is not on the default branch has its work kept
// as a local archive tag before the worktree goes, as a goblin's own does.
func TestSweepTagsUnlandedCheckoutWorktreeThenReturnsIt(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	stray := f.checkoutWorktree(t, filepath.Join(".claude", "worktrees", "own-work"))
	gitIn(t, stray.Path, "commit", "-q", "--allow-empty", "-m", "work of its own")
	head := gitIn(t, stray.Path, "rev-parse", "HEAD")

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{stray}}))

	// Assert
	if _, err := os.Stat(stray.Path); !os.IsNotExist(err) {
		t.Fatalf("the stray survived: %v; kept %+v", err, record.Kept)
	}
	if got := gitIn(t, f.project, "rev-parse", "refs/tags/archive/own-work^{commit}"); got != head {
		t.Errorf("archive/own-work = %s, want the worktree's own commit %s", got, head)
	}
	if item, ok := has(record.Removed, stray.Path); !ok || !strings.Contains(item.Detail, "archive/own-work") {
		t.Errorf("removed = %+v, want the stray named with its archive tag", record.Removed)
	}
}

// A checkout worktree inside a live task's own folders is that goblin's while
// it works: its harness made it there, and it goes with the task at its cleanup
// or its next relaunch, never under a live goblin's hands.
func TestSweepLeavesACheckoutWorktreeInsideALiveTasksFolders(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	scratch := filepath.Join(f.home.Scratch(), "live")
	stray := reap.WorktreeDir{Path: filepath.Join(scratch, "claude", "scratchpad", "goblin-probe"), Project: f.project, Source: reap.SourceCheckout, Registration: reap.RegistrationListed, Created: f.now.Add(-2 * time.Hour)}
	gitIn(t, f.project, "worktree", "add", "-q", "--detach", stray.Path, "origin/main")
	fixture := f.config(reap.Inventory{
		Worktrees: []reap.WorktreeDir{stray},
		Tasks:     []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live", Project: f.project, Worktree: filepath.Join(f.home.Worktrees(), "app", "live"), Scratch: scratch}}},
	})

	// Act
	record := Sweep(context.Background(), fixture)

	// Assert
	if _, err := os.Stat(stray.Path); err != nil {
		t.Fatalf("a live task's harness worktree was removed: %v", err)
	}
	if item, ok := has(record.Kept, stray.Path); !ok || !strings.Contains(item.Detail, "inside a live task's own folders") {
		t.Errorf("kept = %+v, want it kept as a live task's", record.Kept)
	}
}
