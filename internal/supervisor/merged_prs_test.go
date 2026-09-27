package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// forkedRepos makes an upstream repository whose PRs 126 onward, shared of
// them, merged, and a fork copied from it that carries those merge commits
// and merged its own PR 1. It returns both checkouts and each upstream pull
// request's merged head, the merge commit's second parent, by number.
func forkedRepos(t *testing.T, shared int) (upstream, fork string, heads map[string]string) {
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
	heads = map[string]string{}
	for number := 126; number < 126+shared; number++ {
		branch := fmt.Sprintf("feat/change-%d", number)
		git(upstream, "switch", "-q", "-c", branch)
		git(upstream, "commit", "-q", "--allow-empty", "-m", branch)
		heads[fmt.Sprint(number)] = git(upstream, "rev-parse", "HEAD")
		git(upstream, "switch", "-q", "main")
		git(upstream, "merge", "-q", "--no-ff", branch, "-m", fmt.Sprintf("Merge pull request #%d from o/%s", number, branch))
	}
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
	return upstream, fork, heads
}

// pullListings answers each repository's listing of pull request heads, as
// its origin on GitHub would, and records which repositories were asked.
type pullListings struct {
	heads map[string]map[string]string
	err   error
	asked []string
}

func (p *pullListings) list(_ context.Context, repo string) (map[string]string, error) {
	p.asked = append(p.asked, filepath.Base(repo))
	if p.err != nil {
		return nil, p.err
	}
	return p.heads[filepath.Base(repo)], nil
}

func listedPRs(merged []MergedPR) []string {
	var listed []string
	for _, pr := range merged {
		listed = append(listed, pr.PR+" in "+filepath.Base(pr.Project))
	}
	slices.Sort(listed)
	return listed
}

var since20 = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

func TestGitMergedPRsListsAForkCarriedMergeOnceUnderTheRepositoryItWasOpenedIn(t *testing.T) {
	upstream, fork, heads := forkedRepos(t, 1)
	tests := []struct {
		name     string
		repos    []string
		listings map[string]map[string]string
	}{
		{name: "the upstream listed first", repos: []string{upstream, fork}, listings: map[string]map[string]string{"code-goblins": {"126": heads["126"]}}},
		{name: "the fork listed first", repos: []string{fork, upstream}, listings: map[string]map[string]string{"code-goblins": {"126": heads["126"]}}},
		{name: "the fork has a PR 126 of its own that is another change", repos: []string{fork, upstream}, listings: map[string]map[string]string{"code-goblins": {"126": heads["126"]}, "code-goblins-native": {"126": "0123456789abcdef0123456789abcdef01234567"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			listings := &pullListings{heads: test.listings}

			// Act
			merged, err := gitMergedPRs(test.repos, listings.list, time.Now)(t.Context(), since20)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"https://github.com/o/code-goblins-native/pull/1 in code-goblins-native",
				"https://github.com/o/code-goblins/pull/126 in code-goblins",
			}
			if listed := listedPRs(merged); !slices.Equal(listed, want) {
				t.Fatalf("Completed lists %v, want %v", listed, want)
			}
		})
	}
}

func TestGitMergedPRsListsASharedMergeOnceUnderTheFirstRepositoryWhenNoOriginSays(t *testing.T) {
	upstream, fork, _ := forkedRepos(t, 1)
	failure := errors.New("origin could not be reached")
	for _, test := range []struct {
		name     string
		listings *pullListings
		wantErr  error
	}{
		{name: "no listing names it", listings: &pullListings{}},
		{name: "the listing fails, which is reported", listings: &pullListings{err: failure}, wantErr: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			merged, err := gitMergedPRs([]string{upstream, fork}, test.listings.list, time.Now)(t.Context(), since20)

			// Assert
			if !errors.Is(err, test.wantErr) || (test.wantErr == nil) != (err == nil) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			want := []string{
				"https://github.com/o/code-goblins-native/pull/1 in code-goblins-native",
				"https://github.com/o/code-goblins/pull/126 in code-goblins",
			}
			if listed := listedPRs(merged); !slices.Equal(listed, want) {
				t.Fatalf("Completed lists %v, want %v", listed, want)
			}
		})
	}
}

// A fork carries every upstream merge of the week, so asking about each one
// held the board's loop for minutes: 153 shared merges on 2026-09-27.
func TestGitMergedPRsAsksEachRepositoryOnceForAWeekOfSharedMerges(t *testing.T) {
	// Arrange
	upstream, fork, heads := forkedRepos(t, 3)
	listings := &pullListings{heads: map[string]map[string]string{"code-goblins": heads}}
	clock := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	list := gitMergedPRs([]string{fork, upstream}, listings.list, func() time.Time { return clock })

	// Act
	merged, err := list(t.Context(), since20)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 {
		t.Fatalf("Completed lists %v, want PR 1 and the three shared merges once each", listedPRs(merged))
	}
	for _, pr := range merged {
		if strings.Contains(pr.PR, "/pull/12") && filepath.Base(pr.Project) != "code-goblins" {
			t.Fatalf("%s listed under %s, want the upstream it was opened in", pr.PR, pr.Project)
		}
	}
	slices.Sort(listings.asked)
	if !slices.Equal(listings.asked, []string{"code-goblins", "code-goblins-native"}) {
		t.Fatalf("asked %v for three shared merges, want each repository once", listings.asked)
	}
	listings.asked = nil
	clock = clock.Add(pullHeadsRecheck - time.Second)
	if _, err := list(t.Context(), since20); err != nil || len(listings.asked) != 0 {
		t.Fatalf("within the recheck interval asked %v (error %v), want nothing", listings.asked, err)
	}
	clock = clock.Add(time.Second)
	if _, err := list(t.Context(), since20); err != nil || !slices.Equal(listings.asked, []string{"code-goblins-native"}) {
		t.Fatalf("after the recheck interval asked %v (error %v), want only the fork, whose listing lacks those pull requests", listings.asked, err)
	}
}
