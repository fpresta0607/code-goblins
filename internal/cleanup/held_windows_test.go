package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// holdAsWorkingDirectory holds dir as a process holds its working directory,
// with traverse and synchronize access and no delete sharing, until the test
// ends, so the folder cannot be removed meanwhile.
func holdAsWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.FILE_TRAVERSE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(handle) })
	if err := os.Remove(dir); err == nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("removing the held folder = %v; want it refused as in use", err)
	}
}

// #915, 2026-10-14: an earlier cleanup removed cg-dictation-everywhere's
// files and .git link and stopped at the folder, which the lavish-axi page
// server a goblin's call had started from it held as its working directory.
// Every cleanup after stopped there too, and left the task working. The task
// retires anyway, and the folder is left for the janitor, which removes an
// empty folder no task owns and no project registers once nothing holds it:
// git listed this one as a prunable worktree, which the janitor keeps, so
// the cleanup prunes that record.
func TestAPlainCleanupRetiresATaskWhoseEmptyFolderAnotherProcessHolds(t *testing.T) {
	// Arrange
	task := newHomeTask(t)
	meta, err := state.ReadTaskMeta(task.stateDir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Extras = nil
	if err := state.WriteTaskMeta(task.stateDir, meta); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(task.worktree)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(task.worktree, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if listed := realGit(t, task.project, "worktree", "list", "--porcelain"); !strings.Contains(listed, "prunable") {
		t.Fatalf("git does not list the emptied worktree as prunable:\n%s", listed)
	}
	holdAsWorkingDirectory(t, task.worktree)

	// Act
	result, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup = %v; want the task retired, the held folder left for the janitor", err)
	}
	if !strings.Contains(result.Output, "left the empty folder "+task.worktree+" for the janitor") || strings.Contains(result.Output, "warning: git") {
		t.Errorf("output = %q; want it to say the folder is left for the janitor, and no prune failed", result.Output)
	}
	if _, err := os.Stat(task.worktree); err != nil {
		t.Errorf("the held folder = %v; want it left in place", err)
	}
	if _, err := state.ReadTaskMeta(task.stateDir, "g1"); err == nil {
		t.Error("task metadata still present; want the task retired")
	}
	if listed := realGit(t, task.project, "worktree", "list", "--porcelain"); strings.Contains(listed, "prunable") {
		t.Errorf("git still lists the held folder as a prunable worktree, which the janitor keeps:\n%s", listed)
	}
}

// A prune that fails leaves git's record of the held folder, which the
// janitor keeps, so the cleanup says so rather than hand it over silently.
func TestAHeldEmptyFolderWhosePruneFailsSaysTheJanitorMayKeepIt(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	fixture.git.top = fixture.project
	holdAsWorkingDirectory(t, fixture.worktree)

	// Act
	result, err := fixture.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup = %v; want the task retired all the same", err)
	}
	if !strings.Contains(result.Output, "warning: git may still list "+fixture.worktree) || !strings.Contains(result.Output, "fatal: not a git repository") {
		t.Errorf("output = %q; want a warning that names the folder and the failed prune", result.Output)
	}
	if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err == nil {
		t.Error("task metadata still present; want the task retired")
	}
}

// #915, 2026-10-14: a worktree whose .venv held .pyd files other tasks'
// Python processes had loaded, the same files through other hard links of
// uv's cache, could not be removed, and the task stayed working through each
// try. Its work is proven clean and kept before the removal, so the task
// retires anyway and the worktree is left for the janitor, which returns it
// once nothing holds its files. A removal that fails for any other reason
// still refuses, leaving the task as it was, and so does a hold that stops
// the keeping of its unlanded work, which nothing would keep after.
func TestAPlainCleanupRetiresATaskWhoseWorktreeAnotherProcessHolds(t *testing.T) {
	for name, row := range map[string]struct {
		err        error
		archiveErr error
		isRetired  bool
	}{
		"a file a process has open":    {err: windows.ERROR_SHARING_VIOLATION, isRetired: true},
		"a module a process loaded":    {err: windows.ERROR_ACCESS_DENIED, isRetired: true},
		"a removal refused otherwise":  {err: errors.New("worktree: ambiguous return refused")},
		"a tag its hold stopped first": {err: windows.ERROR_SHARING_VIOLATION, archiveErr: fmt.Errorf("worktree: write the archive tag: %w", windows.ERROR_ACCESS_DENIED)},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			fixture.git.returnErr = fmt.Errorf("worktree: remove the files of %q: %w", fixture.worktree, row.err)
			if row.archiveErr != nil {
				fixture.git.unlanded = map[string]bool{filepath.Base(fixture.worktree): true}
				fixture.git.archiveErr = row.archiveErr
			}

			// Act
			result, err := fixture.service.Cleanup(context.Background(), "g1")

			// Assert
			_, metaErr := state.ReadTaskMeta(fixture.stateDir, "g1")
			if row.isRetired && (err != nil || metaErr == nil || !strings.Contains(result.Output, "left the worktree "+fixture.worktree+" for the janitor")) {
				t.Errorf("Cleanup = %v, output %q, task still recorded %v; want it retired and the worktree left for the janitor", err, result.Output, metaErr == nil)
			}
			if !row.isRetired && (err == nil || metaErr != nil) {
				t.Errorf("Cleanup = %v, task recorded %v; want it refused and the task left as it was", err, metaErr == nil)
			}
		})
	}
}
