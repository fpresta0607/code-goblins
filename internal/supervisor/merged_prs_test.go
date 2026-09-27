package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// forkedRepos makes an upstream repository whose PR 126 merged, and a fork
// copied from it that carries that merge commit and merged its own PR 1. It
// returns both checkouts and the merged head of PR 126, the merge commit's
// second parent.
func forkedRepos(t *testing.T) (upstream, fork, head126 string) {
	t.Helper()
	dir := t.TempDir()
	upstream, fork = filepath.Join(dir, "code-goblins"), filepath.Join(dir, "code-goblins-native")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2026-09-26T16:00:00Z", "GIT_AUTHOR_DATE=2026-09-26T16:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	git(upstream, "init", "-q", "--initial-branch=main")
	git(upstream, "config", "user.email", "t@t")
	git(upstream, "config", "user.name", "t")
	git(upstream, "commit", "-q", "--allow-empty", "-m", "base")
	git(upstream, "switch", "-q", "-c", "feat/board-first-run")
	git(upstream, "commit", "-q", "--allow-empty", "-m", "first run")
	head126 = git(upstream, "rev-parse", "HEAD")
	git(upstream, "switch", "-q", "main")
	git(upstream, "merge", "-q", "--no-ff", "feat/board-first-run", "-m", "Merge pull request #126 from o/feat/board-first-run")
	git(dir, "clone", "-q", upstream, fork)
	git(fork, "config", "user.email", "t@t")
	git(fork, "config", "user.name", "t")
	git(fork, "switch", "-q", "-c", "feat/native-shell")
	git(fork, "commit", "-q", "--allow-empty", "-m", "native shell")
	git(fork, "switch", "-q", "main")
	git(fork, "merge", "-q", "--no-ff", "feat/native-shell", "-m", "Merge pull request #1 from o/feat/native-shell")
	for repo, name := range map[string]string{upstream: "code-goblins", fork: "code-goblins-native"} {
		git(repo, "config", "remote.origin.url", "https://github.com/o/"+name+".git")
		git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	}
	return upstream, fork, head126
}

// prHeads answers refs/pull/<n>/head for the repositories it knows, as
// GitHub would, and records every question.
type prHeads struct {
	mu    sync.Mutex
	heads map[string]string
	asked []string
}

func (p *prHeads) lookup(_ context.Context, repo, number string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, filepath.Base(repo)+"#"+number)
	if head, ok := p.heads[filepath.Base(repo)+"#"+number]; ok {
		return head, nil
	}
	return "", errors.New("no such pull request")
}

func TestGitMergedPRsListsAForkCarriedMergeOnceUnderTheRepositoryItWasOpenedIn(t *testing.T) {
	upstream, fork, head126 := forkedRepos(t)
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		repos []string
		heads map[string]string
	}{
		{name: "the upstream listed first", repos: []string{upstream, fork}, heads: map[string]string{"code-goblins#126": head126}},
		{name: "the fork listed first", repos: []string{fork, upstream}, heads: map[string]string{"code-goblins#126": head126}},
		{name: "the fork has a PR 126 of its own that is another change", repos: []string{fork, upstream}, heads: map[string]string{"code-goblins#126": head126, "code-goblins-native#126": "0123456789abcdef0123456789abcdef01234567"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			heads := &prHeads{heads: test.heads}

			// Act
			merged, err := gitMergedPRs(test.repos, heads.lookup)(t.Context(), since, 10)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var listed []string
			for _, pr := range merged {
				listed = append(listed, pr.PR+" in "+filepath.Base(pr.Project))
			}
			slices.Sort(listed)
			want := []string{
				"https://github.com/o/code-goblins-native/pull/1 in code-goblins-native",
				"https://github.com/o/code-goblins/pull/126 in code-goblins",
			}
			if !slices.Equal(listed, want) {
				t.Fatalf("Completed lists %v, want %v", listed, want)
			}
			if slices.Contains(heads.asked, "code-goblins-native#1") {
				t.Fatalf("asked about a merge only one repository holds: %v", heads.asked)
			}
		})
	}
}

func TestGitMergedPRsListsASharedMergeOnceWhenNoRepositoryCanSay(t *testing.T) {
	// Arrange
	upstream, fork, _ := forkedRepos(t)
	heads := &prHeads{}

	// Act
	merged, err := gitMergedPRs([]string{upstream, fork}, heads.lookup)(t.Context(), time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), 10)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, pr := range merged {
		if strings.HasSuffix(pr.PR, "/pull/126") {
			count++
			if pr.PR != "https://github.com/o/code-goblins/pull/126" {
				t.Errorf("an unconfirmed shared merge went to %s, want the first repository listed", pr.PR)
			}
		}
	}
	if count != 1 {
		t.Fatalf("PR 126 is listed %d times, want once: %+v", count, merged)
	}
}
