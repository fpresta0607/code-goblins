package supervisor

import (
	"context"
	"errors"
	"slices"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// hostedRunner answers the one hosted read with the JSON a test records, and
// runs everything else for real, so the production gh command, its field list
// and its decoding all run.
type hostedRunner struct {
	inner  execx.Runner
	body   string
	stderr string
	code   int
	calls  []execx.Request
}

func (r *hostedRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "gh" {
		r.calls = append(r.calls, req)
		return execx.Result{Stdout: []byte(r.body), Stderr: []byte(r.stderr), ExitCode: r.code}, nil
	}
	return r.inner.Run(ctx, req)
}

// gitIn runs git in dir for the fixture, without a global configuration.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_CONFIG_GLOBAL="+filepath.Join(os.TempDir(), "no-gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(os.TempDir(), "no-gitconfig"),
	)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stateDir is the task state folder of a fixture root.
func stateDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// forkCheckout is a checkout whose origin is the upstream and whose fork holds
// the base branch, with one commit so git answers.
func forkCheckout(t *testing.T) string {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, worktree, "init", "--quiet", "--initial-branch=fix/daemon")
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, worktree, "add", "-A")
	gitIn(t, worktree, "commit", "--quiet", "-m", "one")
	gitIn(t, worktree, "remote", "add", "origin", "https://github.com/kunchenguid/no-mistakes.git")
	gitIn(t, worktree, "remote", "add", "fork", "https://github.com/fpresta0607/no-mistakes.git")
	// Both bases exist, so a read reaches the branch comparison rather than
	// failing on a missing ref.
	gitIn(t, worktree, "update-ref", "refs/remotes/fork/main", "HEAD")
	gitIn(t, worktree, "update-ref", "refs/remotes/origin/main", "HEAD")
	return worktree
}

// Collection is local: a pull request recorded only in a current-generation
// done status is collected, and no hosted read happens on that pass at all.
func TestCIGoblinsCollectsADoneStatusPullRequestWithoutAHostedRead(t *testing.T) {
	// Arrange
	root := t.TempDir()
	worktree := forkCheckout(t)
	stateDirPath := stateDir(t, root)
	meta := state.TaskMeta{ID: "task", Project: worktree, Worktree: worktree, Backend: "native", SpawnGen: "generation-1"}
	if err := state.WriteTaskMeta(stateDirPath, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDirPath, meta.ID, "done: PR https://github.com/fpresta0607/no-mistakes/pull/24"); err != nil {
		t.Fatal(err)
	}
	hosted := &hostedRunner{inner: execx.OSRunner{}, body: `{"baseRefName":"main","headRefName":"fix/daemon"}`}

	// Act
	goblins := ciGoblins(context.Background(), hosted, stateDirPath)

	// Assert
	if len(goblins) != 1 {
		t.Fatalf("goblins = %d, want the one live goblin", len(goblins))
	}
	if len(goblins[0].pullRequests) != 1 || goblins[0].pullRequests[0] != "https://github.com/fpresta0607/no-mistakes/pull/24" {
		t.Fatalf("pull requests = %v, want the done-status one", goblins[0].pullRequests)
	}
	if len(hosted.calls) != 0 {
		t.Fatalf("the collection pass asked GitHub %d times, want none: %v", len(hosted.calls), hosted.calls)
	}
	if goblins[0].target != "" {
		t.Fatalf("target = %q, want it resolved only in the overlap path", goblins[0].target)
	}
}

// The overlap path reads the pull request's base with the supported field list
// and resolves it to the fork, and an unreadable or malformed answer is an
// incomplete read rather than another base.
func TestResolveTargetReadsTheBaseThroughTheOverlapPath(t *testing.T) {
	for _, c := range []struct {
		name       string
		body       string
		code       int
		wantTarget string
		wantUnread bool
		wantFields string
	}{
		{name: "a fork-origin base", body: `{"baseRefName":"main","headRefName":"fix/daemon"}`, wantTarget: "fork/main", wantFields: "baseRefName,headRefName"},
		{name: "malformed output", body: `{"baseRefName":`, wantUnread: true},
		{name: "a refused read", body: ``, code: 1, wantUnread: true},
		{name: "an answer naming no base", body: `{"baseRefName":"","headRefName":"fix/daemon"}`, wantUnread: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			worktree := forkCheckout(t)
			hosted := &hostedRunner{inner: execx.OSRunner{}, body: c.body, code: c.code}
			goblin := ciGoblin{id: "task", repo: worktree, branch: "fix/daemon", pullRequests: []string{"https://github.com/fpresta0607/no-mistakes/pull/24"}, meta: state.TaskMeta{ID: "task", Worktree: worktree}}

			// Act
			resolved := resolveTarget(context.Background(), hosted, goblin)

			// Assert
			if resolved.target != c.wantTarget {
				t.Fatalf("target = %q, want %q", resolved.target, c.wantTarget)
			}
			if (resolved.targetUnread != "") != c.wantUnread {
				t.Fatalf("targetUnread = %q, want unread=%t", resolved.targetUnread, c.wantUnread)
			}
			if len(hosted.calls) != 1 {
				t.Fatalf("hosted reads = %d, want one", len(hosted.calls))
			}
			args := strings.Join(hosted.calls[0].Args, " ")
			if c.wantFields != "" && !strings.Contains(args, "--json "+c.wantFields) {
				t.Fatalf("gh args = %q, want the supported field list %q", args, c.wantFields)
			}
			if strings.Contains(args, "baseRepository") {
				t.Fatalf("gh args = %q, want no unsupported field", args)
			}
		})
	}
}

// A goblin with no pull request asks GitHub nothing, and a pull request of
// another branch is not this branch's base.
func TestResolveTargetLeavesAnOrdinaryGoblinAlone(t *testing.T) {
	worktree := forkCheckout(t)
	for _, c := range []struct {
		name         string
		pullRequests []string
		body         string
	}{
		{name: "no pull request"},
		{name: "a pull request of another branch", pullRequests: []string{"https://github.com/fpresta0607/no-mistakes/pull/24"}, body: `{"baseRefName":"main","headRefName":"another/branch"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hosted := &hostedRunner{inner: execx.OSRunner{}, body: c.body}
			goblin := ciGoblin{id: "task", repo: worktree, branch: "fix/daemon", pullRequests: c.pullRequests, meta: state.TaskMeta{ID: "task", Worktree: worktree}}
			resolved := resolveTarget(context.Background(), hosted, goblin)
			if resolved.target != "" || resolved.targetUnread != "" {
				t.Fatalf("target = %q unread = %q, want nothing resolved", resolved.target, resolved.targetUnread)
			}
		})
	}
}

// A recorded GitHub backoff holds the base read: the guarded runner refuses it
// before it reaches gh, and a refusal it does meet records the backoff.
func TestTheBaseReadRespectsAndRecordsGitHubBackoff(t *testing.T) {
	worktree := forkCheckout(t)
	goblin := ciGoblin{id: "task", repo: worktree, branch: "fix/daemon", pullRequests: []string{"https://github.com/fpresta0607/no-mistakes/pull/24"}, meta: state.TaskMeta{ID: "task", Worktree: worktree}}
	now := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
	goblinRepo := worktree

	t.Run("a recorded backoff stops the read", func(t *testing.T) {
		w := &fleetWakes{BackOff: map[string]time.Time{goblinRepo: now.Add(time.Hour)}}
		hosted := &hostedRunner{inner: execx.OSRunner{}, body: `{"baseRefName":"main","headRefName":"fix/daemon"}`}
		runner := githubPollRunner{commands: hosted, state: w, repo: goblinRepo, now: func() time.Time { return now }}
		resolved := resolveTarget(context.Background(), runner, goblin)
		if len(hosted.calls) != 0 {
			t.Fatalf("the base read reached gh during a recorded backoff: %v", hosted.calls)
		}
		if resolved.target != "" {
			t.Fatalf("target = %q, want nothing resolved during backoff", resolved.target)
		}
	})

	t.Run("a refusal records the backoff", func(t *testing.T) {
		w := &fleetWakes{}
		hosted := &hostedRunner{inner: execx.OSRunner{}, stderr: "HTTP 403: rate limit exceeded", code: 1}
		runner := githubPollRunner{commands: hosted, state: w, repo: goblinRepo, now: func() time.Time { return now }}
		resolveTarget(context.Background(), runner, goblin)
		if len(hosted.calls) != 1 {
			t.Fatalf("hosted reads = %d, want the one refused read", len(hosted.calls))
		}
		if until := w.BackOff[goblinRepo]; !until.After(now) {
			t.Fatalf("backoff = %v, want it recorded after the refusal", until)
		}
	})
}

// An unreadable base is consumed by the overlap read: the branch is reported as
// not measurable rather than measured against another base.
func TestAnUnreadableBaseIsReportedRatherThanMeasuredAgainstOrigin(t *testing.T) {
	worktree := forkCheckout(t)
	goblin := ciGoblin{id: "task", repo: worktree, branch: "fix/daemon", targetUnread: "https://github.com/fpresta0607/no-mistakes/pull/24", meta: state.TaskMeta{ID: "task", Worktree: worktree}}

	_, _, err := branchPaths(context.Background(), execx.OSRunner{}, goblin)

	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("branchPaths = %v, want the unreadable base reported", err)
	}
}

// The base remote is the one whose parsed repository is exactly the pull
// request's base repository: an owner alone or a suffix never matches, and a
// repository no remote holds, or another host, is an error.
func TestBaseTargetResolvesOnlyTheExactBaseRepository(t *testing.T) {
	worktree := forkCheckout(t)

	target, err := baseTarget(context.Background(), execx.OSRunner{}, worktree, "https://github.com/fpresta0607/no-mistakes/pull/24", "main")
	if err != nil || target != "fork/main" {
		t.Fatalf("baseTarget = %q, %v; want fork/main by the exact repository", target, err)
	}
	if target, err := baseTarget(context.Background(), execx.OSRunner{}, worktree, "https://github.com/kunchenguid/no-mistakes/pull/1597", "main"); err != nil || target != "origin/main" {
		t.Fatalf("baseTarget = %q, %v; want origin/main for the upstream base", target, err)
	}
	if _, err := baseTarget(context.Background(), execx.OSRunner{}, worktree, "https://github.com/someone/else/pull/1", "main"); err == nil {
		t.Fatal("a base repository no remote holds was resolved anyway")
	}
	if _, err := baseTarget(context.Background(), execx.OSRunner{}, worktree, "https://gitlab.com/fpresta0607/no-mistakes/pull/24", "main"); err == nil {
		t.Fatal("a pull request on another host was resolved")
	}
}

// A goblin that moved its branch since collection is re-resolved for the branch
// read now and read once more, through the same function overlapArea uses, so
// one branch's target is never applied to another. The two branches have
// different resolvable bases, so the exact paths tell the old target from the
// moved branch's own: measured against fork/main the area would also hold
// other-base-only.txt, and against fork/other-base it holds moved.txt alone.
func TestAMovedBranchIsReResolvedBeforeItsAreaIsRead(t *testing.T) {
	for _, c := range []struct {
		name       string
		when       string
		wantBranch string
		wantPaths  []string
		wantMoving bool
	}{
		{name: "moved once, then stable, is read against its own base", when: "during the retry", wantBranch: "another/branch", wantPaths: []string{"moved.txt"}},
		{name: "moved through both reads is read on the next pass", when: "always", wantMoving: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange: two bases, and a branch that holds what only the other
			// base carries.
			worktree := forkCheckout(t)
			gitIn(t, worktree, "checkout", "--quiet", "-b", "base-two")
			writeFixtureFile(t, worktree, "other-base-only.txt", "only the second base has this")
			gitIn(t, worktree, "add", "-A")
			gitIn(t, worktree, "commit", "--quiet", "-m", "base two")
			gitIn(t, worktree, "update-ref", "refs/remotes/fork/other-base", "HEAD")
			gitIn(t, worktree, "checkout", "--quiet", "-b", "another/branch")
			writeFixtureFile(t, worktree, "moved.txt", "the moved branch's own work")
			gitIn(t, worktree, "add", "-A")
			gitIn(t, worktree, "commit", "--quiet", "-m", "moved branch work")
			goblin := ciGoblin{
				id: "task", repo: worktree, branch: "fix/daemon", target: "fork/main",
				pullRequests: []string{
					"https://github.com/fpresta0607/no-mistakes/pull/24",
					"https://github.com/fpresta0607/no-mistakes/pull/25",
				},
				meta: state.TaskMeta{ID: "task", Worktree: worktree},
			}
			runner := &movingBranchRunner{inner: execx.OSRunner{}, when: c.when}
			service := &Service{}

			// Act
			paths, branch, err := service.areaBranchPaths(context.Background(), runner, goblin)

			// Assert
			if c.wantMoving {
				if !errors.Is(err, errGoblinMoving) {
					t.Fatalf("areaBranchPaths = %v, want the moved branch read on the next pass", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("areaBranchPaths: %v", err)
			}
			if branch != c.wantBranch {
				t.Fatalf("branch = %q, want the branch read now %q", branch, c.wantBranch)
			}
			if !slices.Equal(paths, c.wantPaths) {
				t.Fatalf("paths = %v, want exactly %v, the moved branch's own base", paths, c.wantPaths)
			}
		})
	}
}

// writeFixtureFile writes rel under dir, making its folder.
func writeFixtureFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// movingBranchRunner moves the branch as the first read ends, or answers with
// another branch from the start.
type movingBranchRunner struct {
	inner execx.Runner
	when  string
	reads int
}

func (r *movingBranchRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "gh" && len(req.Args) >= 2 && req.Args[0] == "pr" && req.Args[1] == "view" {
		// Each pull request names its own base and head branch.
		if strings.Contains(req.Args[2], "/pull/25") {
			return execx.Result{Stdout: []byte(`{"baseRefName":"other-base","headRefName":"another/branch"}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{"baseRefName":"main","headRefName":"fix/daemon"}`)}, nil
	}
	if req.Name == "git" && len(req.Args) == 2 && req.Args[0] == "branch" && req.Args[1] == "--show-current" {
		r.reads++
		switch {
		case r.when == "during the retry" && r.reads == 1:
			return execx.Result{Stdout: []byte("fix/daemon\n")}, nil
		case r.when == "during the retry":
			return execx.Result{Stdout: []byte("another/branch\n")}, nil
		case r.reads <= 2:
			// The first area read and its own re-read both settle on this branch,
			// so the first read completes on it.
			return execx.Result{Stdout: []byte("another/branch\n")}, nil
		default:
			// It moves again before the overlap re-read finishes, so the goblin
			// is working and its area is read on the next pass.
			return execx.Result{Stdout: []byte("third/branch\n")}, nil
		}
	}
	return r.inner.Run(ctx, req)
}

// A remote enumeration that fails says nothing about whether the base is
// checked out here, so it must never leave the area measured against the
// ordinary default. The success control is the same fixture with the read
// working: no remote holds the base, so the default legitimately stands.
func TestAFailedRemoteReadIsUnreadRatherThanMeasuredAgainstTheDefault(t *testing.T) {
	for _, c := range []struct {
		name        string
		prURL       string
		failRemotes bool
		wantErr     bool
	}{
		{name: "remote enumeration fails", prURL: "https://github.com/fpresta0607/no-mistakes/pull/24", failRemotes: true, wantErr: true},
		// No remote holds this base, which is a successful listing, so the
		// ordinary default stands and nothing is read.
		{name: "remote enumeration succeeds and holds no such base", prURL: "https://github.com/someone/else/pull/24"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange: origin and the default branch read fine, so only the
			// remote enumeration can decide this.
			worktree := forkCheckout(t)
			goblin := ciGoblin{
				id: "task", repo: worktree, branch: "fix/daemon",
				pullRequests: []string{c.prURL},
				meta:         state.TaskMeta{ID: "task", Worktree: worktree},
			}
			runner := &failingRemotesRunner{inner: execx.OSRunner{}, fail: c.failRemotes}
			service := &Service{}

			// Act
			_, _, err := service.areaBranchPaths(context.Background(), runner, goblin)

			// Assert
			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "could not be read") {
					t.Fatalf("areaBranchPaths = %v, want the unread base reported rather than a measured fallback", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("areaBranchPaths = %v, want the ordinary default to stand", err)
			}
		})
	}
}

// failingRemotesRunner fails the remote listing when asked, and runs every
// other command for real.
type failingRemotesRunner struct {
	inner execx.Runner
	fail  bool
}

func (r *failingRemotesRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if r.fail && req.Name == "git" && slices.Contains(req.Args, "remote") && slices.Contains(req.Args, "-v") {
		return execx.Result{ExitCode: 1, Stderr: []byte("fatal: not a git repository")}, nil
	}
	return r.inner.Run(ctx, req)
}
