package spawn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// parentRepository is a project with one commit and a parent goblin's
// worktree beside it on branch feat/x, one commit ahead of the project, and
// returns both.
func parentRepository(t *testing.T) (project, parentWorktree string) {
	t.Helper()
	root := makeDir(t, t.TempDir())
	project = filepath.Join(root, "app")
	gitIn(t, root, "init", "-q", "--initial-branch=main", project)
	gitIn(t, project, "config", "user.email", "t@t")
	gitIn(t, project, "config", "user.name", "t")
	writeFile(t, filepath.Join(project, "README.md"), "seed\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-q", "-m", "seed")
	parentWorktree = filepath.Join(root, "worktrees", "app", "g1")
	gitIn(t, project, "worktree", "add", "-q", "-b", "feat/x", parentWorktree)
	writeFile(t, filepath.Join(parentWorktree, "parent.txt"), "the parent's work\n")
	gitIn(t, parentWorktree, "add", ".")
	gitIn(t, parentWorktree, "commit", "-q", "-m", "parent work")
	return project, parentWorktree
}

func writeParent(t *testing.T, stateDir string, meta state.TaskMeta) {
	t.Helper()
	meta.Window, meta.Harness, meta.Kind, meta.Backend = "native", "claude", "ship", "native"
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
}

func TestHelperStartCutsTheHelpersBranchFromItsParentsBranchAndLastCommit(t *testing.T) {
	// Arrange
	project, parentWorktree := parentRepository(t)
	stateDir := t.TempDir()
	writeParent(t, stateDir, state.TaskMeta{ID: "g1", Project: project, Worktree: parentWorktree})
	service := Service{StateDir: stateDir, Commands: execx.OSRunner{}}

	// Act
	base, err := service.helperStart(context.Background(), Request{ID: "g1-h1", Parent: "g1"}, project)

	// Assert
	if err != nil {
		t.Fatalf("helperStart: %v", err)
	}
	if base.branch != "feat/x-h1" || base.parentBranch != "feat/x" {
		t.Errorf("branch = %q off %q, want feat/x-h1 off feat/x", base.branch, base.parentBranch)
	}
	if want := gitIn(t, parentWorktree, "rev-parse", "HEAD"); base.head != want {
		t.Errorf("head = %q, want the parent's last commit %q", base.head, want)
	}
}

func TestHelperStartRefusesWhatCannotHaveAHelper(t *testing.T) {
	project, parentWorktree := parentRepository(t)
	detached := filepath.Join(filepath.Dir(parentWorktree), "g2")
	gitIn(t, project, "worktree", "add", "-q", "--detach", detached)
	other := makeDir(t, t.TempDir())
	cases := []struct {
		name    string
		request Request
		project string
		want    string
	}{
		{"a helper of a helper", Request{ID: "g1-h1-h1", Parent: "g1-h1"}, project, "a helper cannot start helpers"},
		{"a second helper", Request{ID: "g1-h2", Parent: "g1"}, project, "already has a helper, g1-h1"},
		{"a parent on no branch", Request{ID: "g2-h1", Parent: "g2"}, project, "on no branch"},
		{"another project", Request{ID: "g3-h1", Parent: "g3"}, other, "its parent's project"},
		{"a name that is not the parent's", Request{ID: "x-h1", Parent: "g3"}, project, "g3-<name>"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			writeParent(t, stateDir, state.TaskMeta{ID: "g1", Project: project, Worktree: parentWorktree})
			writeParent(t, stateDir, state.TaskMeta{ID: "g1-h1", Project: project, Worktree: parentWorktree + "-h1", Parent: "g1"})
			writeParent(t, stateDir, state.TaskMeta{ID: "g2", Project: project, Worktree: detached})
			writeParent(t, stateDir, state.TaskMeta{ID: "g3", Project: project, Worktree: parentWorktree})
			service := Service{StateDir: stateDir, Commands: execx.OSRunner{}}

			// Act
			_, err := service.helperStart(context.Background(), test.request, test.project)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("helperStart = %v, want a refusal naming %q", err, test.want)
			}
		})
	}
}

func TestSpawnRefusesAHelperOutsideLocalOnlyBeforeAnyMutation(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.request.Parent, f.request.ID, f.request.Mode = "g1", "g1-h1", "direct-PR"
	writeFile(t, f.brief, "Do the work.\n")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "local-only") {
		t.Errorf("Spawn = %v, want a helper refused outside local-only", err)
	}
	if slices.Contains(f.events, "worktree-acquire") {
		t.Errorf("events = %v, want nothing acquired", f.events)
	}
}

func TestSpawnRefusesASecondHelperUnderTheSpawnLockBeforeAnyMutation(t *testing.T) {
	// Arrange
	f := newFixture(t)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g1", Project: f.project, Worktree: f.worktree})
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g1-h1", Project: f.project, Worktree: f.worktree + "-h1", Parent: "g1"})
	f.request.Parent, f.request.ID, f.request.Mode = "g1", "g1-h2", "local-only"
	writeFile(t, f.brief, "Do the work.\n")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "already has a helper") {
		t.Errorf("Spawn = %v, want the second helper refused", err)
	}
	if slices.Contains(f.events, "worktree-acquire") {
		t.Errorf("events = %v, want nothing acquired", f.events)
	}
	if _, statErr := os.Stat(state.TaskMetaPath(f.stateDir, "g1-h2")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the refused helper's record: stat = %v, want none", statErr)
	}
}

func TestNotifyInstructionTellsAHelperToReportToItsParentAndNeverPush(t *testing.T) {
	helper := notifyInstruction(state.TaskMeta{ID: "g1-h1", Kind: "ship", Parent: "g1"})
	goblin := notifyInstruction(state.TaskMeta{ID: "g1", Kind: "ship"})

	for _, want := range []string{"helper goblin of g1", "notify g1-h1 --done", "never push", "g1 merges your branch", "cannot start helpers"} {
		if !strings.Contains(helper, want) {
			t.Errorf("helper instruction lacks %q:\n%s", want, helper)
		}
	}
	for _, unwanted := range []string{"--pr <url>", "helper start"} {
		if strings.Contains(helper, unwanted) {
			t.Errorf("helper instruction offers %q:\n%s", unwanted, helper)
		}
	}
	for _, want := range []string{"helper start g1 --brief", "helper merge g1", "--waiting-on <helper-id>", "--pr <url>"} {
		if !strings.Contains(goblin, want) {
			t.Errorf("goblin instruction lacks %q:\n%s", want, goblin)
		}
	}
	if scout := notifyInstruction(state.TaskMeta{ID: "s1", Kind: "scout"}); strings.Contains(scout, "helper start") {
		t.Errorf("a scout is offered helpers:\n%s", scout)
	}
}
