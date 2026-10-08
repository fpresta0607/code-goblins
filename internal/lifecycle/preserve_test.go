package lifecycle

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type preservationRunner struct {
	root, status, branch, head, remote string
	pushFails                          bool
	pushed                             bool
}

func (runner *preservationRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	var output string
	switch strings.Join(request.Args, " ") {
	case "rev-parse --show-toplevel":
		output = runner.root
	case "status --porcelain=v1 --untracked-files=all":
		output = runner.status
	case "symbolic-ref --quiet --short HEAD":
		if runner.branch == "" {
			return execx.Result{ExitCode: 1}, nil
		}
		output = runner.branch
	case "rev-parse HEAD":
		output = runner.head
	case "ls-remote --exit-code origin refs/heads/" + runner.branch:
		if runner.remote == "" {
			return execx.Result{ExitCode: 2}, nil
		}
		output = runner.remote + "\trefs/heads/" + runner.branch
	case "push origin HEAD:refs/heads/" + runner.branch:
		runner.pushed = true
		if runner.pushFails {
			return execx.Result{ExitCode: 1, Stderr: []byte("remote unavailable")}, nil
		}
		runner.remote = runner.head
	default:
		return execx.Result{}, fmt.Errorf("unexpected command %s %v", request.Name, request.Args)
	}
	return execx.Result{Stdout: []byte(output)}, nil
}

func TestPreserveWorkKeepsEverythingNotProvenPushed(t *testing.T) {
	for _, test := range []struct {
		name, status, branch, mode, remote string
		pushFails, wantRemoval, wantPush   bool
	}{
		{name: "dirty worktree", status: " M file.go", branch: "feat/work", mode: "direct-PR"},
		{name: "untracked work", status: "?? notes.txt", branch: "feat/work", mode: "direct-PR"},
		{name: "detached unpushed work", mode: "direct-PR"},
		{name: "local only branch", branch: "feat/work", mode: "local-only"},
		{name: "gate owned work", branch: "feat/work", mode: "no-mistakes"},
		{name: "pushed branch", branch: "feat/work", mode: "direct-PR", remote: "abc123", wantRemoval: true},
		{name: "push before removing", branch: "feat/work", mode: "direct-PR", wantRemoval: true, wantPush: true},
		{name: "push fails", branch: "feat/work", mode: "direct-PR", pushFails: true, wantPush: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "gb-work")
			runner := &preservationRunner{root: root, status: test.status, branch: test.branch, head: "abc123", remote: test.remote, pushFails: test.pushFails}
			result, err := PreserveWork(context.Background(), runner, state.TaskMeta{ID: "work", Worktree: root, Mode: test.mode}, "")
			if err != nil {
				t.Fatal(err)
			}
			if result.CanRemove != test.wantRemoval || runner.pushed != test.wantPush {
				t.Fatalf("preservation=%+v pushed=%v; want removal=%v push=%v", result, runner.pushed, test.wantRemoval, test.wantPush)
			}
			if len(result.Kept) == 0 {
				t.Fatal("preserved work was not reported")
			}
			if !result.CanRemove && !strings.Contains(strings.Join(result.Kept, " "), root) {
				t.Fatalf("kept worktree not named: %+v", result)
			}
		})
	}
}

func TestPreserveWorkRefusesAnEnclosingCheckout(t *testing.T) {
	root := t.TempDir()
	runner := &preservationRunner{root: root}
	_, err := PreserveWork(context.Background(), runner, state.TaskMeta{ID: "work", Worktree: filepath.Join(root, ".worktrees", "gb-work")}, "")
	if err == nil {
		t.Fatal("an empty worktree directory was treated as its enclosing checkout")
	}
}

// gitAt runs git in dir.
func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A helper's work is kept by its parent's branch once its parent merged it,
// so stopping the helper then lets its worktree go; until then it is local
// work never pushed, and its worktree is kept.
func TestPreserveWorkKeepsAHelpersWorktreeUntilItsParentsBranchHoldsIt(t *testing.T) {
	// Arrange
	root, err := fsx.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, parent, helper := filepath.Join(root, "app"), filepath.Join(root, "g1"), filepath.Join(root, "g1-h1")
	gitAt(t, root, "init", "-q", "--initial-branch=main", project)
	gitAt(t, project, "commit", "-q", "--allow-empty", "-m", "seed")
	gitAt(t, project, "worktree", "add", "-q", "-b", "feat/x", parent)
	gitAt(t, project, "worktree", "add", "-q", "-b", "feat/x-h1", helper, "feat/x")
	gitAt(t, helper, "commit", "-q", "--allow-empty", "-m", "helper work")
	meta := state.TaskMeta{ID: "g1-h1", Parent: "g1", Mode: "local-only", Worktree: helper}

	// Act
	before, beforeErr := PreserveWork(context.Background(), execx.OSRunner{}, meta, parent)
	gitAt(t, parent, "merge", "-q", "--no-ff", "-m", "merge helper", "feat/x-h1")
	after, afterErr := PreserveWork(context.Background(), execx.OSRunner{}, meta, parent)

	// Assert
	if beforeErr != nil || before.CanRemove {
		t.Errorf("before the merge = %+v, %v; want the worktree kept", before, beforeErr)
	}
	if afterErr != nil || !after.CanRemove || !strings.Contains(strings.Join(after.Kept, " "), "which its parent g1's branch holds") {
		t.Errorf("after the merge = %+v, %v; want the work kept by g1's branch", after, afterErr)
	}
}
