package readcache

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// counter is git or sqlite3 as the cache's caller runs it, counting each
// process it starts and remembering the last request.
type counter struct {
	starts int
	last   execx.Request
	next   execx.Runner
}

func (c *counter) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	c.starts++
	c.last = req
	return c.next.Run(ctx, req)
}

// settledCache is a cache whose clock runs a minute ahead, so the files a
// test has just written are past the racy window.
func settledCache() *Cache {
	cache := New()
	cache.now = func() time.Time { return time.Now().Add(time.Minute) }
	return cache
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-10-19T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-19T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepository makes a repository with two commits on main, an origin whose
// HEAD names main, and an ignored node_modules folder.
func newRepository(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "remote.origin.url", "https://github.com/example/work.git")
	write(t, filepath.Join(dir, ".gitignore"), "node_modules/\n*.log\n")
	write(t, filepath.Join(dir, "main.go"), "package main\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "first")
	write(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	git(t, dir, "commit", "-qam", "second")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	write(t, filepath.Join(dir, "node_modules", "left-pad", "index.js"), "module.exports = 1\n")
	return dir
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// boardReads are the reads the board repeats every minute, as it runs
// them, each given the commit HEAD names and the one before it.
var boardReads = []struct {
	name string
	args func(head, first string) []string
}{
	{"commit", func(string, string) []string { return []string{"rev-parse", "HEAD"} }},
	{"verified commit", func(string, string) []string {
		return []string{"--no-pager", "-c", "core.quotepath=false", "rev-parse", "--verify", "HEAD"}
	}},
	{"default branch's commit", func(string, string) []string { return []string{"rev-parse", "origin/main"} }},
	{"missing ref", func(string, string) []string {
		return []string{"rev-parse", "--verify", "--quiet", "refs/remotes/origin/master"}
	}},
	{"refs", func(string, string) []string {
		return []string{"for-each-ref", "--format=%(HEAD)%09%(refname)%09%(objectname)", "refs/heads", "refs/remotes"}
	}},
	{"branch", func(string, string) []string { return []string{"branch", "--show-current"} }},
	{"origin's HEAD", func(string, string) []string {
		return []string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"}
	}},
	{"symbolic HEAD", func(string, string) []string {
		return []string{"--no-pager", "-c", "core.quotepath=false", "symbolic-ref", "--quiet", "HEAD"}
	}},
	{"short HEAD", func(string, string) []string {
		return []string{"--no-pager", "-c", "core.quotepath=false", "symbolic-ref", "--short", "HEAD"}
	}},
	{"origin", func(string, string) []string { return []string{"config", "--get", "remote.origin.url"} }},
	{"missing remote", func(string, string) []string { return []string{"config", "--get", "remote.upstream.url"} }},
	{"reflog", func(string, string) []string { return []string{"reflog", "--format=%gs"} }},
	{"worktrees", func(string, string) []string { return []string{"worktree", "list", "--porcelain"} }},
	{"ancestor of HEAD", func(_, first string) []string {
		return []string{"--no-pager", "-c", "core.quotepath=false", "merge-base", "--is-ancestor", first, "HEAD"}
	}},
	{"merge base", func(head, first string) []string { return []string{"merge-base", first, head} }},
	{"changed files", func(head, first string) []string {
		return []string{"diff", "--name-only", "--no-renames", "-z", first, head}
	}},
	{"status", func(string, string) []string {
		return append([]string{"--no-pager", "-c", "core.quotepath=false", "status"}, StatusArgs()...)
	}},
}

// A board refresh reads the same heads, branches, refs, reflog, worktrees
// and status of each goblin's worktree every minute. Once each read has
// answered, reading again a repository nobody changed starts no git, and
// answers what git answered.
func TestARepeatedReadOfAnUnchangedRepositoryStartsNoGit(t *testing.T) {
	for _, board := range boardReads {
		t.Run(board.name, func(t *testing.T) {
			// Arrange
			dir := newRepository(t)
			runner := &counter{next: execx.OSRunner{}}
			read := Runner{Cache: settledCache(), Next: runner}
			// As the board reads a worktree a goblin is using: a status that took
			// optional locks would rewrite its index.
			request := execx.Request{Dir: dir, Name: "git", Args: board.args(git(t, dir, "rev-parse", "HEAD"), git(t, dir, "rev-parse", "HEAD~1")), Env: append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")}
			// A status knows what git ignores from its first answer.
			first, err := read.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := read.Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			before := runner.starts

			// Act
			again, err := read.Run(context.Background(), request)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if runner.starts != before {
				t.Fatalf("an unchanged repository's read started git %d more times", runner.starts-before)
			}
			if !bytes.Equal(again.Stdout, first.Stdout) || again.ExitCode != first.ExitCode {
				t.Fatalf("the kept answer is %q (exit %d), git answered %q (exit %d)", again.Stdout, again.ExitCode, first.Stdout, first.ExitCode)
			}
		})
	}
}

// Whatever changes a read's answer is seen on the next read: a commit, a
// branch made or checked out, a ref fetched, origin's HEAD moved, the
// configuration, a worktree added or removed, a file edited or added.
func TestAChangeToARepositoryIsSeenOnTheNextRead(t *testing.T) {
	statusArgs := append([]string{"status"}, StatusArgs()...)
	tests := []struct {
		name   string
		args   []string
		change func(t *testing.T, dir string)
	}{
		{"commit", []string{"rev-parse", "HEAD"}, func(t *testing.T, dir string) { git(t, dir, "commit", "-q", "--allow-empty", "-m", "third") }},
		{"commit in the ref listing", []string{"for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"}, func(t *testing.T, dir string) {
			git(t, dir, "commit", "-q", "--allow-empty", "-m", "third")
		}},
		{"branch made", []string{"for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes"}, func(t *testing.T, dir string) { git(t, dir, "branch", "fix/other") }},
		{"branch checked out", []string{"branch", "--show-current"}, func(t *testing.T, dir string) { git(t, dir, "checkout", "-q", "-b", "fix/other") }},
		{"branch checked out, short", []string{"symbolic-ref", "--short", "HEAD"}, func(t *testing.T, dir string) { git(t, dir, "checkout", "-q", "-b", "fix/other") }},
		{"head detached", []string{"symbolic-ref", "--quiet", "HEAD"}, func(t *testing.T, dir string) { git(t, dir, "checkout", "-q", "--detach") }},
		{"checkout in the reflog", []string{"reflog", "--format=%gs"}, func(t *testing.T, dir string) { git(t, dir, "checkout", "-q", "-b", "fix/other") }},
		{"ref fetched", []string{"rev-parse", "origin/main"}, func(t *testing.T, dir string) { git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD~1") }},
		{"refs packed and fetched", []string{"rev-parse", "origin/main"}, func(t *testing.T, dir string) {
			git(t, dir, "pack-refs", "--all")
			git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD~1")
			git(t, dir, "pack-refs", "--all")
		}},
		{"origin's HEAD moved", []string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"}, func(t *testing.T, dir string) {
			git(t, dir, "update-ref", "refs/remotes/origin/trunk", "HEAD")
			git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
		}},
		{"origin changed", []string{"config", "--get", "remote.origin.url"}, func(t *testing.T, dir string) {
			git(t, dir, "config", "remote.origin.url", "https://github.com/example/moved.git")
		}},
		{"worktree added", []string{"worktree", "list", "--porcelain"}, func(t *testing.T, dir string) {
			git(t, dir, "worktree", "add", "-q", "-b", "fix/other", filepath.Join(filepath.Dir(dir), "other"))
		}},
		{"worktree folder removed", []string{"worktree", "list", "--porcelain"}, func(t *testing.T, dir string) {
			other := filepath.Join(filepath.Dir(dir), "other")
			git(t, dir, "worktree", "add", "-q", "-b", "fix/other", other)
			if _, err := (Runner{Cache: nil, Next: execx.OSRunner{}}).Run(context.Background(), execx.Request{Dir: dir, Name: "git", Args: []string{"worktree", "list", "--porcelain"}}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(other); err != nil {
				t.Fatal(err)
			}
		}},
		{"tracked file edited", statusArgs, func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { println() }\n")
		}},
		{"file added in a new folder", statusArgs, func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "internal", "new.go"), "package internal\n")
		}},
		{"ignored file now tracked", statusArgs, func(t *testing.T, dir string) {
			git(t, dir, "add", "-f", filepath.Join("node_modules", "left-pad", "index.js"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := newRepository(t)
			read := Runner{Cache: settledCache(), Next: execx.OSRunner{}}
			request := execx.Request{Dir: dir, Name: "git", Args: test.args, Env: append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")}
			for range 2 {
				if _, err := read.Run(context.Background(), request); err != nil {
					t.Fatal(err)
				}
			}
			before, err := read.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, dir)

			// Act
			after, err := read.Run(context.Background(), request)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := execx.OSRunner{}.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after.Stdout, fresh.Stdout) || after.ExitCode != fresh.ExitCode {
				t.Fatalf("after the change the read answered %q (exit %d), git answers %q (exit %d)", after.Stdout, after.ExitCode, fresh.Stdout, fresh.ExitCode)
			}
			if bytes.Equal(before.Stdout, after.Stdout) && before.ExitCode == after.ExitCode {
				t.Fatalf("the change left git's answer %q (exit %d) as it was, so it proves nothing", after.Stdout, after.ExitCode)
			}
		})
	}
}

// What git ignores changes no status, so a file written under an ignored
// folder, as a build or an install writes thousands, starts no git.
func TestAChangeGitIgnoresStartsNoGit(t *testing.T) {
	// Arrange
	dir := newRepository(t)
	runner := &counter{next: execx.OSRunner{}}
	read := Runner{Cache: settledCache(), Next: runner}
	request := execx.Request{Dir: dir, Name: "git", Args: append([]string{"status"}, StatusArgs()...), Env: append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")}
	for range 2 {
		if _, err := read.Run(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	before := runner.starts
	write(t, filepath.Join(dir, "node_modules", "left-pad", "index.js"), "module.exports = 2\n")
	write(t, filepath.Join(dir, "node_modules", "right-pad", "index.js"), "module.exports = 3\n")

	// Act
	if _, err := read.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	// Assert
	if runner.starts != before {
		t.Fatalf("a change under an ignored folder started git %d times", runner.starts-before)
	}
}

// A read the cache does not know, or one it cannot be sure of, runs git
// every time: a command it has no rule for, one in a folder below a
// worktree's top, one whose environment points git elsewhere, one git
// failed, and one whose files, read by their size and time, changed within
// the racy window, which a second write in the same tick could change again
// unseen.
func TestAReadTheCacheCannotBeSureOfRunsGitEveryTime(t *testing.T) {
	tests := []struct {
		name    string
		request func(dir string) execx.Request
		cache   func() *Cache
	}{
		{"unknown command", func(dir string) execx.Request {
			return execx.Request{Dir: dir, Name: "git", Args: []string{"log", "-1", "--format=%H"}}
		}, settledCache},
		{"folder below the top", func(dir string) execx.Request {
			return execx.Request{Dir: filepath.Join(dir, "node_modules"), Name: "git", Args: []string{"rev-parse", "HEAD"}}
		}, settledCache},
		{"environment naming another repository", func(dir string) execx.Request {
			return execx.Request{Dir: dir, Name: "git", Args: []string{"rev-parse", "HEAD"}, Env: append(os.Environ(), "GIT_DIR="+filepath.Join(dir, ".git"))}
		}, settledCache},
		{"failed read", func(dir string) execx.Request {
			return execx.Request{Dir: dir, Name: "git", Args: []string{"rev-parse", "origin/nothing"}}
		}, settledCache},
		{"ref files changed just now", func(dir string) execx.Request {
			return execx.Request{Dir: dir, Name: "git", Args: []string{"for-each-ref", "--format=%(refname)", "refs/heads"}}
		}, New},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := newRepository(t)
			git(t, dir, "commit", "-q", "--allow-empty", "-m", "just now")
			runner := &counter{next: execx.OSRunner{}}
			read := Runner{Cache: test.cache(), Next: runner}
			if _, err := read.Run(context.Background(), test.request(dir)); err != nil {
				t.Fatal(err)
			}

			// Act
			if _, err := read.Run(context.Background(), test.request(dir)); err != nil {
				t.Fatal(err)
			}

			// Assert
			if runner.starts != 2 {
				t.Fatalf("two reads started git %d times, want 2", runner.starts)
			}
		})
	}
}

// A read that starts no other program runs with no console, so no console
// host starts with it; a status, which may run the repository's file system
// monitor hook, keeps the console a start is given.
func TestOnlyAReadThatStartsNothingRunsWithNoConsole(t *testing.T) {
	tests := []struct {
		args          []string
		isConsoleless bool
	}{
		{[]string{"rev-parse", "HEAD"}, true},
		{[]string{"worktree", "list", "--porcelain"}, true},
		{append([]string{"status"}, StatusArgs()...), false},
		{[]string{"log", "-1"}, false},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			// Arrange
			runner := &counter{next: execx.OSRunner{}}

			// Act
			if _, err := (Runner{Cache: settledCache(), Next: runner}).Run(context.Background(), execx.Request{Dir: newRepository(t), Name: "git", Args: test.args}); err != nil {
				t.Fatal(err)
			}

			// Assert
			if runner.last.NoConsole != test.isConsoleless {
				t.Fatalf("NoConsole = %t, want %t", runner.last.NoConsole, test.isConsoleless)
			}
		})
	}
}

// fixedAnswer answers every request with the same output, as sqlite3 would
// answer a query of an unchanged database.
type fixedAnswer struct{}

func (fixedAnswer) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{Stdout: []byte(`[{"n":1}]`)}, nil
}

// The board queries the gate's database for each goblin's run every minute.
// A query of a database nobody wrote to starts no sqlite3, and a commit,
// which rewrites the header of the write-ahead log's index, or a write to
// the database file itself, is seen on the next query.
func TestADatabaseQueryRunsAgainOnlyAfterAWrite(t *testing.T) {
	tests := []struct {
		name      string
		write     func(t *testing.T, database string)
		isWritten bool
	}{
		{"nothing written", func(*testing.T, string) {}, false},
		{"commit to the log", func(t *testing.T, database string) { write(t, database+"-shm", strings.Repeat("b", 136)) }, true},
		{"log grown", func(t *testing.T, database string) { write(t, database+"-wal", strings.Repeat("w", 8192)) }, true},
		{"database rewritten", func(t *testing.T, database string) { write(t, database, strings.Repeat("d", 8192)) }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			database := filepath.Join(t.TempDir(), "state.sqlite")
			write(t, database, strings.Repeat("d", 4096))
			write(t, database+"-wal", strings.Repeat("w", 4096))
			write(t, database+"-shm", strings.Repeat("a", 136))
			runner := &counter{next: fixedAnswer{}}
			cache := New()
			cache.now = func() time.Time { return time.Now().Add(time.Minute) }
			read := Runner{Cache: cache, Next: runner}
			request := execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", database, "SELECT 1 AS n"}}
			if _, err := read.Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			test.write(t, database)

			// Act
			if _, err := read.Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}

			// Assert
			if want := map[bool]int{false: 1, true: 2}[test.isWritten]; runner.starts != want {
				t.Fatalf("sqlite3 ran %d times, want %d", runner.starts, want)
			}
			if !runner.last.NoConsole {
				t.Fatal("sqlite3, which starts nothing, ran with a console")
			}
		})
	}
}

// ghAccount is gh answering the account it works as, counting the times it
// is asked.
type ghAccount struct{ asked int }

func (g *ghAccount) Run(context.Context, execx.Request) (execx.Result, error) {
	g.asked++
	return execx.Result{Stdout: []byte("fleet-account\n")}, nil
}

// The CI poll asks gh which account it works as on each poll that needs it.
// That changes only when gh signs in, out or switches accounts, each of
// which rewrites gh's hosts file, so gh is asked again only then.
func TestTheAccountGhWorksAsIsAskedAgainOnlyWhenGhsHostsChange(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(t *testing.T, hosts string)
		wantAsked int
	}{
		{"hosts unchanged", func(*testing.T, string) {}, 1},
		{"account switched", func(t *testing.T, hosts string) { write(t, hosts, "github.com:\n    user: other-account\n") }, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			folder := t.TempDir()
			t.Setenv("GH_CONFIG_DIR", folder)
			hosts := filepath.Join(folder, "hosts.yml")
			write(t, hosts, "github.com:\n    user: fleet-account\n")
			gh := &ghAccount{}
			read := Runner{Cache: settledCache(), Next: gh}
			request := execx.Request{Dir: t.TempDir(), Name: "gh", Args: []string{"api", "user", "--jq", ".login"}}
			if _, err := read.Run(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			test.change(t, hosts)

			// Act
			again, err := read.Run(context.Background(), request)

			// Assert
			if err != nil || string(again.Stdout) != "fleet-account\n" {
				t.Fatalf("the read answered %q, %v", again.Stdout, err)
			}
			if gh.asked != test.wantAsked {
				t.Fatalf("gh was asked %d times, want %d", gh.asked, test.wantAsked)
			}
		})
	}
}
