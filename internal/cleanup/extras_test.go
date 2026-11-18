package cleanup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// realGit runs git in dir and returns its trimmed output.
func realGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// homeTask is a task spawned into a home: a project cloned from a bare
// origin, its worktree and two extra worktrees under the home's worktrees
// folder, all made by the worktree service, and a scratch folder holding a
// build's leftovers, recorded on a native task whose terminal has ended.
type homeTask struct {
	service  Service
	stateDir string
	project  string
	worktree string
	extras   []string
	scratch  string
}

func newHomeTask(t *testing.T) homeTask {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	realGit(t, root, "init", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	realGit(t, root, "clone", "-q", origin, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, seed, "add", ".")
	realGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	realGit(t, seed, "push", "-q", "origin", "HEAD:main")
	projectPath := filepath.Join(root, "app")
	realGit(t, root, "clone", "-q", origin, projectPath)
	realGit(t, projectPath, "config", "user.email", "t@t")
	realGit(t, projectPath, "config", "user.name", "t")
	project, err := fsx.Canonical(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	commands := execx.OSRunner{}
	worktrees := worktree.Service{Commands: commands, Root: filepath.Join(home, "worktrees")}
	ctx := context.Background()
	own, err := worktrees.Acquire(ctx, project, "g1", "")
	if err != nil {
		t.Fatal(err)
	}
	var extras []string
	for _, name := range []string{"proof", "ci"} {
		extra, err := worktrees.AddExtra(ctx, project, "g1", name, "")
		if err != nil {
			t.Fatal(err)
		}
		extras = append(extras, extra.Path)
	}
	scratch := filepath.Join(home, "scratch", "g1")
	if err := os.MkdirAll(filepath.Join(scratch, "go-build123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "test.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "g1", Window: "native", Worktree: own.Path, Project: project, Harness: "claude", Backend: "native", Scratch: scratch, Extras: extras}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return homeTask{
		service: Service{
			StateDir:  stateDir,
			Commands:  commands,
			Terminal:  &herdr.Client{Commands: commands, Session: "fleet"},
			Worktrees: worktrees,
		},
		stateDir: stateDir,
		project:  project,
		worktree: own.Path,
		extras:   extras,
		scratch:  scratch,
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestCleanupRemovesTheWorktreeEveryRecordedExtraAndTheScratch(t *testing.T) {
	// Arrange: the proof extra holds a commit that is on no branch at all.
	task := newHomeTask(t)
	proof := task.extras[0]
	realGit(t, proof, "commit", "-q", "--allow-empty", "-m", "proof")
	proofHead := realGit(t, proof, "rev-parse", "HEAD")

	// Act
	result, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	for _, path := range append([]string{task.worktree, task.scratch}, task.extras...) {
		if exists(t, path) {
			t.Errorf("%s survived the cleanup", path)
		}
	}
	if listed := realGit(t, task.project, "worktree", "list"); strings.Count(listed, "\n") != 0 {
		t.Errorf("the project still registers worktrees:\n%s", listed)
	}
	if got := realGit(t, task.project, "rev-parse", "refs/tags/archive/g1-proof^{commit}"); got != proofHead {
		t.Errorf("archive/g1-proof = %s, want the proof extra's unlanded commit %s", got, proofHead)
	}
	if !strings.Contains(result.Output, "archive/g1-proof") {
		t.Errorf("Output = %q, want the archive tag named", result.Output)
	}
	if tags, err := state.ArchiveTags(task.stateDir); err != nil || len(tags) != 1 || !strings.EqualFold(tags[0].Checkout, task.project) || tags[0].Tag != "archive/g1-proof" {
		t.Errorf("the home records the archive tags %v (%v), want archive/g1-proof in %s, which an uninstall keeps and removes", tags, err, task.project)
	}
	if _, err := state.ReadTaskMeta(task.stateDir, "g1"); err == nil {
		t.Error("the task record survived the cleanup")
	}
}

func TestCleanupReturnsAnExtraOfAnotherProjectToThatProjectKeepingItsWork(t *testing.T) {
	// Arrange: the task records an extra worktree of a second project, which
	// holds a commit that is on no branch.
	task := newHomeTask(t)
	root := filepath.Dir(filepath.Dir(task.stateDir))
	origin := filepath.Join(root, "tool.git")
	realGit(t, root, "init", "--bare", "--initial-branch=main", origin)
	otherPath := filepath.Join(root, "tool")
	realGit(t, root, "clone", "-q", origin, otherPath)
	realGit(t, otherPath, "config", "user.email", "t@t")
	realGit(t, otherPath, "config", "user.name", "t")
	realGit(t, otherPath, "commit", "-q", "--allow-empty", "-m", "seed")
	realGit(t, otherPath, "push", "-q", "origin", "HEAD:main")
	other, err := fsx.Canonical(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := task.service.Worktrees.AddExtra(context.Background(), other, "g1", "release", "")
	if err != nil {
		t.Fatal(err)
	}
	realGit(t, extra.Path, "commit", "-q", "--allow-empty", "-m", "release")
	releaseHead := realGit(t, extra.Path, "rev-parse", "HEAD")
	meta, err := state.ReadTaskMeta(task.stateDir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Extras = append(meta.Extras, extra.Path)
	if err := state.WriteTaskMeta(task.stateDir, meta); err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if exists(t, extra.Path) {
		t.Errorf("%s survived the cleanup", extra.Path)
	}
	if listed := realGit(t, other, "worktree", "list"); strings.Count(listed, "\n") != 0 {
		t.Errorf("the other project still registers worktrees:\n%s", listed)
	}
	if got := realGit(t, other, "rev-parse", "refs/tags/archive/g1-release^{commit}"); got != releaseHead {
		t.Errorf("archive/g1-release = %s, want the extra's unlanded commit %s", got, releaseHead)
	}
	if !strings.Contains(result.Output, "archive/g1-release") {
		t.Errorf("Output = %q, want the archive tag named", result.Output)
	}
	tags, err := state.ArchiveTags(task.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(tags, func(tag state.ArchiveTag) bool {
		return tag.Tag == "archive/g1-release" && strings.EqualFold(tag.Checkout, other)
	}) {
		t.Errorf("the home records the archive tags %v, want archive/g1-release in %s, which an uninstall keeps and removes", tags, other)
	}
}

func TestCleanupRefusesAndChangesNothingWhileAnExtraHoldsUncommittedWork(t *testing.T) {
	// Arrange
	task := newHomeTask(t)
	if err := os.WriteFile(filepath.Join(task.extras[1], "draft.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "uncommitted") || !strings.Contains(err.Error(), filepath.Base(task.extras[1])) {
		t.Fatalf("Cleanup = %v, want a refusal naming the extra with uncommitted work", err)
	}
	for _, path := range append([]string{task.worktree, task.scratch, filepath.Join(task.scratch, "test.log")}, task.extras...) {
		if !exists(t, path) {
			t.Errorf("%s is gone after a refused cleanup", path)
		}
	}
	if _, err := state.ReadTaskMeta(task.stateDir, "g1"); err != nil {
		t.Errorf("the task record is gone after a refused cleanup: %v", err)
	}
	if tags := realGit(t, task.project, "tag", "--list", "archive/*"); tags != "" {
		t.Errorf("a refused cleanup made tags: %s", tags)
	}
}
