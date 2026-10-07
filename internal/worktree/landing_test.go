package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// gitRun runs git in dir and returns its trimmed output.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// clonedProject is a checkout of a bare origin whose main holds one commit,
// with origin/HEAD naming main, as a project the fleet works in is.
func clonedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitRun(t, root, "init", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	gitRun(t, root, "clone", "-q", origin, seed)
	gitRun(t, seed, "config", "user.email", "t@t")
	gitRun(t, seed, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-q", "-m", "seed")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:main")
	project := filepath.Join(root, "app")
	gitRun(t, root, "clone", "-q", origin, project)
	gitRun(t, project, "config", "user.email", "t@t")
	gitRun(t, project, "config", "user.name", "t")
	gitRun(t, project, "remote", "set-head", "origin", "--auto")
	return project
}

func TestLandingTellsMergedWorkFromUnlandedWork(t *testing.T) {
	// Arrange: one worktree at origin/main, one with a commit of its own on a
	// branch, one with a commit of its own on a detached HEAD.
	project := clonedProject(t)
	git := RunnerGit{Commands: execx.OSRunner{}}
	ctx := context.Background()
	home := t.TempDir()
	merged := filepath.Join(home, "merged")
	branch := filepath.Join(home, "branch")
	detached := filepath.Join(home, "detached")
	gitRun(t, project, "worktree", "add", "--detach", merged, "origin/main")
	gitRun(t, project, "worktree", "add", "-b", "feat/x", branch, "origin/main")
	gitRun(t, project, "worktree", "add", "--detach", detached, "origin/main")
	for _, dir := range []string{branch, detached} {
		if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", ".")
		gitRun(t, dir, "commit", "-q", "-m", "work")
	}

	// Act and Assert
	cases := []struct {
		dir        string
		wantLanded bool
		wantBranch string
	}{
		{merged, true, ""},
		{branch, false, "feat/x"},
		{detached, false, ""},
	}
	for _, c := range cases {
		landing, err := git.Landing(ctx, c.dir)
		if err != nil {
			t.Fatalf("Landing(%s): %v", c.dir, err)
		}
		if landing.Landed != c.wantLanded || landing.Branch != c.wantBranch || landing.Head != gitRun(t, c.dir, "rev-parse", "HEAD") {
			t.Errorf("Landing(%s) = %+v, want landed %v on branch %q at HEAD", filepath.Base(c.dir), landing, c.wantLanded, c.wantBranch)
		}
	}
}

func TestLandingWithoutADefaultBranchReadsAsUnlanded(t *testing.T) {
	project := clonedProject(t)
	gitRun(t, project, "remote", "set-head", "origin", "--delete")

	landing, err := (RunnerGit{Commands: execx.OSRunner{}}).Landing(context.Background(), project)

	if err != nil {
		t.Fatal(err)
	}
	if landing.Landed {
		t.Errorf("Landing = %+v, want unlanded when the default branch cannot be named", landing)
	}
}

func TestArchiveTagKeepsUnlandedWorkReachable(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	git := RunnerGit{Commands: execx.OSRunner{}}
	ctx := context.Background()
	detached := filepath.Join(t.TempDir(), "task-proof")
	gitRun(t, project, "worktree", "add", "--detach", detached, "origin/main")
	if err := os.WriteFile(filepath.Join(detached, "work.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, detached, "add", ".")
	gitRun(t, detached, "commit", "-q", "-m", "work")
	landing, err := git.Landing(ctx, detached)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	tag, err := git.ArchiveTag(ctx, detached, landing, "task-proof")
	again, againErr := git.ArchiveTag(ctx, detached, landing, "task-proof")

	// Assert: the tag holds HEAD, and tagging the same work again is the
	// same archive.
	if err != nil || againErr != nil {
		t.Fatalf("ArchiveTag: %v, %v", err, againErr)
	}
	if tag != "archive/task-proof" || again != tag {
		t.Errorf("tags = %q then %q, want archive/task-proof both times", tag, again)
	}
	if got := gitRun(t, project, "rev-parse", "refs/tags/"+tag+"^{commit}"); got != landing.Head {
		t.Errorf("%s names %s, want HEAD %s", tag, got, landing.Head)
	}

	// A tag of the same name at other work is kept, and this one is named
	// with its commit too.
	gitRun(t, detached, "commit", "-q", "--allow-empty", "-m", "more")
	moved, err := git.Landing(ctx, detached)
	if err != nil {
		t.Fatal(err)
	}
	second, err := git.ArchiveTag(ctx, detached, moved, "task-proof")
	if err != nil {
		t.Fatal(err)
	}
	if second != "archive/task-proof-"+moved.Head[:12] {
		t.Errorf("second tag = %q, want one named with the commit", second)
	}
	if got := gitRun(t, project, "rev-parse", "refs/tags/archive/task-proof^{commit}"); got != landing.Head {
		t.Errorf("the first archive moved to %s, want it kept at %s", got, landing.Head)
	}
}

func TestHoldsHeadTellsAMergedHelperFromAnUnmergedOne(t *testing.T) {
	// Arrange: a parent on feat/x and its helper on feat/x-h1, one commit
	// ahead of it.
	project := clonedProject(t)
	git := RunnerGit{Commands: execx.OSRunner{}}
	home := t.TempDir()
	parent, helper := filepath.Join(home, "g1"), filepath.Join(home, "g1-h1")
	gitRun(t, project, "worktree", "add", "-b", "feat/x", parent, "origin/main")
	gitRun(t, project, "worktree", "add", "-b", "feat/x-h1", helper, "feat/x")
	if err := os.WriteFile(filepath.Join(helper, "helper.txt"), []byte("helped"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, helper, "add", ".")
	gitRun(t, helper, "commit", "-q", "-m", "helper work")

	// Act
	before, beforeErr := git.HoldsHead(context.Background(), parent, helper)
	gitRun(t, parent, "merge", "-q", "--no-ff", "-m", "merge helper", "feat/x-h1")
	after, afterErr := git.HoldsHead(context.Background(), parent, helper)

	// Assert
	if beforeErr != nil || before {
		t.Errorf("before the merge HoldsHead = %t, %v; want false", before, beforeErr)
	}
	if afterErr != nil || !after {
		t.Errorf("after the merge HoldsHead = %t, %v; want true", after, afterErr)
	}
}
