package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// caseSpellings makes a folder named name in parent and returns it, its
// name in capitals beside it, and whether the file system opens the one by
// the other.
func caseSpellings(t *testing.T, parent, name string) (lower, upper string, isOnePath bool) {
	t.Helper()
	lower, upper = filepath.Join(parent, name), filepath.Join(parent, strings.ToUpper(name))
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// A folder inside a worktree spelled in another case is inside it exactly
// where the file system opens the worktree by that spelling.
func TestInWorktreeComparesFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	worktree, upper, isOnePath := caseSpellings(t, t.TempDir(), "run")
	inside := filepath.Join(upper, "pkg")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	isInside := inWorktree(inside, []string{worktree})

	// Assert
	if isInside != isOnePath {
		t.Fatalf("inWorktree(%s, %s) = %t, want %t", inside, worktree, isInside, isOnePath)
	}
}

// A run in a folder named worktrees in another case has the gate's shape
// exactly where the file system opens that folder as worktrees, and is the
// task the environment names elsewhere.
func TestGateTaskNamesTheWorktreesFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	root := filepath.Join(parent, "WORKTREES", "repo", "run")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(parent, "worktrees"))
	isOnePath := err == nil
	t.Setenv("NO_MISTAKES_GATE", "")
	t.Setenv("GOBLINS_TASK_ID", "env-task")
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return home.Home{}, errors.New("asked for the home") }}

	// Act
	id, err := gateTask(root, runtime)

	// Assert
	if isGateShaped := err != nil && strings.Contains(err.Error(), "asked for the home"); isGateShaped != isOnePath || !isOnePath && id != "env-task" {
		t.Fatalf("gateTask(%s) = %q, %v; want the gate's shape: %t", root, id, err, isOnePath)
	}
}

// A project an issue names in another case than its checkout's folder is
// that checkout, whose repository is public, exactly where the file system
// opens the folder by that spelling; elsewhere it is the private project of
// that name in the projects root.
func TestIssueScreenNamesTheCheckoutsProjectByTheFileSystemsCase(t *testing.T) {
	// Arrange
	gh := &issueGH{private: map[string]bool{"acme/WIDGETS": true}}
	runtime, h := issueRuntime(t, gh)
	checkout, _, isOnePath := caseSpellings(t, t.TempDir(), "widgets")
	projects := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projects, "WIDGETS", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime.projectsRoot = func() (string, error) { return projects, nil }
	writeIssueManifest(t, h, checkout, `{"project":"widgets"}`)

	// Act
	_, err := fileRoot(context.Background(), runtime, checkout, tickets.Root{Title: "a root", Body: "Seen in WIDGETS."}, time.Now())

	// Assert
	if isFiled := err == nil; isFiled != isOnePath {
		t.Fatalf("fileRoot = %v, writes = %v; want filed as the checkout's own project: %t", err, gh.writes, isOnePath)
	}
	if !isOnePath && !strings.Contains(err.Error(), "it names WIDGETS") {
		t.Fatalf("fileRoot = %v, want a refusal naming the private WIDGETS", err)
	}
}

// A parent's wait on its helper spelled in another case keeps the helper at
// work exactly where the file system opens the helper's id by that spelling
// in the state folder.
func TestReachHelpersNamesTheAwaitedHelperByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1-h1", SpawnGen: "s2", Parent: "g1", Window: "native", Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(h.State, "G1-H1.meta"))
	isOnePath := err == nil
	record := state.Lifecycle{Operation: "op-1", Action: "pause", Pause: &state.PauseCondition{Reason: "dependency", Until: "task:G1-H1"}}
	var ran []lifecycle.Request
	run := func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
		ran = append(ran, request)
		return state.Lifecycle{Phase: "paused"}, nil
	}

	// Act
	lines, err := reachHelpers(context.Background(), h, state.TaskMeta{ID: "g1"}, &record, run)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isKept := slices.Equal(lines, []string{"helper g1-h1 kept at work: g1 waits on it"}) && len(ran) == 0; isKept != isOnePath {
		t.Fatalf("lines = %q, ran %+v; want g1-h1 kept at work: %t", lines, ran, isOnePath)
	}
}

// The home's bin, spelled in another case, is its own build's folder and
// holds its programs exactly where the file system opens the bin by that
// spelling.
func TestHomePathsNameTheBinByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := testHome(t)
	_, upper, isOnePath := caseSpellings(t, h.Root, filepath.Base(h.Bin()))

	// Act
	folder := journalFolder(h, update.Journal{Root: upper})
	isHomes := homeProgram(h, filepath.Join(upper, "goblins.exe"))

	// Assert
	if isNamed := folder == h.Bin(); isNamed != isOnePath {
		t.Errorf("journalFolder for a journal of %s = %q, want the bin %s: %t", upper, folder, h.Bin(), isOnePath)
	}
	if isHomes != isOnePath {
		t.Errorf("homeProgram(%s) = %t, want %t", filepath.Join(upper, "goblins.exe"), isHomes, isOnePath)
	}
}
