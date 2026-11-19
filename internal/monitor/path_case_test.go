package monitor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// caseSpellings makes the folder parent\name, name in lowercase, and returns
// its path, the same path with name in uppercase, and whether the file system
// opens the folder by that second spelling: the oracle a case test here holds
// a comparison to, read from the file system itself. Where it does not, the
// second spelling is made as a folder of its own, so a comparison that reads
// what is on disk cannot take the two for one.
func caseSpellings(t *testing.T, parent, name string) (string, string, bool) {
	t.Helper()
	lower := filepath.Join(parent, name)
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, strings.ToUpper(name))
	_, err := os.Stat(upper)
	isOnePath := err == nil
	if !isOnePath {
		if err := os.Mkdir(upper, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return lower, upper, isOnePath
}

// A directory in a worktree a task record names, spelled in another case, is
// that task's exactly where the file system opens the one by the other; else
// it is the worktree the folder's own name names.
func TestRecordedWorktreesPlaceADirectoryByTheFileSystemsCase(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "worktrees")
	worktree, upper, isOnePath := caseSpellings(t, filepath.Join(root, "app"), "task-3-proof")
	meta := metaFor("task-3")
	meta.Worktree = worktree
	writeTask(t, stateDir, meta)
	want := "TASK-3-PROOF"
	if isOnePath {
		want = "task-3"
	}

	// Act
	task := worktreeTask(recordedWorktrees(stateDir), []string{root}, filepath.Join(upper, "web"))

	// Assert
	if task != want {
		t.Fatalf("worktreeTask(%s) = %q, want %q, as the file system opens %s by %s: %t", upper, task, want, worktree, upper, isOnePath)
	}
}

// A goblin's own poll names its task by the worktree it runs in: a task named
// in another case is a goblin this home knows exactly where the file system
// opens the goblin's record by that spelling.
func TestScanFlagsAPollOfATaskInAnotherCaseWhereTheFileSystemSaysSo(t *testing.T) {
	// Arrange
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 4242, Start: now.Add(-time.Minute), Task: "G1", Page: "plan.html"}}}
	service := pollScanService(t, &now, polls, "g1")
	_, err := os.Stat(filepath.Join(service.StateDir, "G1.meta"))
	isOnePath := err == nil

	// Act
	event := scanOnce(t, service)

	// Assert
	if (event != nil) != isOnePath {
		t.Fatalf("event = %+v, want one raised: %t, as the file system opens g1.meta by G1.meta: %t", event, isOnePath, isOnePath)
	}
}

// A poll's state folder is the one named when it is that folder spelled in
// another case exactly where the file system opens the one by the other.
func TestSamePollPathComparesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, t.TempDir(), "state")

	// Act
	isSame := samePollPath(lower, upper)

	// Assert
	if isSame != isOnePath {
		t.Fatalf("samePollPath(%s, %s) = %t, want %t", lower, upper, isSame, isOnePath)
	}
}

// A native Codex goblin's rollouts are those bound to its worktree: one bound
// to the worktree spelled in another case is its own exactly where the file
// system opens the one by the other.
func TestNativeCodexRolloutsAreBoundToTheWorktreeByTheFileSystemsCase(t *testing.T) {
	// Arrange
	worktree, upper, isOnePath := caseSpellings(t, t.TempDir(), "repo")
	home := t.TempDir()
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T17-34-25-session-1.jsonl")
	transcriptLines(t, rollout, time.Now(), map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": upper}})
	progress := &HostProgress{Home: home}

	// Act
	rollouts := progress.nativeCodexRollouts(context.Background(), worktree, time.Time{})

	// Assert
	if isBound := slices.Contains(rollouts, rollout); isBound != isOnePath || len(rollouts) > 1 {
		t.Fatalf("nativeCodexRollouts(%s) = %q, want %s among them: %t", worktree, rollouts, rollout, isOnePath)
	}
}
