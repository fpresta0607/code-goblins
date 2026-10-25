package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// gitAt runs git in dir and returns its trimmed output, failing the test with
// the command and its error when git refuses.
func gitAt(t *testing.T, dir string, args ...string) string {
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


// gitHas reports whether rev:path exists in the repository at dir.
func gitHas(t *testing.T, dir, revPath string) error {
	t.Helper()
	command := exec.Command("git", "cat-file", "-e", revPath)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(os.TempDir(), "no-gitconfig"), "GIT_CONFIG_SYSTEM="+filepath.Join(os.TempDir(), "no-gitconfig"))
	_, err := command.CombinedOutput()
	return err
}

// writeAt writes rel under dir, making its folder.
func writeAt(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A goblin's branch is measured against the branch its pull request targets,
// not against an upstream tracking ref that is older than what the branch
// already inherited. On 2026-10-24 two overlap alerts named docs a fork branch
// never touched: its origin is the upstream project, the branch had inherited
// a newer upstream main than its own origin/main ref recorded, and the diff
// from that stale ref counted upstream's commit as the branch's work.
//
// The fixture is the real supported shape: origin is upstream, a fork remote
// holds the fork's own main which already contains the inherited doc, and the
// published feature tracks fork/fix/daemon. The delta must carry the shared
// file the goblin changed and must not carry the inherited doc.
func TestOverlapReadsTheForkDeltaFromTheTargetBranchNotAStaleUpstreamRef(t *testing.T) {
	// Arrange: upstream gains a doc; the fork inherits it and commits its own main.
	root := t.TempDir()
	upstream, fork, work := filepath.Join(root, "upstream"), filepath.Join(root, "fork"), filepath.Join(root, "work")
	for _, dir := range []string{upstream, fork} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitAt(t, dir, "init", "--quiet", "--initial-branch=main")
	}
	writeAt(t, upstream, "root.md", "the root both share\n")
	gitAt(t, upstream, "add", "-A")
	gitAt(t, upstream, "commit", "--quiet", "-m", "root")
	writeAt(t, upstream, "upstream-main.md", "upstream's own main\n")
	gitAt(t, upstream, "add", "-A")
	gitAt(t, upstream, "commit", "--quiet", "-m", "upstream main")
	beforeDoc := gitAt(t, upstream, "rev-parse", "HEAD")
	writeAt(t, upstream, "docs/upstream-only.md", "a doc upstream wrote\n")
	gitAt(t, upstream, "add", "-A")
	gitAt(t, upstream, "commit", "--quiet", "-m", "upstream doc")

	// The fork inherits the doc, so its own main already holds it.
	gitAt(t, fork, "fetch", "--quiet", upstream, "main")
	gitAt(t, fork, "reset", "--quiet", "--hard", "FETCH_HEAD")
	writeAt(t, fork, "fork-main.md", "the fork's own main\n")
	gitAt(t, fork, "add", "-A")
	gitAt(t, fork, "commit", "--quiet", "-m", "fork main")

	// The goblin's checkout has upstream as origin and the fork as its own
	// remote, and its origin/main ref is stale: it stops before the doc.
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, work, "init", "--quiet", "--initial-branch=main")
	gitAt(t, work, "remote", "add", "origin", upstream)
	gitAt(t, work, "remote", "add", "fork", fork)
	gitAt(t, work, "fetch", "--quiet", "origin", "main")
	gitAt(t, work, "fetch", "--quiet", "fork", "main")
	gitAt(t, work, "update-ref", "refs/remotes/origin/main", beforeDoc)

	// The feature comes from the fork's main, changes one file a teammate's
	// pull request also changes, and is published and tracking itself.
	gitAt(t, work, "checkout", "--quiet", "-b", "fix/daemon", "fork/main")
	writeAt(t, work, "shared.go", "package shared // the goblin's own change\n")
	gitAt(t, work, "add", "-A")
	gitAt(t, work, "commit", "--quiet", "-m", "the goblin's change")
	gitAt(t, work, "push", "--quiet", "fork", "fix/daemon")
	gitAt(t, work, "config", "branch.fix/daemon.remote", "fork")
	gitAt(t, work, "config", "branch.fix/daemon.merge", "refs/heads/fix/daemon")

	// The identities the fixture claims, before any production code runs.
	if got := gitAt(t, work, "remote", "get-url", "origin"); got != upstream {
		t.Fatalf("origin = %q, want the upstream %q", got, upstream)
	}
	if got := gitAt(t, work, "remote", "get-url", "fork"); got != fork {
		t.Fatalf("fork remote = %q, want the fork %q", got, fork)
	}
	if got := gitAt(t, work, "rev-parse", "--abbrev-ref", "fix/daemon@{upstream}"); got != "fork/fix/daemon" {
		t.Fatalf("the published feature tracks %q, want fork/fix/daemon", got)
	}
	if err := gitHas(t, work, "fork/main:docs/upstream-only.md"); err != nil {
		t.Fatalf("the fork's main must already hold the inherited doc: %v", err)
	}
	if err := gitHas(t, work, "origin/main:docs/upstream-only.md"); err == nil {
		t.Fatal("origin/main must stop before the doc, so the stale ref is staged")
	}

	// The target comes from the pull request's own base, resolved to the local
	// remote that holds it: the fork, not the upstream origin.
	goblin := ciGoblin{id: "task", repo: work, branch: "fix/daemon", target: "fork/main", meta: state.TaskMeta{ID: "task", Worktree: work}}

	// Act
	paths, _, err := branchPaths(context.Background(), execx.OSRunner{}, goblin)

	// Assert
	if err != nil {
		t.Fatalf("branch paths: %v", err)
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "shared.go") {
		t.Fatalf("the goblin's own changed file is missing from its delta: %v", paths)
	}
	if strings.Contains(joined, "docs/upstream-only.md") {
		t.Fatalf("an inherited upstream doc was counted as the branch's own work: %v", paths)
	}
}
