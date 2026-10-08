package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestHelperStartHandsTheBriefsTextToTheSupervisor(t *testing.T) {
	// Arrange
	runtime := testCommandRuntime(t)
	var asked supervisor.HelperRequest
	runtime.requestHelper = func(_ home.Home, request supervisor.HelperRequest) (supervisor.HelperStart, error) {
		asked = request
		return supervisor.HelperStart{ID: "g1-h1", Branch: "feat/x-h1"}, nil
	}
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"helper", "start", "g1", "--brief", brief, "--title", "  Accounts   migration "}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if asked != (supervisor.HelperRequest{Parent: "g1", Brief: "Write the migration.\n", Title: "Accounts migration"}) {
		t.Errorf("asked %+v, want g1's brief and its title", asked)
	}
	for _, want := range []string{"starting helper g1-h1 on branch feat/x-h1", "--waiting-on g1-h1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q does not say %q", stdout.String(), want)
		}
	}
}

func TestHelperStartSaysWhyTheSupervisorRefused(t *testing.T) {
	// Arrange
	runtime := testCommandRuntime(t)
	runtime.requestHelper = func(home.Home, supervisor.HelperRequest) (supervisor.HelperStart, error) {
		return supervisor.HelperStart{}, errors.New("Only 3.1 GB of memory is free; ask again in ten minutes")
	}
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"helper", "start", "g1", "--brief", brief}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "not started: Only 3.1 GB of memory is free; ask again in ten minutes") {
		t.Errorf("exit = %d, stderr = %q; want the refusal and when to ask again", exit, stderr.String())
	}
}

func TestHelperStartRefusesWhatItCannotSend(t *testing.T) {
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		exit int
		want string
	}{
		{"no task", []string{"helper", "start"}, 2, "your task ID comes first"},
		{"no brief", []string{"helper", "start", "g1"}, 2, "--brief <file> is required"},
		{"a title on two lines", []string{"helper", "start", "g1", "--brief", brief, "--title", "two\nlines"}, 2, "a title is one line"},
		{"a brief that is not there", []string{"helper", "start", "g1", "--brief", brief + ".missing"}, 1, "helper.md.missing"},
		{"an unknown subcommand", []string{"helper", "begin"}, 2, `unknown subcommand "begin"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			runtime := testCommandRuntime(t)
			isAsked := false
			runtime.requestHelper = func(home.Home, supervisor.HelperRequest) (supervisor.HelperStart, error) {
				isAsked = true
				return supervisor.HelperStart{}, nil
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(c.args, &stdout, &stderr, runtime)

			// Assert
			if exit != c.exit || !strings.Contains(stderr.String(), c.want) || isAsked {
				t.Errorf("exit = %d, stderr = %q, asked = %t; want exit %d naming %q and nothing asked", exit, stderr.String(), isAsked, c.exit, c.want)
			}
		})
	}
}

// mergeFixture is a home where goblin g1 works on feat/x and its helper g1-h1
// committed helper.txt on feat/x-h1, in a real repository, with a lifecycle
// that records the stop it is asked for.
type mergeFixture struct {
	home           home.Home
	parent, helper string
	runtime        commandRuntime
	retired        []lifecycle.Request
}

func newMergeFixture(t *testing.T) *mergeFixture {
	t.Helper()
	f := &mergeFixture{home: testHome(t)}
	if err := os.MkdirAll(f.home.State, 0o700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(f.home.Root, "app")
	f.parent, f.helper = filepath.Join(f.home.Root, "worktrees", "app", "g1"), filepath.Join(f.home.Root, "worktrees", "app", "g1-h1")
	gitIn(t, f.home.Root, "init", "-q", "--initial-branch=main", project)
	gitIn(t, project, "config", "user.email", "t@t")
	gitIn(t, project, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-q", "-m", "seed")
	gitIn(t, project, "worktree", "add", "-q", "-b", "feat/x", f.parent)
	gitIn(t, project, "worktree", "add", "-q", "-b", "feat/x-h1", f.helper, "feat/x")
	if err := os.WriteFile(filepath.Join(f.helper, "helper.txt"), []byte("the helper's part\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.helper, "add", ".")
	gitIn(t, f.helper, "commit", "-q", "-m", "the helper's part")
	for _, meta := range []state.TaskMeta{
		{ID: "g1", SpawnGen: "s1", Worktree: f.parent},
		{ID: "g1-h1", SpawnGen: "s2", Parent: "g1", Mode: "local-only", Title: "Accounts migration", Worktree: f.helper},
	} {
		meta.Window, meta.Harness, meta.Kind, meta.Backend, meta.Project = "native", "claude", "ship", "native", project
		if err := state.WriteTaskMeta(f.home.State, meta); err != nil {
			t.Fatal(err)
		}
	}
	f.runtime = testCommandRuntimeForHome(f.home)
	f.runtime.taskLifecycle = func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
		f.retired = append(f.retired, request)
		return state.Lifecycle{Phase: "stopped", Kept: []string{"branch feat/x-h1, which its parent g1's branch holds"}}, nil
	}
	return f
}

func (f *mergeFixture) merge() (int, string, string) {
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"helper", "merge", "g1"}, &stdout, &stderr, f.runtime)
	return exit, stdout.String(), stderr.String()
}

func TestHelperMergeMergesTheHelpersBranchWithAMergeCommitAndRetiresIt(t *testing.T) {
	// Arrange
	f := newMergeFixture(t)
	helperHead := gitIn(t, f.helper, "rev-parse", "HEAD")

	// Act
	exit, stdout, stderr := f.merge()

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr)
	}
	parents := strings.Fields(gitIn(t, f.parent, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 || parents[2] != helperHead {
		t.Errorf("the parent's HEAD has parents %v, want a merge commit whose second parent is the helper's %s", parents[1:], helperHead)
	}
	if branch := gitIn(t, f.parent, "symbolic-ref", "--short", "HEAD"); branch != "feat/x" {
		t.Errorf("the parent is on %s, want its own branch feat/x", branch)
	}
	if subject := gitIn(t, f.parent, "log", "-1", "--format=%s"); !strings.Contains(subject, "g1-h1") || !strings.Contains(subject, "Accounts migration") {
		t.Errorf("merge subject %q, want it to name the helper and its title", subject)
	}
	if len(f.retired) != 1 || f.retired[0].ID != "g1-h1" || f.retired[0].Generation != "s2" || f.retired[0].Action != "stop" || !strings.Contains(f.retired[0].Reason, "Merged into its parent g1's branch feat/x") {
		t.Errorf("retired %+v, want g1-h1 stopped as merged", f.retired)
	}
	if !strings.Contains(stdout, "merged g1-h1") || !strings.Contains(stdout, "retired g1-h1") {
		t.Errorf("stdout %q, want the merge and the retirement said", stdout)
	}
}

func TestHelperMergeLeavesAConflictForTheParentAndRetiresOnceItIsCommitted(t *testing.T) {
	// Arrange: the parent and its helper both changed README.md.
	f := newMergeFixture(t)
	for dir, text := range map[string]string{f.parent: "the parent's\n", f.helper: "the helper's\n"} {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "commit", "-q", "-am", "edit README")
	}

	// Act
	exit, _, stderr := f.merge()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "conflicts in README.md") || !strings.Contains(stderr, "cfo helper merge g1 again") || len(f.retired) != 0 {
		t.Fatalf("exit = %d, stderr = %q, retired %+v; want the conflict left to the parent", exit, stderr, f.retired)
	}

	// Act: the parent resolves and commits the merge, then asks again.
	if err := os.WriteFile(filepath.Join(f.parent, "README.md"), []byte("both\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.parent, "commit", "-q", "-am", "merge the helper")
	exit, stdout, stderr := f.merge()

	// Assert
	if exit != 0 || !strings.Contains(stdout, "already holds g1-h1's work") || len(f.retired) != 1 {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, retired %+v; want only the retirement", exit, stdout, stderr, f.retired)
	}
}

func TestHelperMergeRefusesUncommittedWorkOnEitherSide(t *testing.T) {
	for _, side := range []string{"parent", "helper"} {
		t.Run(side, func(t *testing.T) {
			// Arrange
			f := newMergeFixture(t)
			dir := map[string]string{"parent": f.parent, "helper": f.helper}[side]
			if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("unsaved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := gitIn(t, f.parent, "rev-parse", "HEAD")

			// Act
			exit, _, stderr := f.merge()

			// Assert
			if exit != 1 || !strings.Contains(stderr, "uncommitted") || len(f.retired) != 0 || gitIn(t, f.parent, "rev-parse", "HEAD") != before {
				t.Errorf("exit = %d, stderr = %q, retired %+v; want nothing merged or retired", exit, stderr, f.retired)
			}
		})
	}
}

func TestHelperMergeSaysWhenThereIsNoHelper(t *testing.T) {
	f := newMergeFixture(t)
	if err := state.RemoveTaskMeta(f.home.State, "g1-h1"); err != nil {
		t.Fatal(err)
	}

	exit, _, stderr := f.merge()

	if exit != 1 || !strings.Contains(stderr, "g1 has no live helper") {
		t.Errorf("exit = %d, stderr = %q; want no helper named", exit, stderr)
	}
}
